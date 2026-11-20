package daemon

import (
	"context"
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
	"tg-cli/internal/embed"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/store/storetest"
	"tg-cli/internal/vectors"
)

// semanticT0 is "now" of the indexer tests.
var semanticT0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// semanticMarker starts every synthetic message; the no-copy test looks for it
// in the vector file.
const semanticMarker = "Сообщение номер"

// fakeEmbedder stands in for Ollama: it counts the calls, can fail, and can
// block until the context ends.
type fakeEmbedder struct {
	mu          sync.Mutex
	info        embed.ModelInfo
	infoErr     error
	embedErr    error
	failOnCall  int // fail the Nth Embed call (1-based) with embedErr; 0 means every call
	infoCalls   int
	embedCalls  int
	textsSeen   int
	lastTexts   []string
	block       bool
	entered     chan struct{}
	beforeEmbed func()
}

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{info: embed.ModelInfo{Name: "bge-m3:latest", Digest: "digest-1", Dim: 4}, entered: make(chan struct{}, 16)}
}

func (e *fakeEmbedder) Info(context.Context) (embed.ModelInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.infoCalls++
	return e.info, e.infoErr
}

func (e *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.embedCalls++
	call := e.embedCalls
	block, hook, err, failOn := e.block, e.beforeEmbed, e.embedErr, e.failOnCall
	e.mu.Unlock()
	e.entered <- struct{}{}
	if hook != nil {
		hook()
	}
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err != nil && (failOn == 0 || failOn == call) {
		return nil, err
	}
	e.mu.Lock()
	e.textsSeen += len(texts)
	e.lastTexts = append([]string(nil), texts...)
	e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = []float32{1, float32(len(t)%7 + 1), 0, 0}
	}
	return out, nil
}

func (e *fakeEmbedder) counts() (info, embedCalls, texts int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.infoCalls, e.embedCalls, e.textsSeen
}

func (e *fakeEmbedder) set(f func(*fakeEmbedder)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f(e)
}

type semanticFixture struct {
	t          *testing.T
	d          *daemon
	db         *store.DB
	vec        *vectors.Store
	emb        *fakeEmbedder
	clock      *schedClock
	x          *semanticIndexer
	cfg        SemanticSearchConfig
	dir        string
	accessPath string
	root       context.Context
}

func newSemanticFixture(t *testing.T) *semanticFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := storetest.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vec, err := vectors.Open(filepath.Join(dir, vectorsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vec.Close() })
	emb := newFakeEmbedder()
	clock := newSchedClock(semanticT0)
	cfg := SemanticSearchConfig{Enabled: true, OllamaURL: "http://127.0.0.1:11434", Model: "bge-m3", Batch: 16, Interval: 10 * time.Minute}
	accessPath := filepath.Join(dir, "access.json")
	d := &daemon{
		opts:  Options{Access: access.Open(accessPath), Paths: Paths{Dir: dir, DatabaseDir: dir}, SemanticSearch: cfg},
		store: db, vectors: vec, embedder: emb, log: discardLogger(),
	}
	root, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	x := newSemanticIndexer(d, cfg, clock)
	x.root = root
	x.pause = 0
	d.semanticIndex = x
	f := &semanticFixture{t: t, d: d, db: db, vec: vec, emb: emb, clock: clock, x: x, cfg: cfg, dir: dir, accessPath: accessPath, root: root}
	if err := db.UpsertChats(context.Background(), []store.Chat{{ID: 1, Type: "group", Title: "One"}, {ID: 2, Type: "group", Title: "Two"}}); err != nil {
		t.Fatal(err)
	}
	return f
}

// messageText is a synthetic message of exactly 120 characters.
func messageText(id int, tag string) string {
	s := fmt.Sprintf("%s %d %s ", semanticMarker, id, tag) + strings.Repeat("слово ", 30)
	return string([]rune(s)[:120])
}

// seed stores n messages of 120 characters, a minute apart, from start. Three
// of them (360 characters) are one fragment.
func (f *semanticFixture) seed(chat int64, firstID int, start time.Time, n int) {
	f.t.Helper()
	msgs := make([]store.Message, 0, n)
	for i := range n {
		id := firstID + i
		msgs = append(msgs, store.Message{ChatID: chat, ID: id, Date: start.Add(time.Duration(i) * time.Minute).Unix(), Text: messageText(id, ""), Raw: []byte(`{}`)})
	}
	if err := f.db.UpsertMessages(context.Background(), msgs); err != nil {
		f.t.Fatal(err)
	}
}

func (f *semanticFixture) edit(chat int64, id int, date time.Time, text string) {
	f.t.Helper()
	if err := f.db.UpsertMessages(context.Background(), []store.Message{{ChatID: chat, ID: id, Date: date.Unix(), Text: text, Raw: []byte(`{}`)}}); err != nil {
		f.t.Fatal(err)
	}
}

// run is one synchronous run without the goroutine.
func (f *semanticFixture) run() rpc.SemanticIndexStatus {
	f.t.Helper()
	ctx := f.x.reserve()
	f.x.finish(f.x.pass(ctx), f.root)
	return f.x.status()
}

func (f *semanticFixture) list() []vectors.FragmentMeta {
	f.t.Helper()
	got, err := f.vec.List(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *semanticFixture) writeAccess(data string) {
	f.t.Helper()
	if err := os.WriteFile(f.accessPath, []byte(data), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// seedTwoConversations stores two fragments in chat 1 (three messages each,
// two hours apart, both closed at semanticT0) and one short message in chat 2.
func (f *semanticFixture) seedTwoConversations() {
	f.seed(1, 1, semanticT0.Add(-5*time.Hour), 3)
	f.seed(1, 11, semanticT0.Add(-3*time.Hour), 3)
	if err := f.db.UpsertMessages(context.Background(), []store.Message{{ChatID: 2, ID: 1, Date: semanticT0.Add(-time.Hour).Unix(), Text: "коротко", Raw: []byte(`{}`)}}); err != nil {
		f.t.Fatal(err)
	}
}

func TestSemanticIndexFullPassBuildsFragmentsAndWritesVectors(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	st := f.run()
	if st.State != rpc.SemanticIndexIdle || st.Reason != "" {
		t.Fatalf("state %q reason %q", st.State, st.Reason)
	}
	if st.Total != 2 || st.Indexed != 2 || st.Embedded != 2 || st.Removed != 0 {
		t.Errorf("counters total %d indexed %d embedded %d removed %d, want 2 2 2 0", st.Total, st.Indexed, st.Embedded, st.Removed)
	}
	if st.Model != "bge-m3:latest" || st.ModelDigest != "digest-1" {
		t.Errorf("model %q digest %q", st.Model, st.ModelDigest)
	}
	if st.StartedAt == nil || st.FinishedAt == nil || *st.StartedAt != semanticT0.Unix() {
		t.Errorf("times not recorded: %+v", st)
	}
	got := f.list()
	if len(got) != 2 {
		t.Fatalf("stored %d fragments, want 2: %+v", len(got), got)
	}
	first := got[0]
	if first.ChatID != 1 || first.FirstID != 1 || first.LastID != 3 || first.Dim != 4 || first.Model != "bge-m3:latest" || first.ModelDigest != "digest-1" {
		t.Errorf("first fragment %+v", first)
	}
	// 3 messages of 120 characters: counted in runes, not in bytes (Cyrillic is
	// two bytes a letter).
	if first.Chars != 360 {
		t.Errorf("Chars = %d, want 360", first.Chars)
	}
	text := vectors.FragmentText([]string{messageText(1, ""), messageText(2, ""), messageText(3, "")})
	if first.TextHash != vectors.TextHash(text) {
		t.Errorf("text hash does not match the joined message texts")
	}
	if first.EmbeddedAt != semanticT0.Unix() {
		t.Errorf("EmbeddedAt = %d, want %d", first.EmbeddedAt, semanticT0.Unix())
	}
}

func TestSemanticIndexDoesNotCopyMessageTextIntoVectorsDB(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.run()
	if err := f.vec.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(filepath.Join(f.dir, vectorsFileName+"*"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no vector files: %v %v", entries, err)
	}
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), semanticMarker) || strings.Contains(string(data), "слово") {
			t.Errorf("%s holds message text", filepath.Base(path))
		}
	}
}

func TestSemanticIndexSecondRunWithoutChangesEmbedsNothing(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.run()
	_, calls, _ := f.emb.counts()
	if calls == 0 {
		t.Fatal("the first run embedded nothing")
	}
	st := f.run()
	if _, after, _ := f.emb.counts(); after != calls {
		t.Errorf("the second run called Embed %d more times, want 0", after-calls)
	}
	if st.Embedded != 0 || st.Removed != 0 || st.Total != 2 || st.Indexed != 2 {
		t.Errorf("second run counters %+v", st)
	}
}

func TestSemanticIndexEditOfAClosedFragmentRecomputesOnlyIt(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.run()
	before := map[int64]int64{}
	for _, m := range f.list() {
		before[m.FirstID] = m.EmbeddedAt
	}
	f.clock.AdvanceTo(semanticT0.Add(time.Hour))
	f.edit(1, 12, semanticT0.Add(-3*time.Hour+time.Minute), messageText(12, "edited"))
	st := f.run()
	if st.Embedded != 1 || st.Indexed != 2 {
		t.Fatalf("embedded %d indexed %d, want 1 and 2", st.Embedded, st.Indexed)
	}
	for _, m := range f.list() {
		changed := m.EmbeddedAt != before[m.FirstID]
		if changed != (m.FirstID == 11) {
			t.Errorf("fragment %d: recomputed=%v", m.FirstID, changed)
		}
	}
	if _, _, texts := f.emb.counts(); texts != 3 {
		t.Errorf("embedder saw %d texts in total, want 3 (2 + 1)", texts)
	}
}

func TestSemanticIndexOpenFragmentWaitsForClosingOrGrowth(t *testing.T) {
	f := newSemanticFixture(t)
	// The last message is 10 minutes old: the fragment is open.
	f.seed(1, 1, semanticT0.Add(-12*time.Minute), 3)
	f.run()
	_, calls, _ := f.emb.counts()

	// One more message of 120 characters: growth below 300 and still open, so
	// the stored vector stays.
	f.seed(1, 4, semanticT0.Add(-9*time.Minute), 1)
	st := f.run()
	if _, after, _ := f.emb.counts(); after != calls || st.Embedded != 0 {
		t.Fatalf("an open fragment that grew by 120 characters was embedded again (embedded %d)", st.Embedded)
	}

	// Growth of 300 characters and more recomputes it even though it is open.
	f.seed(1, 5, semanticT0.Add(-8*time.Minute), 2)
	st = f.run()
	if st.Embedded != 1 {
		t.Fatalf("growth of 360 characters: embedded %d, want 1", st.Embedded)
	}
	_, calls, _ = f.emb.counts()

	// A small edit of an open fragment waits...
	f.edit(1, 2, semanticT0.Add(-11*time.Minute), messageText(2, "edit"))
	if st = f.run(); st.Embedded != 0 {
		t.Fatalf("an edit of an open fragment was embedded at once")
	}
	// ...until 30 minutes pass since its last message.
	f.clock.AdvanceTo(semanticT0.Add(25 * time.Minute))
	if st = f.run(); st.Embedded != 1 {
		t.Fatalf("a closed fragment with a changed text: embedded %d, want 1", st.Embedded)
	}
	if _, after, _ := f.emb.counts(); after <= calls {
		t.Errorf("Embed was not called for the closed fragment")
	}
}

func TestSemanticIndexDeletedMessageChangesOrRemovesTheFragment(t *testing.T) {
	f := newSemanticFixture(t)
	f.seed(1, 1, semanticT0.Add(-5*time.Hour), 4)
	f.run()
	if got := f.list(); len(got) != 1 || got[0].Chars != 480 {
		t.Fatalf("setup: %+v", got)
	}
	if _, err := f.db.MarkMessagesDeleted(context.Background(), 1, []int{4}); err != nil {
		t.Fatal(err)
	}
	st := f.run()
	got := f.list()
	if st.Embedded != 1 || len(got) != 1 || got[0].Chars != 360 || got[0].LastID != 3 {
		t.Fatalf("after one deletion: embedded %d, stored %+v", st.Embedded, got)
	}
	// Two more deletions leave 120 characters: too short for the index.
	if _, err := f.db.MarkMessagesDeleted(context.Background(), 1, []int{2, 3}); err != nil {
		t.Fatal(err)
	}
	st = f.run()
	if st.Removed != 1 || st.Total != 0 || len(f.list()) != 0 {
		t.Fatalf("a fragment that stopped being indexable stays: removed %d total %d stored %d", st.Removed, st.Total, len(f.list()))
	}
}

func TestSemanticIndexRemovesAClosedChatAndSkipsIt(t *testing.T) {
	f := newSemanticFixture(t)
	f.seed(1, 1, semanticT0.Add(-5*time.Hour), 3)
	f.seed(2, 1, semanticT0.Add(-5*time.Hour), 3)
	if st := f.run(); st.Indexed != 2 {
		t.Fatalf("setup: indexed %d", st.Indexed)
	}
	_, _, texts := f.emb.counts()
	f.writeAccess(`{"block":[1]}`)
	st := f.run()
	got := f.list()
	if len(got) != 1 || got[0].ChatID != 2 {
		t.Fatalf("stored after closing chat 1: %+v", got)
	}
	if st.Removed != 1 || st.Total != 1 || st.Embedded != 0 {
		t.Errorf("removed %d total %d embedded %d, want 1 1 0", st.Removed, st.Total, st.Embedded)
	}
	if _, _, after := f.emb.counts(); after != texts {
		t.Errorf("the closed chat's text was sent to the embedder")
	}
	// A new message in the closed chat is not indexed either.
	f.seed(1, 20, semanticT0.Add(-2*time.Hour), 3)
	f.run()
	for _, m := range f.list() {
		if m.ChatID == 1 {
			t.Errorf("a fragment of a closed chat was stored: %+v", m)
		}
	}
}

func TestSemanticIndexKeepsTheIndexWhenTheAccessFileIsUnreadable(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.run()
	f.writeAccess(`{not json`)
	st := f.run()
	if st.State != rpc.SemanticIndexError || len(f.list()) != 2 {
		t.Fatalf("state %q, stored %d: an unreadable access file must keep the index and report an error", st.State, len(f.list()))
	}
}

func TestSemanticIndexChatClosedDuringABatchGetsNoVector(t *testing.T) {
	f := newSemanticFixture(t)
	f.seed(1, 1, semanticT0.Add(-5*time.Hour), 3)
	f.seed(2, 1, semanticT0.Add(-5*time.Hour), 3)
	f.emb.set(func(e *fakeEmbedder) { e.beforeEmbed = func() { f.writeAccess(`{"block":[2]}`) } })
	f.run()
	for _, m := range f.list() {
		if m.ChatID == 2 {
			t.Errorf("a chat closed while its batch was embedding got a vector")
		}
	}
	if len(f.list()) != 1 {
		t.Errorf("stored %d fragments, want chat 1 only", len(f.list()))
	}
}

func TestSemanticIndexChangedModelDigestRecomputesEverything(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.run()
	f.emb.set(func(e *fakeEmbedder) { e.info.Digest = "digest-2" })
	st := f.run()
	if st.Embedded != 2 || st.Indexed != 2 || st.ModelDigest != "digest-2" {
		t.Fatalf("after a digest change: %+v", st)
	}
	ctx := context.Background()
	if n, _ := f.vec.Count(ctx, "bge-m3:latest", "digest-2"); n != 2 {
		t.Errorf("rows of the new digest: %d, want 2", n)
	}
	if n, _ := f.vec.Count(ctx, "bge-m3:latest", "digest-1"); n != 0 {
		t.Errorf("rows of the old digest: %d, want 0", n)
	}
}

func TestSemanticIndexOllamaFailuresBecomeStatusAndSearchStillWorks(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		state string
	}{
		{"unreachable", fmt.Errorf("%w: connection refused", embed.ErrUnavailable), rpc.SemanticIndexUnavailable},
		{"no model", fmt.Errorf("%w (HTTP 404)", embed.ErrModelMissing), rpc.SemanticIndexModelMissing},
		{"other", errors.New("boom"), rpc.SemanticIndexError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSemanticFixture(t)
			f.seedTwoConversations()
			f.emb.set(func(e *fakeEmbedder) { e.infoErr = tc.err })
			st := f.run()
			if st.State != tc.state || st.Reason == "" {
				t.Fatalf("state %q reason %q, want %q with a reason", st.State, st.Reason, tc.state)
			}
			if strings.Contains(st.Reason, semanticMarker) {
				t.Errorf("the reason carries message text: %q", st.Reason)
			}
			if len(f.list()) != 0 {
				t.Errorf("vectors stored although the model is not available")
			}
			res, err := f.d.search(context.Background(), params(t, rpc.SearchParams{Query: "слово", Scopes: []string{"messages"}}))
			if err != nil {
				t.Fatal(err)
			}
			if hits := res.(rpc.SearchResult).Hits; len(hits) == 0 {
				t.Errorf("word search returned nothing after an Ollama failure")
			}
			// The next run, with Ollama back, completes the index.
			f.emb.set(func(e *fakeEmbedder) { e.infoErr = nil })
			if st = f.run(); st.State != rpc.SemanticIndexIdle || st.Indexed != 2 {
				t.Errorf("recovery run: %+v", st)
			}
		})
	}
}

func TestSemanticIndexFailedBatchKeepsStoredBatchesAndNextRunContinues(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.x.cfg.Batch = 1
	f.emb.set(func(e *fakeEmbedder) { e.embedErr = fmt.Errorf("%w: gone", embed.ErrUnavailable); e.failOnCall = 2 })
	st := f.run()
	if st.State != rpc.SemanticIndexUnavailable || st.Embedded != 1 || len(f.list()) != 1 {
		t.Fatalf("after a failure in the second batch: state %q embedded %d stored %d", st.State, st.Embedded, len(f.list()))
	}
	_, _, seen := f.emb.counts()
	f.emb.set(func(e *fakeEmbedder) { e.embedErr = nil })
	st = f.run()
	if st.State != rpc.SemanticIndexIdle || st.Embedded != 1 || st.Indexed != 2 || len(f.list()) != 2 {
		t.Fatalf("continuation: %+v stored %d", st, len(f.list()))
	}
	if _, _, after := f.emb.counts(); after-seen != 1 {
		t.Errorf("the continuation embedded %d texts, want only the missing one", after-seen)
	}
}

func TestSemanticIndexStartRunsAtOnceAndCancelStopsIt(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.emb.set(func(e *fakeEmbedder) { e.block = true })
	f.x.start(f.root)
	awaitCond(t, "the first timer", func() bool { return f.clock.hasTimerAt(semanticT0.Add(f.cfg.Interval)) })
	if st := f.x.status(); st.State != rpc.SemanticIndexIdle || st.NextRunAt == nil || *st.NextRunAt != semanticT0.Add(f.cfg.Interval).Unix() {
		t.Fatalf("before the first run: %+v", st)
	}
	st := f.x.startNow()
	if st.State != rpc.SemanticIndexIndexing {
		t.Fatalf("start answered %q, want indexing", st.State)
	}
	<-f.emb.entered
	// A second start on a run in progress changes nothing.
	if again := f.x.startNow(); again.State != rpc.SemanticIndexIndexing || *again.StartedAt != *st.StartedAt {
		t.Errorf("a second start: %+v", again)
	}
	f.x.cancelRunNow()
	awaitCond(t, "the cancelled run to end", func() bool { return f.x.status().State == rpc.SemanticIndexIdle })
	done := f.x.status()
	if done.Reason != semanticCancelled || done.FinishedAt == nil || len(f.list()) != 0 {
		t.Errorf("after cancel: %+v stored %d", done, len(f.list()))
	}
	// The worker survives a cancelled run and takes the next start.
	f.emb.set(func(e *fakeEmbedder) { e.block = false })
	f.x.startNow()
	awaitCond(t, "the second run", func() bool { s := f.x.status(); return s.State == rpc.SemanticIndexIdle && s.Indexed == 2 })
}

func TestSemanticIndexRunsEveryIntervalFromTheEndOfARun(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.x.start(f.root)
	first := semanticT0.Add(f.cfg.Interval)
	awaitCond(t, "the first timer", func() bool { return f.clock.hasTimerAt(first) })
	if info, _, _ := f.emb.counts(); info != 0 {
		t.Fatalf("Ollama was asked %d times before the first interval", info)
	}
	f.clock.AdvanceTo(first)
	awaitCond(t, "the first run", func() bool { s := f.x.status(); return s.FinishedAt != nil && s.Indexed == 2 })
	// The next run is one interval after the end of this one.
	awaitCond(t, "the second timer", func() bool { return f.clock.hasTimerAt(first.Add(f.cfg.Interval)) })
}

func TestSemanticIndexTriggerStartsARunWithoutWaiting(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.x.start(f.root)
	awaitCond(t, "the first timer", func() bool { return f.clock.hasTimerAt(semanticT0.Add(f.cfg.Interval)) })
	f.d.triggerSemanticIndex()
	awaitCond(t, "the triggered run", func() bool { s := f.x.status(); return s.FinishedAt != nil && s.Indexed == 2 })
}

func TestSemanticIndexTriggerDuringARunQueuesOneMoreRun(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.emb.set(func(e *fakeEmbedder) { e.block = true })
	f.x.start(f.root)
	awaitCond(t, "the first timer", func() bool { return f.clock.hasTimerAt(semanticT0.Add(f.cfg.Interval)) })
	f.x.startNow()
	<-f.emb.entered
	f.d.triggerSemanticIndex() // new messages arrived while the run reads
	f.emb.set(func(e *fakeEmbedder) { e.block = false })
	f.x.cancelRunNow()
	// The cancelled run ends, the queued trigger runs the pass again.
	awaitCond(t, "the follow-up run", func() bool { s := f.x.status(); return s.State == rpc.SemanticIndexIdle && s.Indexed == 2 })
}

func TestSemanticIndexStopJoinsTheGoroutineEvenInsideARun(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedTwoConversations()
	f.emb.set(func(e *fakeEmbedder) { e.block = true })
	ctx, cancel := context.WithCancel(context.Background())
	f.x.start(ctx)
	awaitCond(t, "the first timer", func() bool { return f.clock.hasTimerAt(semanticT0.Add(f.cfg.Interval)) })
	f.x.startNow()
	<-f.emb.entered
	cancel()
	joined := make(chan struct{})
	go func() { f.x.wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("the indexer goroutine did not end after the context was cancelled")
	}
}

// With the setting off the daemon starts no goroutine, writes no file and never
// asks the embedder, whatever triggers arrive.
func TestSemanticIndexOffDoesNothing(t *testing.T) {
	dir := t.TempDir()
	db, err := storetest.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	emb := newFakeEmbedder()
	d := &daemon{opts: Options{Paths: Paths{DatabaseDir: dir}}, store: db, embedder: emb, log: discardLogger()}
	d.startSemanticIndex(context.Background(), newSchedClock(semanticT0))
	if d.semanticIndex != nil {
		t.Fatal("an indexer exists although the setting is off")
	}
	d.triggerSemanticIndex()
	if st := d.semanticIndexStatus(); st.Enabled || st.State != rpc.SemanticIndexDisabled || st.Reason != "" {
		t.Errorf("status %+v, want disabled", st)
	}
	for _, call := range []func(context.Context, []byte) (any, error){
		func(ctx context.Context, p []byte) (any, error) { return d.searchIndexStart(ctx, p) },
		func(ctx context.Context, p []byte) (any, error) { return d.searchIndexCancel(ctx, p) },
	} {
		_, err := call(context.Background(), nil)
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams || re.Message != "semantic search is off: set [semantic_search] enabled = true" {
			t.Errorf("a command with the feature off answered %v", err)
		}
	}
	res, err := d.searchIndexStatus(context.Background(), nil)
	if err != nil || res.(rpc.SemanticIndexStatus).State != rpc.SemanticIndexDisabled {
		t.Errorf("status method: %v %v", res, err)
	}
	if info, calls, _ := emb.counts(); info != 0 || calls != 0 {
		t.Errorf("the embedder was used: info %d, embed %d", info, calls)
	}
	if _, err := os.Stat(filepath.Join(dir, vectorsFileName)); !os.IsNotExist(err) {
		t.Errorf("vectors.db exists with the feature off: %v", err)
	}
}

func TestSemanticIndexStatusExplainsAnOpenFailure(t *testing.T) {
	d := &daemon{opts: Options{SemanticSearch: SemanticSearchConfig{Enabled: true}}, semanticFailure: "vectors file: boom", log: discardLogger()}
	d.startSemanticIndex(context.Background(), nil)
	if d.semanticIndex != nil {
		t.Fatal("an indexer started without the vector file")
	}
	st := d.semanticIndexStatus()
	if !st.Enabled || st.State != rpc.SemanticIndexError || st.Reason != "vectors file: boom" {
		t.Errorf("status %+v", st)
	}
	if _, err := d.searchIndexStart(context.Background(), nil); err == nil {
		t.Error("start succeeded without an indexer")
	}
}

func TestSemanticIndexHistoryLoadDoneTriggersARun(t *testing.T) {
	f := newSemanticFixture(t)
	f.d.sync = newHistorySyncManager(context.Background(), f.d, newSyncTestClock())
	t.Cleanup(f.d.sync.close)
	if _, err := f.d.sync.start(1, chatref.Ref{Kind: chatref.ID, ID: 1}, store.SyncProgress{Complete: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.x.wake) != 1 {
		t.Errorf("a finished history load sent %d trigger signals, want 1", len(f.x.wake))
	}
}

func TestSemanticIndexBackgroundSyncRunTriggersExceptWhenSkipped(t *testing.T) {
	f := newSemanticFixture(t)
	bg := newBackgroundSync(f.d, BackgroundSyncConfig{}, newSchedClock(semanticT0), nil)
	f.d.background = bg
	bg.finish(rpc.BackgroundSyncSkipped)
	if len(f.x.wake) != 0 {
		t.Fatal("a skipped run triggered the indexer")
	}
	bg.finish(rpc.BackgroundSyncDone)
	if len(f.x.wake) != 1 {
		t.Errorf("a finished run sent %d trigger signals, want 1", len(f.x.wake))
	}
}
