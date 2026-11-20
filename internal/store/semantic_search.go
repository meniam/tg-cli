package store

import (
	"context"
	"fmt"
	"strings"

	"tg-cli/internal/stem"
	"tg-cli/internal/textnorm"
)

// WordMatch is a message found by a word leg of the semantic search. The legs
// are returned best first, so the position in the slice is the rank.
type WordMatch struct {
	ChatID int64
	ID     int64
	// TopicID is the forum topic of the message, 0 if it has none.
	TopicID int64
	Date    int64
}

// WordLegOptions selects the messages of the two word legs.
type WordLegOptions struct {
	Query  string
	Prefix bool
	// Limit is the number of messages each leg returns at most.
	Limit      int
	ChatID     *int64
	ChatFilter ChatFilter
}

// WordLegs runs the exact and the stemmed full-text legs over live messages and
// returns the ranked messages of each. The legs apply the same chat filters as
// the ordinary message search, so a closed chat is in neither. The stemmed leg
// is nil if the query has no stems.
func (db *DB) WordLegs(ctx context.Context, o WordLegOptions) (exact, stemmed []WordMatch, err error) {
	match := textnorm.Query(o.Query, o.Prefix)
	if match == "" {
		return nil, nil, ErrEmptyQuery
	}
	options := SearchOptions{Query: o.Query, ChatID: o.ChatID, ChatFilter: o.ChatFilter, Prefix: o.Prefix}
	exactLeg, err := db.messageLeg(ctx, "messages_fts", match, o.Limit, options)
	if err != nil {
		return nil, nil, fmt.Errorf("exact leg: %w", err)
	}
	var stemLeg []messageCandidate
	if stemMatch := stem.Query(o.Query, o.Prefix); stemMatch != "" {
		if stemLeg, err = db.messageLeg(ctx, "messages_stem_fts", stemMatch, o.Limit, options); err != nil {
			return nil, nil, fmt.Errorf("stemmed leg: %w", err)
		}
	}
	return toWordMatches(exactLeg), toWordMatches(stemLeg), nil
}

func toWordMatches(leg []messageCandidate) []WordMatch {
	if len(leg) == 0 {
		return nil
	}
	out := make([]WordMatch, len(leg))
	for i, c := range leg {
		out[i] = WordMatch{ChatID: *c.hit.ChatID, ID: c.hit.ID, TopicID: c.topicID, Date: c.hit.Date}
	}
	return out
}

// FragmentText returns the text of the live messages of one conversation
// fragment: messages of the chat and the forum topic (0 for none) with an id
// from firstID to lastID, not deleted, not service and not empty, in (date, id)
// order, joined by a line feed. The text of a voice message or a round video is
// its caption and finished transcription, as in MessagesForFragments. The text is cut to maxRunes characters, with an
// ellipsis as the last one when it was cut. It is empty if no message is left.
// It does no access check: the caller passes only chats it may show.
func (db *DB) FragmentText(ctx context.Context, chatID, topicID, firstID, lastID int64, maxRunes int) (string, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT m.text,COALESCE(t.text,'') FROM messages m`+transcriptJoin+` WHERE m.chat_id=? AND m.id BETWEEN ? AND ? AND COALESCE(m.topic_id,0)=? AND m.deleted_at IS NULL AND m.service IS NULL AND (m.text<>'' OR t.text IS NOT NULL) ORDER BY m.date,m.id`, chatID, firstID, lastID, topicID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	runes := 0
	for rows.Next() {
		var text, transcript string
		if err := rows.Scan(&text, &transcript); err != nil {
			return "", err
		}
		text = composeMessageText(text, transcript)
		if runes > 0 {
			b.WriteByte('\n')
			runes++
		}
		b.WriteString(text)
		runes += len([]rune(text))
		if maxRunes > 0 && runes > maxRunes {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	out := b.String()
	if maxRunes > 0 && runes > maxRunes {
		out = string([]rune(out)[:maxRunes-1]) + "…"
	}
	return out, nil
}

// ChatLink is what MessageLink needs to know about a chat.
type ChatLink struct {
	Type     string
	Username string
	// Title is the title of the chat, for the answer next to the link.
	Title string
}

// ChatLinks returns the type, the username and the title of each stored chat among
// chatIDs; a chat the database does not know is absent from the map.
func (db *DB) ChatLinks(ctx context.Context, chatIDs []int64) (map[int64]ChatLink, error) {
	out := make(map[int64]ChatLink, len(chatIDs))
	if len(chatIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(chatIDs))
	for i, id := range chatIDs {
		args[i] = id
	}
	rows, err := db.read.QueryContext(ctx, `SELECT id,type,username,title FROM chats WHERE id IN (`+placeholders(len(chatIDs))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var link ChatLink
		var username, title *string
		if err := rows.Scan(&id, &link.Type, &username, &title); err != nil {
			return nil, err
		}
		if username != nil {
			link.Username = *username
		}
		if title != nil {
			link.Title = *title
		}
		out[id] = link
	}
	return out, rows.Err()
}
