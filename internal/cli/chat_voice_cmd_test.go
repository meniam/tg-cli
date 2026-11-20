package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// TestVoiceSourceChatVoiceOn: the flags reach the Daemon, the answer carries the
// counts, and a mark made while the setting is off warns on stderr.
func TestVoiceSourceChatVoiceOn(t *testing.T) {
	r := runVoiceCLI(t, []string{"chat", "voice", "on", "@alice", "--video", "--backfill"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.ChatVoiceOnResult{ChatID: 5, Video: true, EnabledAt: 100, Enabled: true, Candidates: 12, Queued: 10}, nil
	})
	var p rpc.ChatVoiceOnParams
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || r.method != rpc.MethodChatVoiceOn || p != (rpc.ChatVoiceOnParams{Chat: "@alice", Video: true, Backfill: true}) {
		t.Fatalf("on: %+v %+v %v", r, p, err)
	}
	if !strings.Contains(r.stdout, `"queued":10`) || !strings.Contains(r.stdout, `"candidates":12`) || r.stderr != "" {
		t.Fatalf("on output: %+v", r)
	}
	// A negative id goes after --; the flags default to off.
	r = runVoiceCLI(t, []string{"chat", "voice", "on", "--", "-1001234567890"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.ChatVoiceOnResult{ChatID: -1001234567890, EnabledAt: 100}, nil
	})
	p = rpc.ChatVoiceOnParams{}
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || p != (rpc.ChatVoiceOnParams{Chat: "-1001234567890"}) {
		t.Fatalf("on with an id: %+v %+v %v", r, p, err)
	}
	if !strings.Contains(r.stderr, "enabled = true") {
		t.Fatalf("no warning while the setting is off: %q", r.stderr)
	}
	// --backfill with the setting off is refused by the Daemon with code 2.
	r = runVoiceCLI(t, []string{"chat", "voice", "on", "@alice", "--backfill"}, nil, func(string, json.RawMessage) (any, error) {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "the chat is marked, but voice transcription is off", Data: map[string]any{"reason": "disabled"}}
	})
	if r.code != exitcode.Usage || !strings.Contains(r.stdout, `"reason":"disabled"`) {
		t.Fatalf("backfill while off: %+v", r)
	}
	for _, args := range [][]string{{"chat", "voice", "on"}, {"chat", "voice", "on", "a", "b"}} {
		if r = runVoiceCLI(t, args, nil, nil); r.code != exitcode.Usage || r.method != "" {
			t.Fatalf("%v: %+v", args, r)
		}
	}
}

func TestVoiceSourceChatVoiceOffAndList(t *testing.T) {
	r := runVoiceCLI(t, []string{"chat", "voice", "off", "@alice"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.ChatVoiceOffResult{ChatID: 5, Removed: true}, nil
	})
	var p rpc.ChatVoiceOffParams
	if err := json.Unmarshal(r.params, &p); err != nil || r.code != 0 || r.method != rpc.MethodChatVoiceOff || p.Chat != "@alice" || !strings.Contains(r.stdout, `"removed":true`) {
		t.Fatalf("off: %+v %+v %v", r, p, err)
	}
	r = runVoiceCLI(t, []string{"chat", "voice", "list"}, nil, func(string, json.RawMessage) (any, error) {
		return rpc.ChatVoiceListResult{Enabled: true, Chats: []rpc.ChatVoiceEntry{{ChatID: 5, Title: "Team", Video: true, EnabledAt: 100}}}, nil
	})
	if r.code != 0 || r.method != rpc.MethodChatVoiceList || !strings.Contains(r.stdout, `"chat_id":5`) || !strings.Contains(r.stdout, `"video":true`) {
		t.Fatalf("list: %+v", r)
	}
	// A closed chat answers like an unknown one.
	r = runVoiceCLI(t, []string{"chat", "voice", "off", "@closed"}, nil, func(string, json.RawMessage) (any, error) {
		return nil, rpc.ErrChatNotFound()
	})
	if r.code != exitcode.NotFound {
		t.Fatalf("closed chat: %+v", r)
	}
}
