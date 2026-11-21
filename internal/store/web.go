package store

import (
	"context"
	"encoding/json"
	"strconv"
	"tg-cli/internal/rpc"
)

// WebHistory returns the newest nondeleted messages before the exclusive cursor.
func (db *DB) WebHistory(ctx context.Context, chatID int64, before, topic, limit int) ([]Message, error) {
	query := `SELECT ` + messageColumns + ` FROM messages WHERE chat_id=? AND deleted_at IS NULL`
	args := []any{chatID}
	if before > 0 {
		query += ` AND id<?`
		args = append(args, before)
	}
	if topic > 0 {
		query += ` AND topic_id=?`
		args = append(args, topic)
	}
	rows, err := db.read.QueryContext(ctx, query+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (db *DB) WebPinned(ctx context.Context, chatID int64) ([]Message, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages
		WHERE chat_id=? AND deleted_at IS NULL AND pinned=1 ORDER BY id DESC LIMIT 10`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// WebTopics lists topics represented in the local history and their known titles.
func (db *DB) WebTopics(ctx context.Context, chatID int64) ([]rpc.WebTopic, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT m.topic_id,
		COALESCE((SELECT json_extract(n.raw,'$.action.title') FROM messages n
			WHERE n.chat_id=m.chat_id AND n.deleted_at IS NULL AND (n.topic_id=m.topic_id OR n.id=m.topic_id)
			AND json_extract(n.raw,'$.action._') IN ('messageActionTopicEdit','messageActionTopicCreate')
			AND json_type(n.raw,'$.action.title')='text' ORDER BY n.id DESC LIMIT 1), '')
		FROM messages m WHERE m.chat_id=? AND m.deleted_at IS NULL AND m.topic_id>0
		GROUP BY m.topic_id ORDER BY max(m.id) DESC LIMIT 100`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var topics []rpc.WebTopic
	for rows.Next() {
		var topic rpc.WebTopic
		if err := rows.Scan(&topic.ID, &topic.Title); err != nil {
			return nil, err
		}
		if topic.Title == "" {
			topic.Title = "Topic " + strconv.Itoa(topic.ID)
		}
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

const chatHistoryModePrefix = "chat.history_mode."

// HistoryMode returns the local scheduling preference for a stored chat.
func (db *DB) HistoryMode(ctx context.Context, chatID int64) (string, error) {
	raw, found, err := db.GetAccountState(ctx, chatHistoryModePrefix+strconv.FormatInt(chatID, 10))
	if err != nil || !found {
		return "auto", err
	}
	var mode string
	err = json.Unmarshal(raw, &mode)
	return mode, err
}

func (db *DB) SetHistoryMode(ctx context.Context, chatID int64, mode string) error {
	value, err := json.Marshal(mode)
	if err != nil {
		return err
	}
	return db.SetAccountState(ctx, chatHistoryModePrefix+strconv.FormatInt(chatID, 10), value)
}
