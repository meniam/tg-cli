package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// voiceListFixture queues seven jobs in three chats at increasing times and
// finishes some of them. Chat 3 is the one the tests close. Ids 5 and 6 are
// queued in the same second, so their order rests on the row id.
func voiceListFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "user", Title: "Alice"}, {ID: 3, Type: "user", Title: "Closed"}}); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		at  int64
		job VoiceJob
	}{
		{100, voiceJob(1, 1, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming)},
		{101, voiceJob(1, 2, VoiceKindVideoNote, VoiceSourceSync, VoicePrioritySync)},
		{102, voiceJob(2, 3, VoiceKindVideo, VoiceSourceBackfill, VoicePriorityBackfill)},
		{103, voiceJob(3, 4, VoiceKindVoice, VoiceSourceManual, VoicePriorityManual)},
		{104, voiceJob(1, 5, VoiceKindVoice, VoiceSourceBackfill, VoicePriorityBackfill)},
		{104, voiceJob(1, 6, VoiceKindVoice, VoiceSourceBackfill, VoicePriorityBackfill)},
		{105, voiceJob(2, 7, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming)},
	}
	for _, s := range steps {
		setClock(db, s.at)
		mustEnqueue(t, db, s.job, false)
	}
	finish := func(chat int64, id int, state, reason string, bytes int64, at int64) {
		t.Helper()
		// Claim exactly this job: queue it first by priority with the others cancelled
		// is clumsy, so mark it running through the store's own transition.
		if _, err := db.write.ExecContext(ctx, `UPDATE voice_queue SET state=?,started_at=? WHERE chat_id=? AND message_id=?`, VoiceStateRunning, at, chat, id); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishVoice(ctx, chat, id, state, reason, bytes, time.Unix(at, 0)); err != nil {
			t.Fatal(err)
		}
	}
	finish(1, 1, VoiceStateDone, "", 2048, 110)
	finish(1, 2, VoiceStateFailed, "convert", 512, 111)
	finish(2, 3, VoiceStateSkipped, "no_audio", 0, 112)
	return db
}

func listedIDs(entries []VoiceListEntry) []int {
	var ids []int
	for _, e := range entries {
		ids = append(ids, e.MessageID)
	}
	return ids
}

func TestListVoiceJobsNewestFirstWithFilters(t *testing.T) {
	db := voiceListFixture(t)
	ctx := context.Background()
	cases := []struct {
		name string
		q    VoiceListQuery
		want []int
	}{
		{"all, newest first", VoiceListQuery{Limit: 50}, []int{7, 6, 5, 4, 3, 2, 1}},
		{"state", VoiceListQuery{Limit: 50, State: VoiceStateQueued}, []int{7, 6, 5, 4}},
		{"failed", VoiceListQuery{Limit: 50, State: VoiceStateFailed}, []int{2}},
		{"kind", VoiceListQuery{Limit: 50, Kind: VoiceKindVoice}, []int{7, 6, 5, 4, 1}},
		{"chat", VoiceListQuery{Limit: 50, ChatID: intPtr(2)}, []int{7, 3}},
		{"state, kind and chat", VoiceListQuery{Limit: 50, State: VoiceStateQueued, Kind: VoiceKindVoice, ChatID: intPtr(1)}, []int{6, 5}},
		{"closed chat is gone", VoiceListQuery{Limit: 50, ChatFilter: ChatFilter{Block: []int64{3}}}, []int{7, 6, 5, 3, 2, 1}},
		{"closed chat asked by id", VoiceListQuery{Limit: 50, ChatID: intPtr(3), ChatFilter: ChatFilter{Block: []int64{3}}}, nil},
		{"everything hidden", VoiceListQuery{Limit: 50, ChatFilter: ChatFilter{Hide: true}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, more, err := db.ListVoiceJobs(ctx, tc.q)
			if err != nil || more {
				t.Fatalf("ListVoiceJobs: more=%v err=%v", more, err)
			}
			if !reflect.DeepEqual(listedIDs(got), tc.want) {
				t.Errorf("ids = %v, want %v", listedIDs(got), tc.want)
			}
		})
	}
}

func TestListVoiceJobsFieldsAndTitle(t *testing.T) {
	db := voiceListFixture(t)
	got, _, err := db.ListVoiceJobs(context.Background(), VoiceListQuery{Limit: 50, State: VoiceStateFailed})
	if err != nil || len(got) != 1 {
		t.Fatalf("failed jobs = %+v, %v", got, err)
	}
	e := got[0]
	if e.ChatID != 1 || e.MessageID != 2 || e.Kind != VoiceKindVideoNote || e.Source != VoiceSourceSync || e.State != VoiceStateFailed || e.Reason != "convert" ||
		e.Bytes != 512 || e.EnqueuedAt != 101 || e.FinishedAt != 111 || e.ChatTitle != "Alice" {
		t.Errorf("entry = %+v", e)
	}
	queued, _, err := db.ListVoiceJobs(context.Background(), VoiceListQuery{Limit: 1, State: VoiceStateQueued, ChatID: intPtr(2)})
	if err != nil || len(queued) != 1 || queued[0].FinishedAt != 0 || queued[0].ChatTitle != "" {
		t.Errorf("queued job of an unknown chat = %+v, %v", queued, err)
	}
}

func TestListVoiceJobsPagesWithoutGapsOrRepeats(t *testing.T) {
	db := voiceListFixture(t)
	ctx := context.Background()
	var all []int
	var after *VoiceListPosition
	pages := 0
	for {
		entries, more, err := db.ListVoiceJobs(ctx, VoiceListQuery{Limit: 3, After: after})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		all = append(all, listedIDs(entries)...)
		if !more {
			break
		}
		last := entries[len(entries)-1].Position
		after = &last
		if pages > 5 {
			t.Fatal("paging does not end")
		}
	}
	if want := []int{7, 6, 5, 4, 3, 2, 1}; !reflect.DeepEqual(all, want) || pages != 3 {
		t.Errorf("pages gave %v in %d pages, want %v in 3", all, pages, want)
	}
	// The page boundary falls inside the second with two jobs (5 and 6).
	first, _, _ := db.ListVoiceJobs(ctx, VoiceListQuery{Limit: 2})
	next, _, _ := db.ListVoiceJobs(ctx, VoiceListQuery{Limit: 1, After: &first[1].Position})
	if !reflect.DeepEqual(listedIDs(first), []int{7, 6}) || !reflect.DeepEqual(listedIDs(next), []int{5}) {
		t.Errorf("tie in one second: first %v, next %v", listedIDs(first), listedIDs(next))
	}
}

func TestListVoiceJobsRejectsBadQuery(t *testing.T) {
	db := voiceListFixture(t)
	for _, q := range []VoiceListQuery{{Limit: 0}, {Limit: -1}, {Limit: 5, State: "done'; --"}, {Limit: 5, Kind: "audio"}} {
		if _, _, err := db.ListVoiceJobs(context.Background(), q); err == nil {
			t.Errorf("query %+v was accepted", q)
		}
	}
}
