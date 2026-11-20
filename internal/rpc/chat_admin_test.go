package rpc

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestChatAdminValidation(t *testing.T) {
	methods := []string{MethodChatJoin, MethodChatLeave, MethodChatCreate, MethodChatEdit, MethodChatKick, MethodChatBan, MethodChatUnban, MethodChatRestrict, MethodChatPermissions, MethodChatSlowMode}
	wantMethods := []string{"chat.join", "chat.leave", "chat.create", "chat.edit", "chat.kick", "chat.ban", "chat.unban", "chat.restrict", "chat.permissions", "chat.slow_mode"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("methods: %v", methods)
	}
	for _, tc := range []struct {
		deny         []string
		clear, valid bool
	}{{nil, true, true}, {[]string{"send-plain"}, false, true}, {nil, false, false}, {[]string{"view-messages"}, false, false}, {[]string{"send-media", "send-media"}, false, false}, {[]string{"send-media"}, true, false}} {
		if (ValidateRights(tc.deny, tc.clear) == nil) != tc.valid {
			t.Errorf("rights %+v", tc)
		}
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d     time.Duration
		valid bool
	}{{29 * time.Second, false}, {30 * time.Second, true}, {366 * 24 * time.Hour, true}, {366*24*time.Hour + time.Second, false}} {
		if (ValidateUntil(now.Add(tc.d).Format(time.RFC3339), now) == nil) != tc.valid {
			t.Errorf("until %v", tc.d)
		}
	}
	for _, tc := range []struct {
		name  string
		err   error
		valid bool
	}{
		{"slow mode 0", ValidateSlowModeSeconds(0), true},
		{"slow mode 10", ValidateSlowModeSeconds(10), true},
		{"slow mode 3600", ValidateSlowModeSeconds(3600), true},
		{"slow mode 11", ValidateSlowModeSeconds(11), false},
		{"slow mode negative", ValidateSlowModeSeconds(-10), false},
		{"title 1", ValidateChatTitle("а"), true},
		{"title 128", ValidateChatTitle(strings.Repeat("а", 128)), true},
		{"title 129", ValidateChatTitle(strings.Repeat("а", 129)), false},
		{"title empty", ValidateChatTitle(""), false},
		{"title bad utf8", ValidateChatTitle("\xff"), false},
		{"about empty", ValidateChatAbout(""), true},
		{"about 255", ValidateChatAbout(strings.Repeat("x", 255)), true},
		{"about 255 runes", ValidateChatAbout(strings.Repeat("я", 255)), true},
		{"about 256", ValidateChatAbout(strings.Repeat("x", 256)), false},
		{"until empty", ValidateUntil("", now), true},
		{"until not RFC 3339", ValidateUntil("завтра", now), false},
	} {
		if (tc.err == nil) != tc.valid {
			t.Errorf("%s: err=%v, valid=%v", tc.name, tc.err, tc.valid)
		}
	}
	if e := ErrAdminDisabled(); e.Code != CodeWriteDisabled || e.Data.(map[string]any)["list"] != "admin" {
		t.Fatal(e)
	}
}
