package daemon

import (
	"context"
	"encoding/json"

	"tg-cli/internal/rpc"
)

// chatOverview answers from the local database only: it never calls Telegram, so
// it works while the account is offline. The access filter is applied inside the
// query, before the limit, so a closed chat never reaches the answer.
func (d *daemon) chatOverview(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatOverviewParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Type != "" && !chatTypes[p.Type] {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "type must be one of user, bot, group, supergroup, channel")
	}
	limit, err := limitOrDefault(p.Limit, defaultChatLimit, maxChatLimit)
	if err != nil {
		return nil, err
	}
	rows, err := d.store.ChatsOverview(ctx, chatFilter(d.policy()), limit, p.Type)
	if err != nil {
		return nil, err
	}
	out := rpc.ChatOverviewResult{Chats: make([]rpc.ChatOverview, 0, len(rows))}
	for _, row := range rows {
		// The store row and the wire type carry the same fields; the copy keeps
		// the local shape out of the RPC contract.
		item := rpc.ChatOverview{
			Contact: row.Contact, ChatID: row.ChatID, Type: row.Type, Title: row.Title, Username: row.Username,
			CreatedAt: row.CreatedAt, Muted: row.Muted, MuteUntil: row.MuteUntil,
			Archived: row.Archived, Pinned: row.Pinned, Unread: row.Unread,
			UnreadMentions: row.UnreadMentions, UnreadMark: row.UnreadMark,
			StateSyncedAt: row.StateSyncedAt, Messages: row.Messages, MyMessages: row.MyMessages,
			HistoryComplete: row.HistoryComplete, HistoryFirstMessageDate: row.HistoryFirstMessageDate,
			HistoryGapFrom: row.HistoryGapFrom, HistoryGapTo: row.HistoryGapTo,
		}
		if row.FirstMessage != nil {
			item.FirstMessage = &rpc.ChatMessagePreview{ID: row.FirstMessage.ID, Date: row.FirstMessage.Date, Text: row.FirstMessage.Text}
		}
		if row.LastMessage != nil {
			item.LastMessage = &rpc.ChatMessagePreview{ID: row.LastMessage.ID, Date: row.LastMessage.Date, Text: row.LastMessage.Text}
		}
		out.Chats = append(out.Chats, item)
	}
	return out, nil
}
