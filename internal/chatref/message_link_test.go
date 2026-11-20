package chatref

import (
	"strings"
	"testing"
)

func TestParseMessageLinkForms(t *testing.T) {
	cases := []struct {
		input     string
		chat      Ref
		topic, id int
	}{
		{"https://t.me/Telegram/123", Ref{Kind: Username, Username: "telegram"}, 0, 123},
		{"http://t.me/telegram/50?single#ignored", Ref{Kind: Username, Username: "telegram"}, 0, 50},
		{"t.me/telegram/7/9?thread=7", Ref{Kind: Username, Username: "telegram"}, 7, 9},
		{"https://t.me/c/12345/67?comment=4", Ref{Kind: ID, ID: -1000000012345}, 0, 67},
		{"t.me/c/12345/7/9#x", Ref{Kind: ID, ID: -1000000012345}, 7, 9},
		{"t.me/telegram/1/", Ref{Kind: Username, Username: "telegram"}, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseMessageLink(tc.input)
			if err != nil || got.Chat != tc.chat || got.Topic != tc.topic || got.ID != tc.id {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}
func TestParseMessageLinkRejectsGarbage(t *testing.T) {
	cases := []string{"", "telegram/1", "https://evil.test/telegram/1", "https://t.me/telegram", "t.me/telegram/0", "t.me/telegram/-2", "t.me/telegram/nope", "t.me/telegram/1/2/3", "t.me/c/0/1", "t.me/c/nope/1", "t.me/c/1/0", "t.me/c/1/2/3/4", "t.me/share/1", "t.me/addstickers/1", "t.me/joinchat/1", "t.me/+AbCdEfGh1234/1", "t.me/c/9223372036854775807/1", "t.me/abcd/1", "https://user@t.me/telegram/1", "https://user:pass@t.me/telegram/1", "t.me/telegram/+1", "t.me/telegram/7/+9"}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			_, err := ParseMessageLink(input)
			if err == nil || !strings.Contains(err.Error(), "https://t.me/channel/123") {
				t.Fatalf("accepted %q: %v", input, err)
			}
		})
	}
}
func TestFormatMessageLink(t *testing.T) {
	ref := MessageRef{Chat: Ref{Kind: ID, ID: -1000000012345}, ID: 67}
	link, err := FormatMessageLink(ref, "")
	if err != nil || link != "https://t.me/c/12345/67" {
		t.Fatalf("private: %q %v", link, err)
	}
	ref.Topic = 7
	link, err = FormatMessageLink(ref, "NewsFeed")
	if err != nil || link != "https://t.me/newsfeed/7/67" {
		t.Fatalf("public topic: %q %v", link, err)
	}
	for _, bad := range []MessageRef{{Chat: Ref{Kind: ID, ID: 42}, ID: 1}, {Chat: Ref{Kind: ID, ID: -42}, ID: 1}, {Chat: Ref{Kind: ID, ID: -1000000012345}, ID: 0}} {
		if link, err := FormatMessageLink(bad, ""); err == nil {
			t.Errorf("formatted invalid %+v: %s", bad, link)
		}
	}
}

func TestAudit01_FormatMessageLinkEdges(t *testing.T) {
	channel := Ref{Kind: ID, ID: -1000000012345}
	for _, tc := range []struct {
		name     string
		ref      MessageRef
		username string
		want     string // empty means an error is expected
	}{
		{"negative topic", MessageRef{Chat: channel, Topic: -1, ID: 5}, "", ""},
		{"zero message id", MessageRef{Chat: channel, ID: 0}, "", ""},
		{"username taken from the chat", MessageRef{Chat: Ref{Kind: Username, Username: "telegram"}, ID: 5}, "", "https://t.me/telegram/5"},
		{"argument wins over the chat username", MessageRef{Chat: Ref{Kind: Username, Username: "telegram"}, ID: 5}, "newsfeed", "https://t.me/newsfeed/5"},
		{"reserved username", MessageRef{Chat: channel, ID: 5}, "share", ""},
		{"invalid username", MessageRef{Chat: channel, ID: 5}, "bad name", ""},
		{"reserved username in the chat", MessageRef{Chat: Ref{Kind: Username, Username: "addstickers"}, ID: 5}, "", ""},
		{"phone without a name", MessageRef{Chat: Ref{Kind: Phone, Phone: "15551234567"}, ID: 5}, "", ""},
		{"invite without a name", MessageRef{Chat: Ref{Kind: Invite, Invite: "AbCdEfGh1234"}, ID: 5}, "", ""},
		{"phone with a public name", MessageRef{Chat: Ref{Kind: Phone, Phone: "15551234567"}, ID: 5}, "telegram", "https://t.me/telegram/5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := FormatMessageLink(tc.ref, tc.username)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("formatted %+v: %s", tc.ref, link)
				}
				return
			}
			if err != nil || link != tc.want {
				t.Fatalf("got %q, %v; want %q", link, err, tc.want)
			}
		})
	}
}
