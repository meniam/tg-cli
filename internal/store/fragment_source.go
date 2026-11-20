package store

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// FragmentMessage is a message as the semantic indexer reads it: the fields the
// chunker needs plus the text that is embedded.
type FragmentMessage struct {
	ChatID int64
	ID     int64
	// Date is Unix seconds.
	Date int64
	// TopicID is the forum topic of the message, 0 when there is none.
	TopicID int64
	Text    string
	// ReplyTo is the id of the message answered in the same chat, 0 when none.
	ReplyTo int64
	// Chars is the length of Text in characters (runes), not bytes: the chunker
	// thresholds count characters, and Cyrillic takes two bytes each.
	Chars int
}

// MessagesForFragments returns the messages that go into the semantic index:
// not deleted, not service, with text. The text of a voice message or a round
// video is its caption plus its finished transcription (see transcriptJoin); a
// voice message without one has no text and is not returned. The access filter applies inside the
// query, so a closed chat never reaches the indexer. The order is (chat_id,
// date, id), the order the chunker needs. The scan walks the chat_id prefix of
// messages_chat_date_idx and sorts only the ids inside equal dates.
func (db *DB) MessagesForFragments(ctx context.Context, filter ChatFilter) ([]FragmentMessage, error) {
	cond, args := filter.condition("m.chat_id", false)
	rows, err := db.read.QueryContext(ctx, `SELECT m.chat_id, m.id, m.date, m.topic_id, m.text, COALESCE(t.text, ''), m.reply_to
FROM messages m`+transcriptJoin+`
WHERE m.deleted_at IS NULL AND m.service IS NULL AND (m.text <> '' OR t.text IS NOT NULL)`+cond+`
ORDER BY m.chat_id, m.date, m.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("read messages for fragments: %w", err)
	}
	defer rows.Close()
	var out []FragmentMessage
	for rows.Next() {
		var m FragmentMessage
		var topic, reply *int64
		var transcript string
		if err := rows.Scan(&m.ChatID, &m.ID, &m.Date, &topic, &m.Text, &transcript, &reply); err != nil {
			return nil, fmt.Errorf("read messages for fragments: %w", err)
		}
		m.Text = composeMessageText(m.Text, transcript)
		if topic != nil {
			m.TopicID = *topic
		}
		if reply != nil {
			m.ReplyTo = *reply
		}
		m.Chars = utf8.RuneCountInString(m.Text)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages for fragments: %w", err)
	}
	return out, nil
}
