package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func syncDepthReply(string, json.RawMessage) (any, error) {
	return syncCLIJob(rpc.HistorySyncQueued), nil
}

func TestChatSyncStartDepthFlags(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name         string
		args         []string
		wantMessages int
		sinceWithin  [2]time.Duration // the expected age of since: [min, max]
	}{
		{"messages", []string{"--messages", "100"}, 100, [2]time.Duration{}},
		{"days", []string{"--days", "30"}, 0, [2]time.Duration{30*24*time.Hour - time.Minute, 30*24*time.Hour + time.Minute}},
		{"months", []string{"--months", "1"}, 0, [2]time.Duration{27 * 24 * time.Hour, 32 * 24 * time.Hour}},
		{"since date", []string{"--since", "2026-01-15"}, 0, [2]time.Duration{time.Minute, 10 * 365 * 24 * time.Hour}},
		{"since rfc3339", []string{"--since", "2026-01-15T10:00:00Z"}, 0, [2]time.Duration{time.Minute, 10 * 365 * 24 * time.Hour}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runSyncCLI(t, append([]string{"chat", "sync", "start", "42"}, tc.args...), syncDepthReply)
			if run.code != exitcode.OK || run.method != rpc.MethodHistorySyncStart {
				t.Fatalf("run=%+v", run)
			}
			var p rpc.HistorySyncParams
			if err := json.Unmarshal(run.params, &p); err != nil {
				t.Fatal(err)
			}
			if p.Chat != "42" || p.Messages != tc.wantMessages {
				t.Fatalf("params=%+v", p)
			}
			if tc.sinceWithin == ([2]time.Duration{}) {
				if p.Since != "" {
					t.Fatalf("since must stay empty: %+v", p)
				}
				return
			}
			since, err := time.Parse(time.RFC3339, p.Since)
			if err != nil {
				t.Fatalf("since %q: %v", p.Since, err)
			}
			if age := now.Sub(since); age < tc.sinceWithin[0] || age > tc.sinceWithin[1] {
				t.Fatalf("since %s is %s old, want within %v", p.Since, age, tc.sinceWithin)
			}
		})
	}
	plain := runSyncCLI(t, []string{"chat", "sync", "start", "42"}, syncDepthReply)
	var p rpc.HistorySyncParams
	if err := json.Unmarshal(plain.params, &p); err != nil || p.Messages != 0 || p.Since != "" {
		t.Fatalf("a start without a depth flag must carry none: %s %v", plain.params, err)
	}
}

func TestChatSyncDepthFlagsAreChecked(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	for name, args := range map[string][]string{
		"two flags":      {"--messages", "10", "--days", "5"},
		"days and since": {"--days", "5", "--since", "2026-01-01"},
		"zero messages":  {"--messages", "0"},
		"too many":       {"--messages", "100001"},
		"negative days":  {"--days", "-3"},
		"zero months":    {"--months", "0"},
		"bad date":       {"--since", "yesterday"},
		"future date":    {"--since", future},
	} {
		t.Run(name, func(t *testing.T) {
			for _, command := range [][]string{{"chat", "sync", "start", "42"}, {"chat", "sync", "queue"}} {
				run := runSyncCLI(t, append(command, args...), syncDepthReply)
				if run.code != exitcode.Usage || run.method != "" {
					t.Errorf("%v %v: %+v", command, args, run)
				}
			}
		})
	}
}

func TestChatSyncQueueRequestAndOutput(t *testing.T) {
	reply := func(string, json.RawMessage) (any, error) {
		return rpc.HistorySyncQueueResult{
			DryRun: true, WouldQueue: 2, SkippedLoaded: 1,
			Chats: []rpc.HistorySyncQueueChat{
				{ChatID: 11, Title: "Anna", LastMessageDate: 1700000000, Action: rpc.HistorySyncActionWouldQueue},
				{ChatID: 12, Title: "Boris", LastMessageDate: 1699990000, Action: rpc.HistorySyncActionWouldQueue},
			},
		}, nil
	}
	run := runSyncCLI(t, []string{"chat", "sync", "queue", "--type", "user", "--active-days", "60", "--unsynced", "--max-chats", "50", "--messages", "100", "--dry-run"}, reply)
	if run.code != exitcode.OK || run.method != rpc.MethodHistorySyncQueue {
		t.Fatalf("run=%+v", run)
	}
	var p rpc.HistorySyncQueueParams
	if err := json.Unmarshal(run.params, &p); err != nil {
		t.Fatal(err)
	}
	want := rpc.HistorySyncQueueParams{Type: "user", ActiveDays: 60, Unsynced: true, MaxChats: 50, Messages: 100, DryRun: true}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("params=%+v, want %+v", p, want)
	}
	if !strings.Contains(run.stdout, `"would_queue":2`) || !strings.Contains(run.stdout, `"title":"Anna"`) {
		t.Fatalf("stdout=%s", run.stdout)
	}
	jsonl := runSyncCLI(t, []string{"chat", "sync", "queue", "--format", "jsonl", "--fields", "chat_id,action"}, reply)
	if jsonl.code != exitcode.OK || jsonl.stdout != "{\"action\":\"would_queue\",\"chat_id\":11}\n{\"action\":\"would_queue\",\"chat_id\":12}\n" && jsonl.stdout != "{\"chat_id\":11,\"action\":\"would_queue\"}\n{\"chat_id\":12,\"action\":\"would_queue\"}\n" {
		t.Fatalf("JSONL queue: %+v", jsonl)
	}
}

func TestChatSyncQueueFlagsAreChecked(t *testing.T) {
	for name, args := range map[string][]string{
		"max chats zero":      {"--max-chats", "0"},
		"max chats too many":  {"--max-chats", "501"},
		"active days zero":    {"--active-days", "0"},
		"active days too big": {"--active-days", "40000"},
	} {
		t.Run(name, func(t *testing.T) {
			run := runSyncCLI(t, append([]string{"chat", "sync", "queue"}, args...), syncDepthReply)
			if run.code != exitcode.Usage || run.method != "" {
				t.Fatalf("%v: %+v", args, run)
			}
		})
	}
	bare := runSyncCLI(t, []string{"chat", "sync", "queue"}, func(string, json.RawMessage) (any, error) {
		return rpc.HistorySyncQueueResult{Chats: []rpc.HistorySyncQueueChat{}}, nil
	})
	var p rpc.HistorySyncQueueParams
	if bare.code != exitcode.OK || json.Unmarshal(bare.params, &p) != nil || !reflect.DeepEqual(p, rpc.HistorySyncQueueParams{}) {
		t.Fatalf("a queue without flags must send no filters: %+v params=%s", bare, bare.params)
	}
}

func TestChatSyncCancelAll(t *testing.T) {
	run := runSyncCLI(t, []string{"chat", "sync", "cancel", "--all"}, func(string, json.RawMessage) (any, error) {
		return rpc.HistorySyncCancelAllResult{Cancelled: 5}, nil
	})
	if run.code != exitcode.OK || run.method != rpc.MethodHistorySyncCancelAll || !strings.Contains(run.stdout, `"cancelled":5`) {
		t.Fatalf("cancel all: %+v", run)
	}
	for name, args := range map[string][]string{
		"nothing":      {"chat", "sync", "cancel"},
		"chat and all": {"chat", "sync", "cancel", "42", "--all"},
	} {
		t.Run(name, func(t *testing.T) {
			bad := runSyncCLI(t, args, syncDepthReply)
			if bad.code != exitcode.Usage || bad.method != "" {
				t.Fatalf("%v: %+v", args, bad)
			}
		})
	}
}

func TestChatSyncQueueTakesChatsAsAList(t *testing.T) {
	reply := func(string, json.RawMessage) (any, error) {
		return rpc.HistorySyncQueueResult{Queued: 2, SkippedLoaded: 1, Chats: []rpc.HistorySyncQueueChat{
			{ChatID: 11, Title: "Anna", Action: rpc.HistorySyncActionQueued},
			{ChatID: 12, Title: "Boris", Action: rpc.HistorySyncActionQueued},
			{ChatID: -100, Title: "Team", Action: rpc.HistorySyncActionSkippedLoaded},
		}}, nil
	}
	run := runSyncCLI(t, []string{"chat", "sync", "queue", "--months", "1", "@anna", "12", "--", "-100"}, reply)
	if run.code != exitcode.OK || run.method != rpc.MethodHistorySyncQueue {
		t.Fatalf("run=%+v", run)
	}
	var p rpc.HistorySyncQueueParams
	if err := json.Unmarshal(run.params, &p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Chats, []string{"@anna", "12", "-100"}) || p.Since == "" || p.Type != "" || p.MaxChats != 0 {
		t.Fatalf("params=%+v", p)
	}
	if !strings.Contains(run.stdout, `"action":"skipped_loaded"`) {
		t.Fatalf("stdout=%s", run.stdout)
	}
	// The filters pick chats from the dialog list; they do not go with named chats.
	for _, flag := range [][]string{{"--type", "user"}, {"--active-days", "5"}, {"--unsynced"}, {"--max-chats", "5"}} {
		run = runSyncCLI(t, append([]string{"chat", "sync", "queue", "@anna"}, flag...), reply)
		if run.code != exitcode.Usage || run.method != "" {
			t.Fatalf("%v with a chat: %+v", flag, run)
		}
	}
	many := []string{"chat", "sync", "queue"}
	for i := 0; i < 101; i++ {
		many = append(many, "@c"+strings.Repeat("x", 4)+string(rune('a'+i%26)))
	}
	if run = runSyncCLI(t, many, reply); run.code != exitcode.Usage || run.method != "" {
		t.Fatalf("101 chats: %+v", run)
	}
}

func TestChatSyncGapRelativeDepthMatchesSince(t *testing.T) {
	for _, args := range [][]string{{"--days", "30"}, {"--months", "2"}} {
		relative := runSyncCLI(t, append([]string{"chat", "sync", "start", "42"}, args...), syncDepthReply)
		if relative.code != exitcode.OK {
			t.Fatalf("relative: %+v", relative)
		}
		var a, b rpc.HistorySyncParams
		if err := json.Unmarshal(relative.params, &a); err != nil {
			t.Fatal(err)
		}
		absolute := runSyncCLI(t, []string{"chat", "sync", "start", "42", "--since", a.Since}, syncDepthReply)
		if absolute.code != exitcode.OK {
			t.Fatalf("absolute: %+v", absolute)
		}
		if err := json.Unmarshal(absolute.params, &b); err != nil || a != b {
			t.Fatalf("params: %+v %+v %v", a, b, err)
		}
	}
}
