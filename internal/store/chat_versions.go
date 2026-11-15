package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"tg-cli/internal/rpc"
)

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
