package chatref

import "testing"

func TestParseChatlist(t *testing.T) {
	for _, s := range []string{"t.me/addlist/AbCdEfGh", "https://t.me/ADDLIST/AbCdEfGh?x=1"} {
		if got, err := ParseChatlist(s); err != nil || got != "AbCdEfGh" {
			t.Fatalf("%q: %q %v", s, got, err)
		}
	}
	for _, s := range []string{"t.me/addlist/", "t.me/addlist/short", "t.me/addlist/AbCdEfGh/extra", "example.com/addlist/AbCdEfGh"} {
		if _, err := ParseChatlist(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
