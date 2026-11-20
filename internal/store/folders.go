package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"tg-cli/internal/rpc"
)

func allowedID(f ChatFilter, id int64) bool {
	if f.Hide {
		return false
	}
	if len(f.Allow) > 0 {
		found := false
		for _, v := range f.Allow {
			if v == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, v := range f.Block {
		if v == id {
			return false
		}
	}
	return true
}
func visibleFolder(f rpc.Folder, filter ChatFilter) rpc.Folder {
	selectPeers := func(peers []rpc.FolderPeer) []rpc.FolderPeer {
		out := make([]rpc.FolderPeer, 0, len(peers))
		for _, p := range peers {
			if allowedID(filter, p.ID) {
				out = append(out, p)
			}
		}
		return out
	}
	f.Pinned = selectPeers(f.Pinned)
	f.Include = selectPeers(f.Include)
	f.Exclude = selectPeers(f.Exclude)
	return f
}

// ReplaceFolders atomically replaces the folder snapshot with sanitized values.
func (db *DB) ReplaceFolders(ctx context.Context, folders []rpc.Folder, filter ChatFilter) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM folder_chats`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM folders`); err != nil {
		return err
	}
	now := db.now().Unix()
	for _, f := range folders {
		f = visibleFolder(f, filter)
		f.SyncedAt = &now
		raw, e := json.Marshal(f)
		if e != nil {
			return e
		}
		var rules any
		if f.Rules != nil {
			b, e := json.Marshal(f.Rules)
			if e != nil {
				return e
			}
			rules = string(b)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO folders(id,title,data,first_seen_at,last_seen_at,kind,position,emoticon,color,rules,synced_at,raw) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, f.ID, f.Title, string(raw), now, now, f.Kind, f.Position, f.Emoticon, f.Color, rules, now, ownerRaw(f.Raw))
		if err != nil {
			return fmt.Errorf("save folder: %w", err)
		}
		for _, group := range []struct {
			kind  string
			peers []rpc.FolderPeer
		}{{"pinned", f.Pinned}, {"include", f.Include}, {"exclude", f.Exclude}} {
			for pos, p := range group.peers {
				if _, err = tx.ExecContext(ctx, `INSERT INTO folder_chats(folder_id,chat_id,position,kind) VALUES(?,?,?,?) ON CONFLICT(folder_id,chat_id) DO NOTHING`, f.ID, p.ID, pos, group.kind); err != nil {
					return err
				}
			}
		}
	}
	if err = saveSnapshot(ctx, tx, "folders", now, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// FoldersSyncedAt returns the unix time of the folder snapshot, or nil when the
// folders were never read from Telegram.
func (db *DB) FoldersSyncedAt(ctx context.Context) (*int64, error) {
	at, _, err := db.snapshot(ctx, "folders")
	return at, err
}

func (db *DB) Folders(ctx context.Context, filter ChatFilter) (rpc.FoldersResult, error) {
	out := rpc.FoldersResult{Folders: []rpc.Folder{}}
	var err error
	out.SyncedAt, _, err = db.snapshot(ctx, "folders")
	if err != nil {
		return out, err
	}
	rows, err := db.read.QueryContext(ctx, `SELECT data FROM folders ORDER BY position,id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		var f rpc.Folder
		if err = json.Unmarshal([]byte(raw), &f); err != nil {
			return out, err
		}
		f = visibleFolder(f, filter)
		for _, peers := range [][]rpc.FolderPeer{f.Pinned, f.Include, f.Exclude} {
			for i := range peers {
				peers[i].Title = ""
				err := db.read.QueryRowContext(ctx, `SELECT title FROM chats WHERE id=?`, peers[i].ID).Scan(&peers[i].Title)
				if err != nil && err != sql.ErrNoRows {
					return out, err
				}
			}
		}
		out.Folders = append(out.Folders, f)
	}
	return out, rows.Err()
}
