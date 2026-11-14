package rpc

import "testing"

func TestOwnerStateValidation(t *testing.T) {
	for _, p := range []BlockedParams{{Limit: 1001}, {Limit: -1}, {Cursor: "bad"}, {Cursor: "01"}, {Local: true, Cursor: "1"}} {
		if _, err := BlockedOffset(p); err == nil {
			t.Errorf("accepted %+v", p)
		}
	}
	if n, err := BlockedOffset(BlockedParams{Cursor: "7"}); err != nil || n != 7 {
		t.Fatalf("offset %d %v", n, err)
	}
	for _, p := range []ChatStateParams{{Limit: 501}, {Limit: -1}, {Chat: "x", Limit: 1}} {
		if ValidateChatStateParams(p) == nil {
			t.Errorf("accepted %+v", p)
		}
	}
}
