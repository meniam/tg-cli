package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func voiceJob(chat int64, id int, kind, source string, priority int) VoiceJob {
	return VoiceJob{ChatID: chat, MessageID: id, Kind: kind, Source: source, Priority: priority}
}

func mustEnqueue(t *testing.T, db *DB, j VoiceJob, retry bool) VoiceEnqueueOutcome {
	t.Helper()
	out, err := db.EnqueueVoice(context.Background(), j, retry)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustClaim(t *testing.T, db *DB, now int64) (VoiceJob, bool) {
	t.Helper()
	j, ok, err := db.ClaimNextVoice(context.Background(), time.Unix(now, 0))
	if err != nil {
		t.Fatal(err)
	}
	return j, ok
}

func mustGetJob(t *testing.T, db *DB, chat int64, id int) VoiceJob {
	t.Helper()
	j, err := db.GetVoiceJob(context.Background(), chat, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestVoiceQueueClaimsByPriorityThenAge(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	steps := []struct {
		at  int64
		job VoiceJob
	}{
		{100, voiceJob(1, 1, VoiceKindVoice, VoiceSourceBackfill, VoicePriorityBackfill)},
		{101, voiceJob(1, 2, VoiceKindVoice, VoiceSourceSync, VoicePrioritySync)},
		{102, voiceJob(1, 3, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming)},
		{103, voiceJob(1, 4, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming)},
		{104, voiceJob(2, 5, VoiceKindVideo, VoiceSourceManual, VoicePriorityManual)},
		// Same second as the previous one: the order of insertion decides.
		{104, voiceJob(2, 6, VoiceKindVideoNote, VoiceSourceManual, VoicePriorityManual)},
	}
	for _, s := range steps {
		setClock(db, s.at)
		if out := mustEnqueue(t, db, s.job, false); out != VoiceEnqueued {
			t.Fatalf("enqueue %d: outcome %v", s.job.MessageID, out)
		}
	}
	var order []int
	for i := 0; ; i++ {
		j, ok := mustClaim(t, db, 200+int64(i))
		if !ok {
			break
		}
		if j.State != VoiceStateRunning || j.StartedAt != 200+int64(i) {
			t.Fatalf("claimed job = %+v", j)
		}
		order = append(order, j.MessageID)
	}
	if want := []int{5, 6, 3, 4, 2, 1}; !reflect.DeepEqual(order, want) {
		t.Fatalf("claim order = %v, want %v", order, want)
	}
	if _, ok := mustClaim(t, db, 300); ok {
		t.Fatal("claim on an empty queue returned a job")
	}
	counts, err := db.VoiceQueueCounts(ctx)
	if err != nil || counts != (VoiceQueueCounts{Running: 6}) {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestVoiceQueueDuplicateRaisesPriorityOnly(t *testing.T) {
	db := openTestDB(t)
	setClock(db, 100)
	mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceBackfill, VoicePriorityBackfill), false)
	setClock(db, 150)
	if out := mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceSync, VoicePrioritySync), false); out != VoiceRaised {
		t.Fatalf("higher priority outcome = %v", out)
	}
	if out := mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceBackfill, VoicePriorityBackfill), false); out != VoiceUnchanged {
		t.Fatalf("lower priority outcome = %v", out)
	}
	if out := mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceSync, VoicePrioritySync), false); out != VoiceUnchanged {
		t.Fatalf("equal priority outcome = %v", out)
	}
	j := mustGetJob(t, db, 1, 1)
	if j.Priority != VoicePrioritySync || j.Source != VoiceSourceSync || j.EnqueuedAt != 100 || j.State != VoiceStateQueued {
		t.Fatalf("job after raise = %+v", j)
	}
	counts, err := db.VoiceQueueCounts(context.Background())
	if err != nil || counts != (VoiceQueueCounts{Queued: 1}) {
		t.Fatalf("a duplicate made a second row: %+v, %v", counts, err)
	}
	// A raised job overtakes an older one with a lower priority.
	mustEnqueue(t, db, voiceJob(1, 2, VoiceKindVoice, VoiceSourceBackfill, VoicePriorityBackfill), false)
	mustEnqueue(t, db, voiceJob(1, 2, VoiceKindVoice, VoiceSourceManual, VoicePriorityManual), false)
	if first, _ := mustClaim(t, db, 200); first.MessageID != 2 {
		t.Fatalf("first claimed = %d, want the raised job 2", first.MessageID)
	}
}

func TestVoiceQueueRunningJobIsNotTouchedByDuplicate(t *testing.T) {
	db := openTestDB(t)
	setClock(db, 100)
	mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceSync, VoicePrioritySync), false)
	running, _ := mustClaim(t, db, 110)
	for _, retry := range []bool{false, true} {
		if out := mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceManual, VoicePriorityManual), retry); out != VoiceUnchanged {
			t.Fatalf("duplicate of a running job (retry=%v): outcome %v", retry, out)
		}
	}
	if got := mustGetJob(t, db, 1, 1); got != running {
		t.Fatalf("running job changed: %+v, was %+v", got, running)
	}
}

func TestVoiceQueueFinishedJobIsQueuedAgainOnlyOnRetry(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	states := []string{VoiceStateDone, VoiceStateFailed, VoiceStateSkipped}
	finishedJobs := make([]VoiceJob, len(states))
	for i, state := range states {
		id := i + 1
		setClock(db, 100)
		mustEnqueue(t, db, voiceJob(1, id, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming), false)
		mustClaim(t, db, 110)
		if _, err := db.RequeueVoice(ctx, 1, id, "network"); err != nil {
			t.Fatal(err)
		}
		mustClaim(t, db, 120)
		if err := db.FinishVoice(ctx, 1, id, state, "why", 1234, time.Unix(130, 0)); err != nil {
			t.Fatal(err)
		}
		finished := mustGetJob(t, db, 1, id)
		if finished.State != state || finished.Reason != "why" || finished.Bytes != 1234 || finished.FinishedAt != 130 || finished.Attempts != 1 {
			t.Fatalf("finished %s job = %+v", state, finished)
		}
		finishedJobs[i] = finished
	}
	for i, state := range states {
		id := i + 1
		finished := finishedJobs[i]
		setClock(db, 500)
		again := voiceJob(1, id, VoiceKindVideo, VoiceSourceManual, VoicePriorityManual)
		if out := mustEnqueue(t, db, again, false); out != VoiceUnchanged {
			t.Fatalf("%s job without retry: outcome %v", state, out)
		}
		if got := mustGetJob(t, db, 1, id); got != finished {
			t.Fatalf("%s job changed without retry: %+v", state, got)
		}
		if out := mustEnqueue(t, db, again, true); out != VoiceEnqueued {
			t.Fatalf("%s job with retry: outcome %v", state, out)
		}
		got := mustGetJob(t, db, 1, id)
		want := VoiceJob{ChatID: 1, MessageID: id, Kind: VoiceKindVideo, Source: VoiceSourceManual, Priority: VoicePriorityManual, State: VoiceStateQueued, EnqueuedAt: 500}
		if got != want {
			t.Fatalf("%s job after retry = %+v, want %+v", state, got, want)
		}
	}
}

func TestVoiceQueueRejectsInvalidJob(t *testing.T) {
	db := openTestDB(t)
	for _, j := range []VoiceJob{
		voiceJob(1, 1, "audio", VoiceSourceManual, 0),
		voiceJob(1, 1, VoiceKindVoice, "cron", 0),
		voiceJob(1, 1, VoiceKindVoice, VoiceSourceManual, -1),
	} {
		if _, err := db.EnqueueVoice(context.Background(), j, false); err == nil {
			t.Errorf("job %+v accepted", j)
		}
	}
	if counts, err := db.VoiceQueueCounts(context.Background()); err != nil || counts != (VoiceQueueCounts{}) {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestVoiceQueueRecoverRunningAfterRestart(t *testing.T) {
	path := t.TempDir() + "/store.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	setClock(db, 100)
	mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming), false)
	mustEnqueue(t, db, voiceJob(1, 2, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming), false)
	mustEnqueue(t, db, voiceJob(1, 3, VoiceKindVoice, VoiceSourceIncoming, VoicePriorityIncoming), false)
	mustClaim(t, db, 110)
	mustClaim(t, db, 111)
	if err := db.FinishVoice(ctx, 1, 2, VoiceStateDone, "", 10, time.Unix(120, 0)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	counts, err := db.VoiceQueueCounts(ctx)
	if err != nil || counts != (VoiceQueueCounts{Queued: 1, Running: 1, Done: 1}) {
		t.Fatalf("counts after reopen = %+v, %v", counts, err)
	}
	n, err := db.RecoverRunningVoice(ctx)
	if err != nil || n != 1 {
		t.Fatalf("recovered = %d, %v", n, err)
	}
	j := mustGetJob(t, db, 1, 1)
	if j.State != VoiceStateQueued || j.Reason != VoiceReasonRecovered || j.StartedAt != 0 || j.Attempts != 0 {
		t.Fatalf("recovered job = %+v", j)
	}
	if got := mustGetJob(t, db, 1, 2); got.State != VoiceStateDone {
		t.Fatalf("a finished job was touched: %+v", got)
	}
	if n, err = db.RecoverRunningVoice(ctx); err != nil || n != 0 {
		t.Fatalf("second recover = %d, %v", n, err)
	}
	// The recovered job keeps its place: it was enqueued first.
	if first, _ := mustClaim(t, db, 200); first.MessageID != 1 {
		t.Fatalf("first claimed = %d, want 1", first.MessageID)
	}
}

func TestVoiceQueueRequeueCountsAttempts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceManual, VoicePriorityManual), false)
	if _, err := db.RequeueVoice(ctx, 1, 1, "network"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("requeue of a queued job: %v", err)
	}
	for want := 1; want <= 3; want++ {
		mustClaim(t, db, 110)
		attempts, err := db.RequeueVoice(ctx, 1, 1, "network")
		if err != nil || attempts != want {
			t.Fatalf("attempts = %d, %v, want %d", attempts, err, want)
		}
		j := mustGetJob(t, db, 1, 1)
		if j.State != VoiceStateQueued || j.Attempts != want || j.Reason != "network" || j.StartedAt != 0 {
			t.Fatalf("requeued job = %+v", j)
		}
	}
	// Queuing a queued job again does not reset the count.
	mustEnqueue(t, db, voiceJob(1, 1, VoiceKindVoice, VoiceSourceManual, VoicePriorityManual), false)
	if j := mustGetJob(t, db, 1, 1); j.Attempts != 3 {
		t.Fatalf("attempts after duplicate = %d, want 3", j.Attempts)
	}
	mustClaim(t, db, 120)
	if err := db.FinishVoice(ctx, 1, 1, VoiceStateFailed, "network", 0, time.Unix(130, 0)); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishVoice(ctx, 1, 1, VoiceStateDone, "", 0, time.Unix(131, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish of a finished job: %v", err)
	}
	if err := db.FinishVoice(ctx, 1, 1, VoiceStateRunning, "", 0, time.Unix(131, 0)); err == nil {
		t.Fatal("finish into running accepted")
	}
	if err := db.FinishVoice(ctx, 1, 99, VoiceStateDone, "", 0, time.Unix(131, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish of a missing job: %v", err)
	}
}

func TestVoiceQueueCancel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	for id := 1; id <= 4; id++ {
		mustEnqueue(t, db, voiceJob(1, id, VoiceKindVoice, VoiceSourceSync, VoicePrioritySync), false)
	}
	mustClaim(t, db, 110) // job 1 runs
	setClock(db, 120)
	ok, err := db.CancelVoice(ctx, 1, 2)
	if err != nil || !ok {
		t.Fatalf("cancel queued = %v, %v", ok, err)
	}
	if got := mustGetJob(t, db, 1, 2); got.State != VoiceStateSkipped || got.Reason != VoiceReasonCancelled || got.FinishedAt != 120 {
		t.Fatalf("cancelled job = %+v", got)
	}
	if ok, err = db.CancelVoice(ctx, 1, 2); err != nil || ok {
		t.Fatalf("second cancel = %v, %v", ok, err)
	}
	if ok, err = db.CancelVoice(ctx, 1, 1); err != nil || ok {
		t.Fatalf("cancel of a running job = %v, %v", ok, err)
	}
	if got := mustGetJob(t, db, 1, 1); got.State != VoiceStateRunning {
		t.Fatalf("running job changed: %+v", got)
	}
	n, err := db.CancelAllVoice(ctx)
	if err != nil || n != 2 {
		t.Fatalf("cancel all = %d, %v", n, err)
	}
	counts, err := db.VoiceQueueCounts(ctx)
	if err != nil || counts != (VoiceQueueCounts{Running: 1, Skipped: 3}) {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
	if _, ok := mustClaim(t, db, 200); ok {
		t.Fatal("a cancelled job was claimed")
	}
	// A cancelled job can be queued again by an explicit retry.
	if out := mustEnqueue(t, db, voiceJob(1, 3, VoiceKindVoice, VoiceSourceManual, VoicePriorityManual), true); out != VoiceEnqueued {
		t.Fatalf("retry of a cancelled job: %v", out)
	}
}

func TestVoiceQueueUsagePerDay(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if got, err := db.VoiceUsage(ctx, "2026-11-20"); err != nil || got != (VoiceUsage{Day: "2026-11-20"}) {
		t.Fatalf("empty day = %+v, %v", got, err)
	}
	for _, add := range []struct {
		day   string
		files int
		bytes int64
	}{
		{"2026-11-20", 1, 1 << 20},
		{"2026-11-20", 2, 5 << 30},
		{"2026-11-21", 1, 7},
	} {
		if err := db.AddVoiceUsage(ctx, add.day, add.files, add.bytes); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := db.VoiceUsage(ctx, "2026-11-20"); err != nil || got != (VoiceUsage{Day: "2026-11-20", Files: 3, Bytes: 1<<20 + 5<<30}) {
		t.Fatalf("day 20 = %+v, %v", got, err)
	}
	if got, err := db.VoiceUsage(ctx, "2026-11-21"); err != nil || got != (VoiceUsage{Day: "2026-11-21", Files: 1, Bytes: 7}) {
		t.Fatalf("day 21 = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "20261120", "2026-11-2", "2026-13-01", "2026-11-20T00:00"} {
		if err := db.AddVoiceUsage(ctx, bad, 1, 1); err == nil {
			t.Errorf("day %q accepted", bad)
		}
		if _, err := db.VoiceUsage(ctx, bad); err == nil {
			t.Errorf("day %q accepted by VoiceUsage", bad)
		}
	}
	if err := db.AddVoiceUsage(ctx, "2026-11-22", -1, 0); err == nil {
		t.Error("negative usage accepted")
	}
}

func TestVoiceQueueChatMarks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.GetVoiceChat(ctx, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing mark: %v", err)
	}
	if err := db.SetVoiceChat(ctx, -1001, false, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetVoiceChat(ctx, 5, true, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	// Marking again changes the video flag and keeps the first time.
	if err := db.SetVoiceChat(ctx, -1001, true, time.Unix(300, 0)); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetVoiceChat(ctx, -1001)
	if err != nil || got != (VoiceChat{ChatID: -1001, Video: true, EnabledAt: 100}) {
		t.Fatalf("mark = %+v, %v", got, err)
	}
	list, err := db.ListVoiceChats(ctx)
	want := []VoiceChat{{ChatID: -1001, Video: true, EnabledAt: 100}, {ChatID: 5, Video: true, EnabledAt: 200}}
	if err != nil || !reflect.DeepEqual(list, want) {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if ok, err := db.ClearVoiceChat(ctx, -1001); err != nil || !ok {
		t.Fatalf("clear = %v, %v", ok, err)
	}
	if ok, err := db.ClearVoiceChat(ctx, -1001); err != nil || ok {
		t.Fatalf("second clear = %v, %v", ok, err)
	}
	if _, err := db.GetVoiceChat(ctx, -1001); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cleared mark: %v", err)
	}
	if list, err = db.ListVoiceChats(ctx); err != nil || len(list) != 1 || list[0].ChatID != 5 {
		t.Fatalf("list after clear = %+v, %v", list, err)
	}
}

// Many goroutines enqueue the same jobs and claim: a job is handed out once and
// nothing is duplicated. Meaningful under -race.
func TestVoiceQueueConcurrentClaimsAreExclusive(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const jobs = 40
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := 1; id <= jobs; id++ {
				if _, err := db.EnqueueVoice(ctx, voiceJob(1, id, VoiceKindVoice, VoiceSourceSync, VoicePrioritySync), false); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	var mu sync.Mutex
	seen := map[int]int{}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, ok, err := db.ClaimNextVoice(ctx, time.Unix(1, 0))
				if err != nil {
					t.Error(err)
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				seen[j.MessageID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != jobs {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %d claimed %d times", id, n)
		}
	}
}

// The newest migration only adds: a database of the previous version keeps its
// transcriptions, which read as Telegram's, and the new tables work.
func TestVoiceQueueMigrationFromEleven(t *testing.T) {
	ctx := context.Background()
	path := databaseAtVersion(t, 11)
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schemaExec(t, old, `INSERT INTO chats(id,type,title,search_text,first_seen_at,last_seen_at) VALUES(1,'user','Ann','ann',1,1)`)
	schemaExec(t, old, `INSERT INTO messages(chat_id,id,date,text,media_type,raw,search_text,first_seen_at,last_seen_at) VALUES(1,7,100,'','voice','{}','',100,100)`)
	schemaExec(t, old, `INSERT INTO transcriptions(chat_id,message_id,transcription_id,text,pending,updated_at,raw) VALUES(1,7,9,'Доставка завтра',0,150,'{"x":{"a":1}}')`)
	schemaExec(t, old, `INSERT INTO transcriptions(chat_id,message_id,transcription_id,text,pending,updated_at) VALUES(1,8,10,'',1,151)`)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, err := db.Version(); err != nil || v != newestMigrationVersion(t) || v < 12 {
		t.Fatalf("version = %d, %v", v, err)
	}
	got, err := db.GetTranscription(ctx, 1, 7)
	want := Transcription{ChatID: 1, MessageID: 7, TranscriptionID: 9, Text: "Доставка завтра", UpdatedAt: 150, Source: TranscriptionSourceTelegram}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("old transcription = %+v, %v, want %+v", got, err, want)
	}
	if schemaCount(t, db.read, `SELECT count(*) FROM transcriptions`) != 2 {
		t.Fatal("transcriptions lost")
	}
	var raw string
	if err := db.read.QueryRow(`SELECT raw FROM transcriptions WHERE message_id=7`).Scan(&raw); err != nil || raw != `{"x":{"a":1}}` {
		t.Fatalf("raw = %q, %v", raw, err)
	}
	// The word index still finds the old transcription.
	if exact, stemmed := wordLegIDs(t, db, "доставка", ChatFilter{}); len(exact) != 1 || len(stemmed) != 1 {
		t.Fatalf("old transcription not searchable: %v %v", exact, stemmed)
	}
	mustEnqueue(t, db, voiceJob(1, 7, VoiceKindVoice, VoiceSourceManual, VoicePriorityManual), false)
	if err := db.SetVoiceChat(ctx, 1, false, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := db.AddVoiceUsage(ctx, "2026-11-20", 1, 1); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := db.read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %q, %v", integrity, err)
	}
	checkFTSIntegrity(t, db.write)
}

func TestVoiceQueueTranscriptionSourceEngineDuration(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	duration := 12.5
	local := Transcription{ChatID: transcriptChat, MessageID: 10, Text: "Доставка будет завтра", Source: TranscriptionSourceLocal, Engine: "gigaam-v3", Duration: &duration, Raw: raw(`{"segments":[{"start":0,"end":2,"text":"Доставка будет завтра"}]}`)}
	if err := db.PutTranscription(ctx, local); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTranscription(ctx, transcriptChat, 10)
	if err != nil || got.Source != TranscriptionSourceLocal || got.Engine != "gigaam-v3" || got.Duration == nil || *got.Duration != 12.5 || got.Pending || got.Text != local.Text {
		t.Fatalf("local transcription = %+v, %v", got, err)
	}
	// The search text follows a local transcription as it does Telegram's.
	if exact, stemmed := wordLegIDs(t, db, "доставка", ChatFilter{}); len(exact) != 1 || len(stemmed) != 1 {
		t.Fatalf("local transcription not searchable: %v %v", exact, stemmed)
	}
	// A call that knows nothing of the new fields stores Telegram's source and no duration.
	putTranscript(t, db, transcriptChat, 11, 5, "Квитанция оплачена", false)
	got, err = db.GetTranscription(ctx, transcriptChat, 11)
	if err != nil || got.Source != TranscriptionSourceTelegram || got.Engine != "" || got.Duration != nil {
		t.Fatalf("default transcription = %+v, %v", got, err)
	}
	// Saving the local result over a Telegram one replaces source, engine and duration.
	if err := db.PutTranscription(ctx, Transcription{ChatID: transcriptChat, MessageID: 11, Text: "Квитанция оплачена вчера", Source: TranscriptionSourceLocal, Engine: "gigaam-v3", Duration: &duration}); err != nil {
		t.Fatal(err)
	}
	if got, err = db.GetTranscription(ctx, transcriptChat, 11); err != nil || got.Source != TranscriptionSourceLocal || got.Duration == nil || got.Text != "Квитанция оплачена вчера" {
		t.Fatalf("replaced transcription = %+v, %v", got, err)
	}
	if err := db.PutTranscription(ctx, Transcription{ChatID: transcriptChat, MessageID: 12, Text: "x", Source: "whisper"}); err == nil {
		t.Fatal("unknown source accepted")
	}
}

// An update from Telegram does not blank the source the row was saved with.
func TestVoiceQueueUpdateKeepsSourceFields(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	duration := 3.0
	if err := db.PutTranscription(ctx, Transcription{ChatID: transcriptChat, MessageID: 10, TranscriptionID: 4, Pending: true, Source: TranscriptionSourceTelegram, Duration: &duration}); err != nil {
		t.Fatal(err)
	}
	updated, err := db.UpdateTranscription(ctx, Transcription{ChatID: transcriptChat, MessageID: 10, TranscriptionID: 4, Text: "Склад закрыт"})
	if err != nil || !updated {
		t.Fatalf("update = %v, %v", updated, err)
	}
	got, err := db.GetTranscription(ctx, transcriptChat, 10)
	if err != nil || got.Source != TranscriptionSourceTelegram || got.Duration == nil || *got.Duration != 3 || got.Text != "Склад закрыт" || got.Pending {
		t.Fatalf("after update = %+v, %v", got, err)
	}
	if exact, _ := wordLegIDs(t, db, "закрыт", ChatFilter{}); len(exact) != 1 {
		t.Fatalf("updated text not searchable: %v", exact)
	}
}
