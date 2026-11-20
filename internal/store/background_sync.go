package store

import (
	"context"
	"fmt"
)

// Groups of ChatsForBackgroundSync, in the order the scheduler takes them.
const (
	// BackgroundGroupPeople is a chat of type user.
	BackgroundGroupPeople = 0
	// BackgroundGroupFolders is a chat the owner pinned or included in a folder.
	BackgroundGroupFolders = 1
	// BackgroundGroupOther is every other chat, bots included.
	BackgroundGroupOther = 2
)

// backgroundGroupSQL is the group of the chat c: the one definition behind both
// the scheduled pick and the history counts of the dashboard, so the two never
// disagree about which group a chat belongs to.
const backgroundGroupSQL = `CASE WHEN c.type = 'user' THEN 0
	            WHEN EXISTS (SELECT 1 FROM folder_chats f WHERE f.chat_id = c.id AND f.kind IN ('pinned','include')) THEN 1
	            ELSE 2 END`

// BackgroundSyncChat is one chat the scheduled history load may take.
type BackgroundSyncChat struct {
	ID    int64
	Type  string
	Title string
	// Group is BackgroundGroupPeople, BackgroundGroupFolders or BackgroundGroupOther.
	Group int
}

// ChatsForBackgroundSync returns up to limit chats whose history is not loaded to
// the start yet: a chat without a sync_state row, or with complete = 0. The access
// filter applies inside the query, so a closed chat is never returned and never
// requested from Telegram. exclude drops chats, for example the ones that failed
// today; it does not make a chat a folder chat.
//
// Chats set to skip are omitted. Chats set to first precede the other chats.
// Within each priority, chats come group by group: people (type user), then chats with a pinned or an
// included folder entry, then the rest. Inside a group the chat with the most
// recent stored message comes first, a chat without messages comes last, and equal
// dates fall back to the chat id. A limit below 1 returns no chats.
func (db *DB) ChatsForBackgroundSync(ctx context.Context, filter ChatFilter, exclude []int64, limit int) ([]BackgroundSyncChat, error) {
	out := []BackgroundSyncChat{}
	if limit < 1 {
		return out, nil
	}
	cond, args := filter.condition("c.id", false)
	if len(exclude) > 0 {
		cond += " AND c.id NOT IN (" + placeholders(len(exclude)) + ")"
		for _, id := range exclude {
			args = append(args, id)
		}
	}
	args = append(args, limit)
	// In SQLite NULL sorts first, so DESC puts the chats without messages last.
	rows, err := db.read.QueryContext(ctx, `SELECT c.id, c.type, c.title,
	       `+backgroundGroupSQL+` AS grp,
	       (SELECT MAX(m.date) FROM messages m WHERE m.chat_id = c.id) AS last_date
	FROM chats c
	LEFT JOIN sync_state s ON s.chat_id = c.id
 LEFT JOIN account_state cs ON cs.key = '`+chatHistoryModePrefix+`' || c.id
	WHERE (s.chat_id IS NULL OR s.complete = 0) AND COALESCE(json_extract(cs.value, '$'), 'auto') <> 'skip'`+cond+`
	ORDER BY (json_extract(cs.value, '$') IS 'first') DESC, grp, last_date DESC, c.id
	LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("read chats for the background sync: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var chat BackgroundSyncChat
		var lastDate *int64
		if err := rows.Scan(&chat.ID, &chat.Type, &chat.Title, &chat.Group, &lastDate); err != nil {
			return nil, fmt.Errorf("read chats for the background sync: %w", err)
		}
		out = append(out, chat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read chats for the background sync: %w", err)
	}
	return out, nil
}
