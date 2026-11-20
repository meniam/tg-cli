package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"tg-cli/internal/chatref"
	"tg-cli/internal/stem"
	"tg-cli/internal/textnorm"
)

// ErrEmptyQuery means search input contains no searchable terms.
var ErrEmptyQuery = errors.New("empty search query")

// ErrInvalidSearchOptions identifies an unknown scope or author.
var ErrInvalidSearchOptions = errors.New("invalid search options")

// SearchOptions controls per-scope FTS queries.
type SearchOptions struct {
	Query          string
	Scopes         []string
	Author         string
	ChatID         *int64
	ChatFilter     ChatFilter
	Limit          int
	Prefix         bool
	IncludeDeleted bool
}

// SearchHit is a ranked result from one scope.
type SearchHit struct {
	Scope    string
	ID       int64
	ChatID   *int64
	Title    string
	Snippet  string
	Date     int64
	Deleted  bool
	Author   string
	Model    *string
	NoteKind string
	// Rank is lower-is-better in every scope: bm25 for chats, people and notes,
	// the negated RRF score for messages.
	Rank float64
	// Link and LinkUnavailable are set for message hits only. Link is a t.me
	// link, nil if the chat has none; LinkUnavailable then says why.
	Link            *string
	LinkUnavailable string
}

var searchScopeOrder = []string{"chats", "people", "messages", "notes"}

// Search returns ranked hits grouped in chats, people, messages, notes order.
func (db *DB) Search(ctx context.Context, options SearchOptions) ([]SearchHit, error) {
	match := textnorm.Query(options.Query, options.Prefix)
	if match == "" {
		return nil, ErrEmptyQuery
	}
	if options.Author != "" && !validNoteAuthor(options.Author) {
		return nil, fmt.Errorf("%w: unknown author %q", ErrInvalidSearchOptions, options.Author)
	}
	enabled := make(map[string]bool, len(searchScopeOrder))
	if len(options.Scopes) == 0 {
		for _, scope := range searchScopeOrder {
			enabled[scope] = true
		}
	} else {
		for _, scope := range options.Scopes {
			switch scope {
			case "chats", "people", "messages", "notes":
				enabled[scope] = true
			default:
				return nil, fmt.Errorf("%w: unknown scope %q", ErrInvalidSearchOptions, scope)
			}
		}
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}
	terms := queryWords(options.Query)
	var hits []SearchHit
	for _, scope := range searchScopeOrder {
		if !enabled[scope] {
			continue
		}
		var found []SearchHit
		var err error
		switch scope {
		case "chats":
			found, err = db.searchChats(ctx, match, limit, terms, options.Prefix, options.ChatFilter)
		case "people":
			found, err = db.searchPeople(ctx, match, limit, terms, options.Prefix, options.ChatFilter)
		case "messages":
			found, err = db.searchMessages(ctx, match, limit, terms, options)
		case "notes":
			found, err = db.searchNotes(ctx, match, limit, terms, options)
		}
		if err != nil {
			return nil, fmt.Errorf("search %s: %w", scope, err)
		}
		hits = append(hits, found...)
	}
	return hits, nil
}

func (db *DB) searchChats(ctx context.Context, match string, limit int, terms []string, prefix bool, filter ChatFilter) ([]SearchHit, error) {
	condition, filterArgs := filter.condition("c.id", false)
	args := append([]any{match}, filterArgs...)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, `SELECT c.id,c.title,c.about,bm25(chats_fts) FROM chats_fts JOIN chats c ON c.rowid=chats_fts.rowid WHERE chats_fts MATCH ?`+condition+` ORDER BY bm25(chats_fts),c.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		var about *string
		if err := rows.Scan(&hit.ID, &hit.Title, &about, &hit.Rank); err != nil {
			return nil, err
		}
		hit.Scope = "chats"
		hit.Snippet = makeSnippet(chooseSnippetSource(terms, prefix, textValue(about), hit.Title), terms, prefix)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (db *DB) searchPeople(ctx context.Context, match string, limit int, terms []string, prefix bool, filter ChatFilter) ([]SearchHit, error) {
	condition, filterArgs := filter.condition("p.id", false)
	args := append([]any{match}, filterArgs...)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, `SELECT p.id,p.first_name,p.last_name,p.username,p.bio,bm25(people_fts) FROM people_fts JOIN people p ON p.rowid=people_fts.rowid WHERE people_fts MATCH ?`+condition+` ORDER BY bm25(people_fts),p.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		var first, last, username, bio *string
		if err := rows.Scan(&hit.ID, &first, &last, &username, &bio, &hit.Rank); err != nil {
			return nil, err
		}
		hit.Scope = "people"
		hit.Title = strings.TrimSpace(textValue(first) + " " + textValue(last))
		if hit.Title == "" {
			hit.Title = textValue(username)
		}
		hit.Snippet = makeSnippet(chooseSnippetSource(terms, prefix, textValue(bio), hit.Title), terms, prefix)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// rrfK is the Reciprocal Rank Fusion constant: a message at rank r in a leg
// adds 1/(rrfK+r) to its score. 60 is the value from the original RRF paper
// and the one brain-agent uses.
const rrfK = 60

// messageCandidate is one message found by a search leg, before fusion.
type messageCandidate struct {
	hit  SearchHit
	text string
	// topicID is the forum topic of the message, 0 if it has none.
	topicID int64
}

// RRFK is the fusion constant of rrfK, for fusion done outside this package
// (the semantic search fuses fragments, not messages).
const RRFK = rrfK

// searchMessages fuses two legs by RRF: the exact one over messages_fts (finds
// numbers, part numbers and names as written) and the stemmed one over
// messages_stem_fts (finds other word forms). Each leg contributes its
// limit*3 best messages, the fused list is cut to limit.
func (db *DB) searchMessages(ctx context.Context, match string, limit int, terms []string, options SearchOptions) ([]SearchHit, error) {
	legSize := limit * 3
	exact, err := db.messageLeg(ctx, "messages_fts", match, legSize, options)
	if err != nil {
		return nil, err
	}
	var stemmed []messageCandidate
	stemMatch := stem.Query(options.Query, options.Prefix)
	if stemMatch != "" {
		if stemmed, err = db.messageLeg(ctx, "messages_stem_fts", stemMatch, legSize, options); err != nil {
			return nil, err
		}
	}

	fusedLegs := fuseRRF(limit, exact, stemmed)
	stemTerms := stem.Terms(options.Query)
	hits := make([]SearchHit, 0, len(fusedLegs))
	for _, entry := range fusedLegs {
		hit := entry.candidate.hit
		// Rank stays "lower is better" like bm25 in the other scopes.
		hit.Rank = -entry.score
		hit.Snippet = messageSnippet(entry.candidate.text, terms, stemTerms, options.Prefix)
		hits = append(hits, hit)
	}
	return hits, nil
}

// fusedMessage is a message with its Reciprocal Rank Fusion score.
type fusedMessage struct {
	candidate messageCandidate
	score     float64
}

// fuseRRF merges ranked legs: a message at rank r (from 1) in a leg adds
// 1/(rrfK+r), messages are identified by chat and id, the best score comes
// first and the result is cut to limit. Among equal scores the earlier leg and
// the better rank win, because the sort is stable over first appearance.
func fuseRRF(limit int, legs ...[]messageCandidate) []*fusedMessage {
	var total int
	for _, leg := range legs {
		total += len(leg)
	}
	byKey := make(map[[2]int64]*fusedMessage, total)
	order := make([]*fusedMessage, 0, total)
	for _, leg := range legs {
		for rank, candidate := range leg {
			key := [2]int64{*candidate.hit.ChatID, candidate.hit.ID}
			entry := byKey[key]
			if entry == nil {
				entry = &fusedMessage{candidate: candidate}
				byKey[key] = entry
				order = append(order, entry)
			}
			entry.score += 1 / float64(rrfK+rank+1)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].score > order[j].score })
	if len(order) > limit {
		order = order[:limit]
	}
	return order
}

// messageLeg runs one full-text leg over a messages FTS table. The table name
// is one of two constants, never user input; the query goes through MATCH as a
// bound argument. Filters (chat, closed chats, deleted) are identical in both
// legs.
func (db *DB) messageLeg(ctx context.Context, table, match string, limit int, options SearchOptions) ([]messageCandidate, error) {
	query := `SELECT m.id,m.chat_id,m.text,m.date,m.deleted_at,m.topic_id,c.type,c.username FROM ` + table + ` JOIN messages m ON m.rowid=` + table + `.rowid LEFT JOIN chats c ON c.id=m.chat_id WHERE ` + table + ` MATCH ?`
	args := []any{match}
	if options.ChatID != nil {
		query += ` AND m.chat_id=?`
		args = append(args, *options.ChatID)
	}
	condition, filterArgs := options.ChatFilter.condition("m.chat_id", false)
	query += condition
	args = append(args, filterArgs...)
	if !options.IncludeDeleted {
		query += ` AND m.deleted_at IS NULL`
	}
	query += ` ORDER BY bm25(` + table + `),m.rowid LIMIT ?`
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []messageCandidate
	for rows.Next() {
		var candidate messageCandidate
		var deletedAt, topicID *int64
		var chatID int64
		var chatType, username sql.NullString
		if err := rows.Scan(&candidate.hit.ID, &chatID, &candidate.text, &candidate.hit.Date, &deletedAt, &topicID, &chatType, &username); err != nil {
			return nil, err
		}
		candidate.hit.ChatID = &chatID
		candidate.hit.Scope = "messages"
		candidate.hit.Deleted = deletedAt != nil
		if topicID != nil {
			candidate.topicID = *topicID
		}
		candidate.hit.Link, candidate.hit.LinkUnavailable = MessageLink(chatID, candidate.hit.ID, chatType.String, username.String, topicID)
		found = append(found, candidate)
	}
	return found, rows.Err()
}

// Reasons a message has no web link.
const (
	linkPrivateChat = "private_chat"
	linkBasicGroup  = "basic_group"
)

// MessageLink builds the t.me link of a stored message without asking Telegram,
// from the chat type and username in chats and the message's forum topic. Only
// channels and supergroups have web links. Direct chats and basic groups give
// no link and a reason; an unknown chat or a username Telegram would not
// accept gives no link and no reason. The link names the chat and the message
// number only, never any text.
func MessageLink(chatID, messageID int64, chatType, username string, topicID *int64) (*string, string) {
	switch chatType {
	case "user", "bot":
		return nil, linkPrivateChat
	case "group":
		return nil, linkBasicGroup
	case "supergroup", "channel":
		ref := chatref.MessageRef{Chat: chatref.Ref{Kind: chatref.ID, ID: chatID}, ID: int(messageID)}
		if topicID != nil && *topicID > 0 {
			ref.Topic = int(*topicID)
		}
		link, err := chatref.FormatMessageLink(ref, username)
		if err != nil {
			return nil, ""
		}
		return &link, ""
	}
	return nil, ""
}

func (db *DB) searchNotes(ctx context.Context, match string, limit int, terms []string, options SearchOptions) ([]SearchHit, error) {
	query := `SELECT n.id,n.chat_id,n.body,n.author,n.model,n.kind,bm25(notes_fts) FROM notes_fts JOIN notes n ON n.rowid=notes_fts.rowid WHERE notes_fts MATCH ?`
	args := []any{match}
	if options.Author != "" {
		query += ` AND n.author=?`
		args = append(args, options.Author)
	}
	if options.ChatID != nil {
		query += ` AND n.chat_id=?`
		args = append(args, *options.ChatID)
	}
	condition, filterArgs := options.ChatFilter.condition("n.chat_id", true)
	query += condition
	args = append(args, filterArgs...)
	query += ` ORDER BY bm25(notes_fts),n.id LIMIT ?`
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		var body string
		if err := rows.Scan(&hit.ID, &hit.ChatID, &body, &hit.Author, &hit.Model, &hit.NoteKind, &hit.Rank); err != nil {
			return nil, err
		}
		hit.Scope = "notes"
		hit.Snippet = makeSnippet(body, terms, options.Prefix)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func queryWords(query string) []string {
	return strings.FieldsFunc(textnorm.Normalize(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func firstWordMatch(source string, terms []string, prefix bool) (int, int, bool) {
	return firstMatch(source, func(word string) bool { return wordMatches(word, terms, prefix) })
}

// firstStemMatch is firstWordMatch over word stems: it finds the first word of
// source whose stem is one of stemTerms (the stemmed query terms).
func firstStemMatch(source string, stemTerms []string, prefix bool) (int, int, bool) {
	return firstMatch(source, func(word string) bool { return wordMatches(stem.Word(word), stemTerms, prefix) })
}

func wordMatches(word string, terms []string, prefix bool) bool {
	for i, term := range terms {
		if word == term || (prefix && i == len(terms)-1 && strings.HasPrefix(word, term)) {
			return true
		}
	}
	return false
}

// firstMatch returns the rune span of the first word of source accepted by
// match, which receives the normalized word.
func firstMatch(source string, match func(word string) bool) (int, int, bool) {
	runes := []rune(source)
	for start := 0; start < len(runes); {
		if !unicode.IsLetter(runes[start]) && !unicode.IsDigit(runes[start]) {
			start++
			continue
		}
		end := start + 1
		for end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end])) {
			end++
		}
		if match(textnorm.Normalize(string(runes[start:end]))) {
			return start, end, true
		}
		start = end
	}
	return 0, 0, false
}

// messageSnippet centers the snippet on a word written as in the query, or,
// for a message found only through stems, on the first word with a query stem.
func messageSnippet(text string, terms, stemTerms []string, prefix bool) string {
	if _, _, found := firstWordMatch(text, terms, prefix); !found {
		if start, _, found := firstStemMatch(text, stemTerms, prefix); found {
			return snippetAt(text, start, true)
		}
	}
	return makeSnippet(text, terms, prefix)
}

func chooseSnippetSource(terms []string, prefix bool, sources ...string) string {
	for _, source := range sources {
		if _, _, found := firstWordMatch(source, terms, prefix); found {
			return source
		}
	}
	for _, source := range sources {
		if source != "" {
			return source
		}
	}
	return ""
}

func makeSnippet(source string, terms []string, prefix bool) string {
	matchStart, _, found := firstWordMatch(source, terms, prefix)
	return snippetAt(source, matchStart, found)
}

// snippetAt cuts a 160-rune window with the match 40 runes from its start.
func snippetAt(source string, matchStart int, found bool) string {
	runes := []rune(source)
	if len(runes) == 0 {
		return ""
	}
	start := 0
	if found && matchStart > 40 {
		start = matchStart - 40
	}
	end := start + 160
	if end > len(runes) {
		end = len(runes)
		if end > 160 && start > end-160 {
			start = end - 160
		}
	}
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}
