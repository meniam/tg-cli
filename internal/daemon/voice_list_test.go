package daemon

import (
	"context"
	"encoding/base64"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// voiceListDaemon stores two chats and five jobs: three in chat 10 (open), two
// in chat 77 (the tests close it). Telegram is not involved: the fake counts
// its calls and the tests expect none.
func voiceListDaemon(t *testing.T) (*daemon, string, func() int) {
	t.Helper()
	d, tg, db, path := accessTestDaemon(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 10, Type: "user", Title: "Alice"}, {ID: 77, Type: "group", Title: "Private"}}); err != nil {
		t.Fatal(err)
	}
	for _, j := range []store.VoiceJob{
		{ChatID: 10, MessageID: 1, Kind: store.VoiceKindVoice, Source: store.VoiceSourceIncoming, Priority: store.VoicePriorityIncoming},
		{ChatID: 10, MessageID: 2, Kind: store.VoiceKindVideoNote, Source: store.VoiceSourceSync, Priority: store.VoicePrioritySync},
		{ChatID: 77, MessageID: 3, Kind: store.VoiceKindVoice, Source: store.VoiceSourceManual, Priority: store.VoicePriorityManual},
		{ChatID: 10, MessageID: 4, Kind: store.VoiceKindVideo, Source: store.VoiceSourceBackfill, Priority: store.VoicePriorityBackfill},
		{ChatID: 77, MessageID: 5, Kind: store.VoiceKindVoice, Source: store.VoiceSourceBackfill, Priority: store.VoicePriorityBackfill},
	} {
		if _, err := db.EnqueueVoice(ctx, j, false); err != nil {
			t.Fatal(err)
		}
	}
	return d, path, func() int { return resolveCount(tg) }
}

func listVoice(t *testing.T, d *daemon, p rpc.VoiceListParams) rpc.VoiceListResult {
	t.Helper()
	v, err := d.voiceList(context.Background(), params(t, p))
	if err != nil {
		t.Fatalf("voice list %+v: %v", p, err)
	}
	return v.(rpc.VoiceListResult)
}

func voiceListIDs(res rpc.VoiceListResult) []int {
	ids := []int{}
	for _, j := range res.Jobs {
		ids = append(ids, j.MessageID)
	}
	return ids
}

func TestVoiceListFiltersAndPages(t *testing.T) {
	d, _, telegramCalls := voiceListDaemon(t)
	all := listVoice(t, d, rpc.VoiceListParams{})
	if got := voiceListIDs(all); len(got) != 5 || got[0] != 5 || got[4] != 1 || all.NextCursor != "" {
		t.Fatalf("all jobs, newest first = %v cursor %q", got, all.NextCursor)
	}
	first := all.Jobs[4]
	if first.ChatID != 10 || first.ChatTitle != "Alice" || first.Kind != "voice" || first.Source != "incoming" || first.State != "queued" || first.FinishedAt != nil || first.Attempts != 0 {
		t.Errorf("job fields = %+v", first)
	}
	for _, tc := range []struct {
		name string
		p    rpc.VoiceListParams
		want []int
	}{
		{"kind", rpc.VoiceListParams{Kind: "voice"}, []int{5, 3, 1}},
		{"state", rpc.VoiceListParams{State: "done"}, []int{}},
		{"chat by id", rpc.VoiceListParams{Chat: "10"}, []int{4, 2, 1}},
		{"chat by username unknown", rpc.VoiceListParams{Chat: "@nobody"}, []int{}},
	} {
		if got := voiceListIDs(listVoice(t, d, tc.p)); !equalInts(got, tc.want) {
			t.Errorf("%s: ids %v, want %v", tc.name, got, tc.want)
		}
	}
	var got []int
	var cursor string
	for page := 0; page < 4; page++ {
		res := listVoice(t, d, rpc.VoiceListParams{Limit: 2, Cursor: cursor})
		got = append(got, voiceListIDs(res)...)
		if cursor = res.NextCursor; cursor == "" {
			break
		}
	}
	if !equalInts(got, []int{5, 4, 3, 2, 1}) {
		t.Errorf("pages gave %v", got)
	}
	if telegramCalls() != 0 {
		t.Errorf("voice list asked Telegram %d times", telegramCalls())
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestVoiceListHidesClosedChats(t *testing.T) {
	d, accessPath, _ := voiceListDaemon(t)
	writeAccess(t, accessPath, `{"block":[77]}`)
	if got := voiceListIDs(listVoice(t, d, rpc.VoiceListParams{})); !equalInts(got, []int{4, 2, 1}) {
		t.Errorf("with chat 77 closed: ids %v, want [4 2 1]", got)
	}
	// Asking for the closed chat gives an empty page, not an error that would
	// tell the chat exists.
	if got := voiceListIDs(listVoice(t, d, rpc.VoiceListParams{Chat: "77"})); len(got) != 0 {
		t.Errorf("closed chat asked by id: ids %v", got)
	}
	// The closed chat takes no place on a page: a page of two still has two.
	if res := listVoice(t, d, rpc.VoiceListParams{Limit: 2}); !equalInts(voiceListIDs(res), []int{4, 2}) || res.NextCursor == "" {
		t.Errorf("page of two = %v cursor %q", voiceListIDs(res), res.NextCursor)
	}
	writeAccess(t, accessPath, `{"allow":[10]}`)
	if got := voiceListIDs(listVoice(t, d, rpc.VoiceListParams{})); !equalInts(got, []int{4, 2, 1}) {
		t.Errorf("allow list: ids %v", got)
	}
}

func TestVoiceListRejectsBadParams(t *testing.T) {
	d, _, _ := voiceListDaemon(t)
	notNumbers := base64.RawURLEncoding.EncodeToString([]byte("a:b"))
	negative := base64.RawURLEncoding.EncodeToString([]byte("-1:2"))
	noColon := base64.RawURLEncoding.EncodeToString([]byte("12"))
	for _, p := range []rpc.VoiceListParams{
		{State: "paused"}, {Kind: "audio"}, {Limit: -1}, {Limit: 201},
		{Cursor: "***"}, {Cursor: notNumbers}, {Cursor: negative}, {Cursor: noColon},
	} {
		_, err := d.voiceList(context.Background(), params(t, p))
		if code := rpcCode(t, err); code != rpc.CodeInvalidParams {
			t.Errorf("params %+v: code %d, want invalid params (err %v)", p, code, err)
		}
	}
}
