package cli

import (
	"context"
	"github.com/spf13/cobra"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func (*fakeTelegram) UnreadChats(context.Context, int, bool, func(int64) bool) (rpc.UnreadChatsResult, []rpc.ChatState, error) {
	return rpc.UnreadChatsResult{Chats: []rpc.UnreadChat{}}, nil, nil
}
func (*fakeTelegram) UnreadMessages(context.Context, chatref.Ref, rpc.MessageUnreadParams) (rpc.UnreadMessagesResult, *rpc.ChatState, error) {
	return rpc.UnreadMessagesResult{Messages: []rpc.Message{}}, nil, nil
}

func TestUnreadSchemaUntrusted(t *testing.T) {
	a, root := annotatedApp()
	found := map[string]bool{}
	walkCommands(root, func(cmd *cobra.Command, path string) {
		if path != "chat unread" && path != "chat changes" && path != "message unread" {
			return
		}
		found[path] = true
		fields := schema.Fields(a.specs[cmd].Result)
		want := map[string]bool{}
		switch path {
		case "chat unread":
			want["chats.title"] = true
			want["chats.username"] = true
		case "chat changes":
			want["current.title"] = true
			want["versions.title"] = true
		case "message unread":
			want["messages.text"] = true
		}
		for _, f := range fields {
			if _, ok := want[f.Path]; ok && f.Untrusted {
				delete(want, f.Path)
			}
		}
		if len(want) > 0 {
			t.Errorf("%s missing untrusted fields %v", path, want)
		}
	})
	if len(found) != 3 {
		t.Fatal(found)
	}
}
