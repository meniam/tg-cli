package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"

	_ "modernc.org/sqlite"
)

// voiceT0 is the local time the voice tests start at: ten in the morning, so a
// budget pause ends at the next local midnight, fourteen hours later.
var voiceT0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local)

// voiceFake is a Telegram client that also downloads voice files. It makes the
// file itself (no network) and counts every call, messages.transcribeAudio
// included: the worker must never make that one.
type voiceFake struct {
	*fakeTelegram
	mu              sync.Mutex
	size            int
	downloads       []VoiceDownload
	downloadErrs    []error // consumed one per download call
	downloadFn      func(ctx context.Context, req VoiceDownload) error
	transcribeCalls int
	// transcribeAnswer is what a Telegram-side transcription returns; nil makes
	// the call an error, because only an explicit --telegram may reach it.
	transcribeAnswer *rpc.Transcription
	cleans           int
}

func (f *voiceFake) SetTranscriptionSink(func(rpc.Transcription)) {}
func (f *voiceFake) Transcribe(context.Context, chatref.Ref, int) (rpc.Transcription, error) {
	f.mu.Lock()
	f.transcribeCalls++
	answer := f.transcribeAnswer
	f.mu.Unlock()
	if answer != nil {
		return *answer, nil
	}
	return rpc.Transcription{}, errors.New("the worker must not call messages.transcribeAudio")
}

func (f *voiceFake) DownloadVoiceFile(ctx context.Context, ref chatref.Ref, id int, req VoiceDownload) (VoiceFile, error) {
	f.mu.Lock()
	f.downloads = append(f.downloads, req)
	var err error
	if len(f.downloadErrs) > 0 {
		err, f.downloadErrs = f.downloadErrs[0], f.downloadErrs[1:]
	}
	fn, size := f.downloadFn, f.size
	f.mu.Unlock()
	if fn != nil {
		if err := fn(ctx, req); err != nil {
			return VoiceFile{}, err
		}
	}
	if err != nil {
		return VoiceFile{}, err
	}
	if size == 0 {
		size = 1000
	}
	path := filepath.Join(req.Dir, fmt.Sprintf("%d_%d.media", ref.ID, id))
	if err := os.MkdirAll(req.Dir, 0o700); err != nil {
		return VoiceFile{}, err
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		return VoiceFile{}, err
	}
	return VoiceFile{Path: path, Size: int64(size)}, nil
}

func (f *voiceFake) CleanVoiceTempDir(dir string) error {
	f.mu.Lock()
	f.cleans++
	f.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (f *voiceFake) downloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.downloads)
}

func (f *voiceFake) transcribed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transcribeCalls
}

// voiceFakeEngine stands in for ffmpeg and the speech engine. The files it
// makes are real, so the tests can look at the working directory.
type voiceFakeEngine struct {
	mu            sync.Mutex
	missing       []string
	convertErr    error
	transcribeErr error
	diskErr       error
	segments      []VoiceSegment
	seconds       float64
	// onTranscribe runs inside Transcribe; it may block until ctx ends or panic.
	onTranscribe func(ctx context.Context) error
	// calls records the order and the arguments of the programs.
	calls      []string
	timeouts   []float64
	ffmpeg     string
	threads    int
	diskNeeded []int64
}

func (e *voiceFakeEngine) Missing(string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.missing...)
}

func (e *voiceFakeEngine) setMissing(m ...string) {
	e.mu.Lock()
	e.missing = m
	e.mu.Unlock()
}

func (e *voiceFakeEngine) Convert(_ context.Context, ffmpeg, in, out string, timeout time.Duration) error {
	e.mu.Lock()
	e.calls = append(e.calls, "convert")
	e.ffmpeg = ffmpeg
	err := e.convertErr
	e.mu.Unlock()
	if _, statErr := os.Stat(in); statErr != nil {
		return fmt.Errorf("the input of convert is missing: %w", statErr)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(out, make([]byte, 64), 0o600)
}

func (e *voiceFakeEngine) Transcribe(ctx context.Context, wav string, threads int, _ time.Duration) ([]VoiceSegment, error) {
	e.mu.Lock()
	e.calls = append(e.calls, "transcribe")
	e.threads = threads
	hook, err, segs := e.onTranscribe, e.transcribeErr, e.segments
	e.mu.Unlock()
	if _, statErr := os.Stat(wav); statErr != nil {
		return nil, fmt.Errorf("the wav of transcribe is missing: %w", statErr)
	}
	if hook != nil {
		if err := hook(ctx); err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	return segs, nil
}

func (e *voiceFakeEngine) WavSeconds(string) (float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.seconds, nil
}

func (e *voiceFakeEngine) CheckDisk(_ string, need int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.diskNeeded = append(e.diskNeeded, need)
	return e.diskErr
}

func (e *voiceFakeEngine) Timeout(seconds float64) time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.timeouts = append(e.timeouts, seconds)
	return time.Duration(seconds+120) * time.Second
}

func (e *voiceFakeEngine) callList() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

type voiceFixture struct {
	t          *testing.T
	d          *daemon
	db         *store.DB
	tg         *voiceFake
	eng        *voiceFakeEngine
	clock      *schedClock
	w          *voiceWorker
	dir        string
	accessPath string
	root       context.Context
	cancel     context.CancelFunc
}

// voiceTestConfig is a checked configuration with the pause between downloads
// off, so a test that does not look at it never waits for it.
func voiceTestConfig() VoiceTranscriptionConfig {
	return VoiceTranscriptionConfig{
		Enabled: true, FFmpeg: "ffmpeg", MaxVoiceMB: 30, MaxVideoNoteMB: 50, MaxVideoMB: 500, MaxVideoMinutes: 120,
		DailyDownloadMB: 2000, DailyFiles: 200, Pause: 0, Threads: 4,
	}
}

func newVoiceFixture(t *testing.T, cfg VoiceTranscriptionConfig) *voiceFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	base := newFake()
	close(base.ready)
	base.user = &rpc.User{ID: 42}
	tg := &voiceFake{fakeTelegram: base}
	accessPath := filepath.Join(dir, "access.json")
	logger := discardLogger()
	d := &daemon{
		opts:  Options{Telegram: tg, Access: access.Open(accessPath), Paths: Paths{Dir: dir, DatabaseDir: dir, Deletions: filepath.Join(dir, "deletions.jsonl")}},
		store: db, log: logger, srv: rpc.NewServer(logger), hub: rpc.NewHub(),
	}
	eng := &voiceFakeEngine{seconds: 12.5, segments: []VoiceSegment{{Start: 0.5, End: 3.25, Text: "привет"}, {Start: 4, End: 9.75, Text: "как дела"}}}
	clock := newSchedClock(voiceT0)
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := newVoiceWorker(d, root, cfg, eng, clock)
	d.voice = w
	f := &voiceFixture{t: t, d: d, db: db, tg: tg, eng: eng, clock: clock, w: w, dir: dir, accessPath: accessPath, root: root, cancel: cancel}
	t.Cleanup(func() { cancel(); w.wait() })
	return f
}

// voiceRaw is the raw Telegram object of a voice message, a video note or a
// video, with the size and the duration the worker reads before downloading. A
// zero size or duration is left out, as an old message leaves it out.
func voiceRaw(kind string, size int64, seconds float64) json.RawMessage {
	attr := fmt.Sprintf(`{"_":"documentAttributeAudio","voice":true,"duration":%d}`, int(seconds))
	switch kind {
	case store.VoiceKindVideoNote:
		attr = fmt.Sprintf(`{"_":"documentAttributeVideo","round_message":true,"duration":%g}`, seconds)
	case store.VoiceKindVideo:
		attr = fmt.Sprintf(`{"_":"documentAttributeVideo","duration":%g}`, seconds)
	}
	if seconds == 0 {
		attr = `{"_":"documentAttributeFilename","file_name":"secret-name.ogg"}`
	}
	doc := `{"_":"document"`
	if size > 0 {
		doc += fmt.Sprintf(`,"size":%d`, size)
	}
	doc += `,"attributes":[` + attr + `]}`
	return json.RawMessage(`{"_":"message","media":{"_":"messageMediaDocument","document":` + doc + `}}`)
}

// seed stores a message of a kind with the given size and duration.
func (f *voiceFixture) seed(chat int64, id int, kind string, size int64, seconds float64) {
	f.t.Helper()
	own := false
	media := kind
	if err := f.db.UpsertMessages(context.Background(), []store.Message{{
		ChatID: chat, ID: id, Date: voiceT0.Unix(), MediaType: &media, Out: &own, Raw: voiceRaw(kind, size, seconds),
	}}); err != nil {
		f.t.Fatal(err)
	}
}

// queue puts a job into the queue straight through the store, whatever the
// setting says: it stands for a job a source or an older run left behind.
func (f *voiceFixture) queue(chat int64, id int, kind, source string, priority int) {
	f.t.Helper()
	if _, err := f.db.EnqueueVoice(context.Background(), store.VoiceJob{ChatID: chat, MessageID: id, Kind: kind, Source: source, Priority: priority}, false); err != nil {
		f.t.Fatal(err)
	}
}

func (f *voiceFixture) job(chat int64, id int) store.VoiceJob {
	f.t.Helper()
	j, err := f.db.GetVoiceJob(context.Background(), chat, id)
	if err != nil {
		f.t.Fatalf("job %d/%d: %v", chat, id, err)
	}
	return j
}

// drain runs the worker synchronously, without its goroutine, until it has
// nothing more to do.
func (f *voiceFixture) drain() {
	f.t.Helper()
	for range 50 {
		if _, again := f.w.pass(f.root); !again {
			return
		}
	}
	f.t.Fatal("the worker did not settle")
}

// holdWorker keeps the goroutine of the worker from starting, so a test that
// queues jobs through the daemon looks at the queue and drives the worker
// itself with drain.
func (f *voiceFixture) holdWorker() {
	f.t.Helper()
	f.w.startOnce.Do(func() {})
}

func (f *voiceFixture) start() {
	f.t.Helper()
	f.w.ensureStarted()
}

func (f *voiceFixture) awaitState(chat int64, id int, state string) store.VoiceJob {
	f.t.Helper()
	var j store.VoiceJob
	awaitCond(f.t, fmt.Sprintf("job %d/%d to be %s", chat, id, state), func() bool {
		var err error
		j, err = f.db.GetVoiceJob(context.Background(), chat, id)
		return err == nil && j.State == state
	})
	return j
}

// noFiles fails the test when the working directory holds anything.
func (f *voiceFixture) noFiles() {
	f.t.Helper()
	entries, err := os.ReadDir(f.w.dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) > 0 {
		f.t.Fatalf("the working directory is not empty: %v", names)
	}
}

func (f *voiceFixture) status() rpc.VoiceStatus {
	f.t.Helper()
	st, err := f.w.status(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func (f *voiceFixture) daysAfter(n int) time.Time {
	return time.Date(voiceT0.Year(), voiceT0.Month(), voiceT0.Day()+n, 0, 0, 0, 0, time.Local)
}

// TestVoiceWorkerChain: one incoming voice message goes through the whole
// chain, ends with a local text and leaves nothing behind. Telegram is asked
// for the file only.
func TestVoiceWorkerChain(t *testing.T) {
	f := newVoiceFixture(t, voiceTestConfig())
	f.seed(1001, 7, store.VoiceKindVoice, 2048, 12)
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceIncoming, store.VoicePriorityIncoming)
	f.w.recover(f.root)
	f.drain()

	j := f.job(1001, 7)
	if j.State != store.VoiceStateDone || j.Reason != "" || j.Bytes != 1000 {
		t.Fatalf("job = %+v", j)
	}
	got, err := f.db.GetTranscription(context.Background(), 1001, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != store.TranscriptionSourceLocal || got.Engine != "gigaam-v3" || got.Pending || got.TranscriptionID != 0 || got.Text != "привет как дела" {
		t.Fatalf("transcription = %+v", got)
	}
	if got.Duration == nil || *got.Duration != 12.5 {
		t.Fatalf("duration = %v", got.Duration)
	}
	var raw struct {
		Local struct {
			Engine   string         `json:"engine"`
			Segments []VoiceSegment `json:"segments"`
		} `json:"voice_local"`
	}
	stored := f.rawTranscription(1001, 7)
	if err := json.Unmarshal(stored, &raw); err != nil || raw.Local.Engine != "gigaam-v3" || len(raw.Local.Segments) != 2 || raw.Local.Segments[1] != (VoiceSegment{Start: 4, End: 9.75, Text: "как дела"}) {
		t.Fatalf("raw = %s (%v)", stored, err)
	}
	if n := f.tg.transcribed(); n != 0 {
		t.Fatalf("messages.transcribeAudio was called %d times", n)
	}
	if calls := f.eng.callList(); strings.Join(calls, ",") != "convert,transcribe" || f.eng.ffmpeg != "ffmpeg" || f.eng.threads != 4 {
		t.Fatalf("engine calls = %v ffmpeg=%q threads=%d", calls, f.eng.ffmpeg, f.eng.threads)
	}
	if len(f.tg.downloads) != 1 || f.tg.downloads[0].MaxBytes != 30<<20 || f.tg.downloads[0].ExpectedSize != 2048 || f.tg.downloads[0].Dir != f.w.dir {
		t.Fatalf("download request = %+v", f.tg.downloads)
	}
	if want := filepath.Join(f.dir, "tmp-voice"); f.w.dir != want {
		t.Fatalf("working directory = %q, want %q", f.w.dir, want)
	}
	f.noFiles()
	usage, err := f.db.VoiceUsage(context.Background(), "2026-11-20")
	if err != nil || usage.Files != 1 || usage.Bytes != 1000 {
		t.Fatalf("usage = %+v %v", usage, err)
	}
	st := f.status()
	if st.Queue.Done != 1 || st.DoneToday != 1 || st.FilesToday != 1 || st.BytesToday != 1000 || st.LastFailure != "" {
		t.Fatalf("status = %+v", st)
	}
	// The same message queued again by a source is done without any work.
	f.queue(1001, 7, store.VoiceKindVoice, store.VoiceSourceSync, store.VoicePrioritySync)
	f.drain()
	if f.tg.downloadCount() != 1 {
		t.Fatalf("a message with a text was downloaded again")
	}
}

// rawTranscription reads the raw column of a transcription through a second,
// read-only connection: the store does not hand it out.
func (f *voiceFixture) rawTranscription(chat int64, id int) []byte {
	f.t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(f.dir, "store.db")+"?mode=ro")
	if err != nil {
		f.t.Fatal(err)
	}
	defer conn.Close()
	var raw sql.NullString
	if err := conn.QueryRow(`SELECT raw FROM transcriptions WHERE chat_id=? AND message_id=?`, chat, id).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	return []byte(raw.String)
}
