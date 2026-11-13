package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

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
	Rank     float64
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
			found, err = db.searchChats(ctx, match, limit, terms, options.Prefix)
		case "people":
			found, err = db.searchPeople(ctx, match, limit, terms, options.Prefix)
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

func (db *DB) searchChats(ctx context.Context, match string, limit int, terms []string, prefix bool) ([]SearchHit, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT c.id,c.title,c.about,bm25(chats_fts) FROM chats_fts JOIN chats c ON c.rowid=chats_fts.rowid WHERE chats_fts MATCH ? ORDER BY bm25(chats_fts),c.id LIMIT ?`, match, limit)
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

func (db *DB) searchPeople(ctx context.Context, match string, limit int, terms []string, prefix bool) ([]SearchHit, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT p.id,p.first_name,p.last_name,p.username,p.bio,bm25(people_fts) FROM people_fts JOIN people p ON p.rowid=people_fts.rowid WHERE people_fts MATCH ? ORDER BY bm25(people_fts),p.id LIMIT ?`, match, limit)
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

func (db *DB) searchMessages(ctx context.Context, match string, limit int, terms []string, options SearchOptions) ([]SearchHit, error) {
	query := `SELECT m.id,m.chat_id,m.text,m.date,m.deleted_at,bm25(messages_fts) FROM messages_fts JOIN messages m ON m.rowid=messages_fts.rowid WHERE messages_fts MATCH ?`
	args := []any{match}
	if options.ChatID != nil {
		query += ` AND m.chat_id=?`
		args = append(args, *options.ChatID)
	}
	if !options.IncludeDeleted {
		query += ` AND m.deleted_at IS NULL`
	}
	query += ` ORDER BY bm25(messages_fts),m.rowid LIMIT ?`
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		var text string
		var deletedAt *int64
		if err := rows.Scan(&hit.ID, &hit.ChatID, &text, &hit.Date, &deletedAt, &hit.Rank); err != nil {
			return nil, err
		}
		hit.Scope = "messages"
		hit.Deleted = deletedAt != nil
		hit.Snippet = makeSnippet(text, terms, options.Prefix)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
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
		word := textnorm.Normalize(string(runes[start:end]))
		for i, term := range terms {
			if word == term || (prefix && i == len(terms)-1 && strings.HasPrefix(word, term)) {
				return start, end, true
			}
		}
		start = end
	}
	return 0, 0, false
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
	runes := []rune(source)
	if len(runes) == 0 {
		return ""
	}
	start := 0
	if matchStart, _, found := firstWordMatch(source, terms, prefix); found && matchStart > 40 {
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
