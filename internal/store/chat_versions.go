package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"tg-cli/internal/rpc"
)

// ChatVersion is a stored snapshot of a chat before a significant change.
type ChatVersion struct {
	ChatID                       int64
	Version                      int
	Type, Title, Username, About *string
	Settings, Raw                json.RawMessage
	ObservedAt                   int64
}

// GetChatVersion reads a stored snapshot, including its private raw data.
func (db *DB) GetChatVersion(ctx context.Context, chatID int64, version int) (ChatVersion, error) {
	var v ChatVersion
	var settings, raw sql.NullString
	err := db.read.QueryRowContext(ctx, `SELECT chat_id,version,type,title,username,about,settings,raw,observed_at FROM chat_versions WHERE chat_id=? AND version=?`, chatID, version).Scan(&v.ChatID, &v.Version, &v.Type, &v.Title, &v.Username, &v.About, &settings, &raw, &v.ObservedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatVersion{}, ErrNotFound
	}
	if err != nil {
		return ChatVersion{}, fmt.Errorf("get chat version: %w", err)
	}
	if settings.Valid {
		v.Settings = json.RawMessage(settings.String)
	}
	if raw.Valid {
		v.Raw = json.RawMessage(raw.String)
	}
	return v, nil
}

func (db *DB) ChatVersions(ctx context.Context, id int64, limit int, filter ChatFilter) (rpc.ChatChangesResult, error) {
	out := rpc.ChatChangesResult{ChatID: id, Versions: []rpc.ChatVersion{}}
	if !allowedID(filter, id) {
		return out, ErrNotFound
	}
	chat, err := db.GetChat(ctx, id)
	if err != nil {
		return out, err
	}
	out.Current = rpc.ChatSnapshot{Type: chat.Type, Title: chat.Title, Username: chat.Username, About: chat.About, Settings: chat.Settings}
	rows, err := db.read.QueryContext(ctx, `SELECT version,type,title,username,about,settings,observed_at FROM chat_versions WHERE chat_id=? ORDER BY version DESC LIMIT ?`, id, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v rpc.ChatVersion
		var typ, title, username, about, settings sql.NullString
		if err = rows.Scan(&v.Version, &typ, &title, &username, &about, &settings, &v.ObservedAt); err != nil {
			return out, err
		}
		v.Type = typ.String
		v.Title = title.String
		if username.Valid {
			v.Username = &username.String
		}
		if about.Valid {
			v.About = &about.String
		}
		if settings.Valid {
			v.Settings = json.RawMessage(settings.String)
		}
		out.Versions = append(out.Versions, v)
	}
	if err = rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	return out, nil
}
