package rpc

import (
	"encoding/json"
	"testing"
)

// The wire form of a search answer: a message hit and a fragment always carry
// the chat name, with an explicit null for a chat without a username, and the
// other scopes carry none of these fields.
func TestSearchAnswerJSON(t *testing.T) {
	chat := int64(-1001)
	link, username := "https://t.me/pub/7", "pub"
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"message hit with a username", SearchHit{Scope: "messages", ID: 7, ChatID: &chat, Snippet: "s", Rank: -0.5, Link: &link, ChatTitle: "Public", ChatUsername: &username},
			`{"scope":"messages","id":7,"chat_id":-1001,"snippet":"s","rank":-0.5,"link":"https://t.me/pub/7","chat_title":"Public","chat_username":"pub"}`},
		{"message hit without a username or a link", SearchHit{Scope: "messages", ID: 3, ChatID: &chat, Snippet: "s", Rank: -0.5, LinkUnavailable: "private_chat", ChatTitle: "Alice"},
			`{"scope":"messages","id":3,"chat_id":-1001,"snippet":"s","rank":-0.5,"link_unavailable":"private_chat","link":null,"chat_title":"Alice","chat_username":null}`},
		{"message hit of an unknown chat", SearchHit{Scope: "messages", ID: 4, ChatID: &chat, Snippet: "s", Rank: -0.5},
			`{"scope":"messages","id":4,"chat_id":-1001,"snippet":"s","rank":-0.5,"link":null,"chat_title":"","chat_username":null}`},
		{"chat hit", SearchHit{Scope: "chats", ID: -1001, Title: "Public", Snippet: "s", Rank: -1},
			`{"scope":"chats","id":-1001,"title":"Public","snippet":"s","rank":-1}`},
		{"fragment", SemanticFragment{ChatID: -1001, FirstID: 1, LastID: 2, StartsAt: "a", EndsAt: "b", Score: 0.5, Sources: []string{"vector"}, Text: "t", ChatTitle: "Public", ChatUsername: &username},
			`{"chat_id":-1001,"topic_id":0,"first_id":1,"last_id":2,"starts_at":"a","ends_at":"b","score":0.5,"sources":["vector"],"indexed":false,"text":"t","link":null,"match_link":null,"chat_title":"Public","chat_username":"pub"}`},
		{"fragment without a username", SemanticFragment{ChatID: 55, FirstID: 1, LastID: 1, StartsAt: "a", EndsAt: "a", Sources: []string{"stem"}, Text: "t", ChatTitle: "Alice", LinkUnavailable: "private_chat"},
			`{"chat_id":55,"topic_id":0,"first_id":1,"last_id":1,"starts_at":"a","ends_at":"a","score":0,"sources":["stem"],"indexed":false,"text":"t","link":null,"match_link":null,"link_unavailable":"private_chat","chat_title":"Alice","chat_username":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
