package daemon

import (
	"context"
	"encoding/json"

	"tg-cli/internal/chatref"
	"tg-cli/internal/config"
)

// newChatMessages returns how many latest messages the Daemon loads for a chat
// it sees for the first time; zero turns the load off.
func (d *daemon) newChatMessages() int {
	n := config.DefaultNewChatMessages
	if value, ok := d.liveSetting("new_chat_sync.messages"); ok {
		var v int
		if json.Unmarshal(value, &v) == nil {
			n = v
		}
	}
	return n
}

// chatIsNew reports whether the database holds nothing of a chat yet: no saved
// message and no load progress. The caller asks before it saves the first
// message of the chat.
func (d *daemon) chatIsNew(ctx context.Context, chatID int64) bool {
	count, err := d.store.CountChatMessages(ctx, chatID)
	if err != nil || count > 0 {
		return false
	}
	_, found, err := d.store.SyncState(ctx, chatID)
	return err == nil && !found
}

// autoSyncNewChat queues the load of the latest messages of a chat that has just
// appeared. It works only while the scheduled history load is enabled (the same
// switch that stops the Daemon's own Telegram requests for history) and
// new_chat_sync.messages is above zero. The job goes through the same queue,
// the same throttle and the same pause between pages as a scheduled load.
func (d *daemon) autoSyncNewChat(chatID int64) {
	if d.background == nil || d.sync == nil || d.getAuthLost() != "" {
		return
	}
	n := d.newChatMessages()
	if n <= 0 || !d.policy().Allowed(chatID) {
		return
	}
	ref := chatref.Ref{Kind: chatref.ID, ID: chatID}
	if d.sync.startAuto(chatID, ref, d.background.pagePause, historyDepth{Messages: n}) {
		d.log.Info("new chat history load queued", "chat", chatID, "messages", n)
	}
}
