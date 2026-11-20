package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"tg-cli/internal/embed"
	"tg-cli/internal/redact"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/vectors"
)

const (
	// semanticIndexPause is the wait between two embedding batches, so the
	// indexer does not hold the processor or the GPU for the whole run.
	semanticIndexPause = 100 * time.Millisecond
	// semanticOpenAge is the age of the last message after which a fragment is
	// closed: the conversation is over and an edit is worth an embedding.
	semanticOpenAge = 30 * time.Minute
	// semanticOpenGrowth is the growth in characters at which an open fragment is
	// embedded again, however young it is.
	semanticOpenGrowth = 300
	// semanticDefaultInterval is the wait between runs when the setting is zero.
	semanticDefaultInterval = 10 * time.Minute
	// semanticReasonLimit bounds the reason shown in the status.
	semanticReasonLimit = 200

	semanticCancelled = "cancelled"
)

// semanticIndexer is the background worker of the semantic index. One goroutine
// wakes every Interval after the end of the previous run, and also on a start
// command or a trigger, reads the local messages, cuts them into fragments and
// keeps vectors.db in step with them. It reads the message database, writes only
// vectors.db, never calls Telegram, and sends fragment text only to the
// embedder. It does not exist when the feature is off.
type semanticIndexer struct {
	d     *daemon
	cfg   SemanticSearchConfig
	clock historySyncClock
	// pause is the wait between two batches; tests shorten it.
	pause time.Duration
	// root is the context of the daemon run; the goroutine ends with it.
	root context.Context
	done chan struct{}
	wake chan struct{}

	mu sync.Mutex
	// running is true from the moment a run is reserved until it ends.
	running   bool
	runCtx    context.Context
	cancelRun context.CancelFunc
	// pending records a trigger that arrived during a run: the run has already
	// read the messages, so one more run follows it.
	pending bool
	st      rpc.SemanticIndexStatus
}

// newSemanticIndexer builds the worker; start launches it. A nil clock means
// the real one.
func newSemanticIndexer(d *daemon, cfg SemanticSearchConfig, clock historySyncClock) *semanticIndexer {
	if clock == nil {
		clock = realHistorySyncClock{}
	}
	if cfg.Interval <= 0 {
		cfg.Interval = semanticDefaultInterval
	}
	return &semanticIndexer{
		d: d, cfg: cfg, clock: clock, pause: semanticIndexPause,
		done: make(chan struct{}), wake: make(chan struct{}, 1),
		st: rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexIdle},
	}
}

// startSemanticIndex launches the worker when the setting is on and the vector
// file and the embedder are open. Without them there is no goroutine: the
// daemon makes no request to Ollama and the status says why.
func (d *daemon) startSemanticIndex(ctx context.Context, clock historySyncClock) {
	if !d.opts.SemanticSearch.Enabled || d.vectors == nil || d.embedder == nil {
		return
	}
	d.semanticIndex = newSemanticIndexer(d, d.opts.SemanticSearch, clock)
	d.semanticIndex.start(ctx)
}

// triggerSemanticIndex asks for a run after new messages arrived. It is a no-op
// when the indexer does not exist.
func (d *daemon) triggerSemanticIndex() {
	if d.semanticIndex != nil {
		d.semanticIndex.trigger()
	}
}

func (x *semanticIndexer) start(ctx context.Context) {
	x.root = ctx
	go x.loop(ctx)
}

func (x *semanticIndexer) wait() { <-x.done }

func (x *semanticIndexer) loop(ctx context.Context) {
	defer close(x.done)
	for {
		x.scheduleNext()
		timer := x.clock.NewTimer(x.cfg.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C():
		case <-x.wake:
			timer.Stop()
		}
		runCtx := x.reserve()
		// A start command that reserved this run also sent a wake signal; the
		// timer may have won the race, and a stale signal would run twice.
		select {
		case <-x.wake:
		default:
		}
		x.finish(x.pass(runCtx), ctx)
	}
}

func (x *semanticIndexer) scheduleNext() {
	next := x.clock.Now().Add(x.cfg.Interval).Unix()
	x.mu.Lock()
	x.st.NextRunAt = &next
	x.mu.Unlock()
}

// reserve marks a run as started and returns its context. A start command
// reserves the run before the goroutine wakes, so its answer already says
// "indexing"; the goroutine then finds the reservation and uses it.
func (x *semanticIndexer) reserve() context.Context {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.reserveLocked()
}

func (x *semanticIndexer) reserveLocked() context.Context {
	if x.running {
		return x.runCtx
	}
	x.runCtx, x.cancelRun = context.WithCancel(x.root)
	x.running = true
	started := x.clock.Now().Unix()
	x.st.State = rpc.SemanticIndexIndexing
	x.st.Reason = ""
	x.st.Embedded, x.st.Removed = 0, 0
	x.st.StartedAt = &started
	x.st.FinishedAt = nil
	x.st.NextRunAt = nil
	return x.runCtx
}

// trigger asks for a run. During a run it only remembers the request.
func (x *semanticIndexer) trigger() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.running {
		x.pending = true
		return
	}
	select {
	case x.wake <- struct{}{}:
	default:
	}
}

// startNow is the start command: it reserves a run and wakes the goroutine. A
// run in progress is reported unchanged.
func (x *semanticIndexer) startNow() rpc.SemanticIndexStatus {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.running {
		x.reserveLocked()
		select {
		case x.wake <- struct{}{}:
		default:
		}
	}
	return x.statusLocked()
}

// cancelRunNow is the cancel command: the run stops, what it stored stays.
func (x *semanticIndexer) cancelRunNow() rpc.SemanticIndexStatus {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.running {
		x.cancelRun()
	}
	return x.statusLocked()
}

func (x *semanticIndexer) status() rpc.SemanticIndexStatus {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.statusLocked()
}

func (x *semanticIndexer) statusLocked() rpc.SemanticIndexStatus { return x.st }

func (x *semanticIndexer) update(f func(*rpc.SemanticIndexStatus)) {
	x.mu.Lock()
	f(&x.st)
	x.mu.Unlock()
}

// outcome is how a run ended.
type outcome struct {
	state  string
	reason string
}

var idleOutcome = outcome{state: rpc.SemanticIndexIdle}

// finish records the end of a run. A cancelled run is idle again with the reason
// "cancelled": it is not a failure.
func (x *semanticIndexer) finish(out outcome, parent context.Context) {
	now := x.clock.Now().Unix()
	x.mu.Lock()
	x.running = false
	x.cancelRun()
	x.st.State, x.st.Reason = out.state, out.reason
	x.st.FinishedAt = &now
	if x.pending {
		x.pending = false
		select {
		case x.wake <- struct{}{}:
		default:
		}
	}
	st := x.st
	x.mu.Unlock()
	if parent.Err() == nil {
		x.d.log.Info("semantic index run ended", "state", st.State, "reason", st.Reason,
			"total", st.Total, "indexed", st.Indexed, "embedded", st.Embedded, "removed", st.Removed)
	}
}

// fragment is a span that goes into the vector index and the hash of its text.
type fragment struct {
	span vectors.Span
	hash string
}

func (f fragment) key() vectors.Key {
	return vectors.Key{ChatID: f.span.ChatID, FirstID: f.span.FirstID}
}

type messageKey struct{ chat, id int64 }

// fragmentsOf cuts the messages into spans, keeps the ones long enough for the
// index and hashes the text of each.
func fragmentsOf(msgs []store.FragmentMessage) (frags []fragment, text func(vectors.Span) string) {
	chunkInput := make([]vectors.Msg, len(msgs))
	at := make(map[messageKey]int, len(msgs))
	for i, m := range msgs {
		chunkInput[i] = vectors.Msg{ID: m.ID, ChatID: m.ChatID, TopicID: m.TopicID, Date: m.Date, Chars: m.Chars, ReplyTo: m.ReplyTo}
		at[messageKey{m.ChatID, m.ID}] = i
	}
	text = func(s vectors.Span) string {
		texts := make([]string, 0, len(s.IDs))
		for _, id := range s.IDs {
			texts = append(texts, msgs[at[messageKey{s.ChatID, id}]].Text)
		}
		return vectors.FragmentText(texts)
	}
	for _, s := range vectors.Chunk(chunkInput) {
		if s.Indexable() {
			frags = append(frags, fragment{span: s, hash: vectors.TextHash(text(s))})
		}
	}
	return frags, text
}

// pass is one run. It reads, reconciles and embeds; it returns how it ended.
func (x *semanticIndexer) pass(ctx context.Context) outcome {
	d := x.d
	policy := d.policy()
	if policy.Closed() {
		// The access file could not be read, so every chat counts as closed. The
		// index is derived data and the search path filters by chat itself: keep
		// what exists instead of dropping the whole index for a typo.
		return outcome{state: rpc.SemanticIndexError, reason: "the access policy could not be read"}
	}
	msgs, err := d.store.MessagesForFragments(ctx, chatFilter(policy))
	if err != nil {
		return x.failure(ctx, "read messages", err)
	}
	frags, text := fragmentsOf(msgs)
	stored, err := d.vectors.List(ctx)
	if err != nil {
		return x.failure(ctx, "list vectors", err)
	}
	removed, err := x.removeStale(ctx, frags, stored)
	if err != nil {
		return x.failure(ctx, "remove vectors", err)
	}
	x.update(func(s *rpc.SemanticIndexStatus) { s.Total, s.Removed = len(frags), removed })

	info, err := d.embedder.Info(ctx)
	if err != nil {
		return x.failure(ctx, "model info", err)
	}
	byKey := make(map[vectors.Key]vectors.FragmentMeta, len(stored))
	for _, m := range stored {
		byKey[m.Key()] = m
	}
	jobs, indexed := x.plan(frags, byKey, info)
	x.update(func(s *rpc.SemanticIndexStatus) { s.Model, s.ModelDigest, s.Indexed = info.Name, info.Digest, indexed })
	return x.embedAll(ctx, jobs, text, info)
}

// removeStale drops the stored fragments that the new cutting does not have: a
// closed chat, a deleted message, a fragment that stopped being long enough. A
// chat that loses all its fragments goes in one transaction. It returns the
// number of fragments removed.
func (x *semanticIndexer) removeStale(ctx context.Context, frags []fragment, stored []vectors.FragmentMeta) (int, error) {
	keep := make(map[vectors.Key]struct{}, len(frags))
	for _, f := range frags {
		keep[f.key()] = struct{}{}
	}
	perChat := map[int64]int{}
	stale := map[int64][]int64{}
	var order []int64
	for _, m := range stored {
		perChat[m.ChatID]++
		if _, ok := keep[m.Key()]; ok {
			continue
		}
		if _, seen := stale[m.ChatID]; !seen {
			order = append(order, m.ChatID)
		}
		stale[m.ChatID] = append(stale[m.ChatID], m.FirstID)
	}
	removed := 0
	for _, chat := range order {
		firsts := stale[chat]
		if len(firsts) == perChat[chat] {
			if err := x.d.vectors.DeleteChats(ctx, []int64{chat}); err != nil {
				return removed, err
			}
		} else {
			for _, first := range firsts {
				if err := x.d.vectors.Delete(ctx, chat, first); err != nil {
					return removed, err
				}
			}
		}
		removed += len(firsts)
	}
	return removed, nil
}

// embedJob is a fragment to embed. fresh is true when the fragment has no vector
// of the current model yet, so storing one adds to the indexed count.
type embedJob struct {
	frag  fragment
	fresh bool
}

// plan picks the fragments to embed. A new key, another model or digest and a
// changed text all mean "again", with one exception: a fragment that is still
// open keeps its vector until it is closed or has grown enough. It also returns
// how many fragments already have a vector of the current model.
func (x *semanticIndexer) plan(frags []fragment, stored map[vectors.Key]vectors.FragmentMeta, info embed.ModelInfo) ([]embedJob, int) {
	now := x.clock.Now().Unix()
	var jobs []embedJob
	indexed := 0
	for _, f := range frags {
		meta, ok := stored[f.key()]
		switch {
		case !ok || meta.Model != info.Name || meta.ModelDigest != info.Digest:
			jobs = append(jobs, embedJob{frag: f, fresh: true})
		case meta.TextHash == f.hash:
			indexed++
		default:
			indexed++
			closed := now-f.span.EndsAt >= int64(semanticOpenAge/time.Second)
			grown := int64(f.span.Chars)-meta.Chars >= semanticOpenGrowth
			if closed || grown {
				jobs = append(jobs, embedJob{frag: f})
			}
		}
	}
	return jobs, indexed
}

// embedAll embeds the jobs in batches and stores each batch before the next one,
// so a failure keeps what is done and the next run goes on from there.
func (x *semanticIndexer) embedAll(ctx context.Context, jobs []embedJob, text func(vectors.Span) string, info embed.ModelInfo) outcome {
	d := x.d
	batch := x.cfg.Batch
	if batch < 1 {
		batch = embed.DefaultBatch
	}
	for from := 0; from < len(jobs); from += batch {
		if ctx.Err() != nil {
			return outcome{state: rpc.SemanticIndexIdle, reason: semanticCancelled}
		}
		chunk := jobs[from:min(from+batch, len(jobs))]
		texts := make([]string, len(chunk))
		for i, j := range chunk {
			texts[i] = text(j.frag.span)
		}
		vecs, err := d.embedder.Embed(ctx, texts)
		if err != nil {
			return x.failure(ctx, "embed", err)
		}
		if len(vecs) != len(chunk) {
			return outcome{state: rpc.SemanticIndexError, reason: "embed: the embedder returned a wrong number of vectors"}
		}
		// A chat closed while the batch was embedding must not get a vector.
		policy := d.policy()
		put := make([]vectors.Fragment, 0, len(chunk))
		fresh := 0
		for i, j := range chunk {
			s := j.frag.span
			if !policy.Allowed(s.ChatID) {
				continue
			}
			put = append(put, vectors.Fragment{
				FragmentMeta: vectors.FragmentMeta{
					ChatID: s.ChatID, TopicID: s.TopicID, FirstID: s.FirstID, LastID: s.LastID,
					StartsAt: s.StartsAt, EndsAt: s.EndsAt, Chars: int64(s.Chars), TextHash: j.frag.hash,
					Model: info.Name, ModelDigest: info.Digest, EmbeddedAt: x.clock.Now().Unix(),
				},
				Vec: vecs[i],
			})
			if j.fresh {
				fresh++
			}
		}
		if err := d.vectors.Put(ctx, put); err != nil {
			return x.failure(ctx, "store vectors", err)
		}
		x.update(func(s *rpc.SemanticIndexStatus) { s.Embedded += len(put); s.Indexed += fresh })
		if from+batch < len(jobs) && !x.sleep(ctx, x.pause) {
			return outcome{state: rpc.SemanticIndexIdle, reason: semanticCancelled}
		}
	}
	return idleOutcome
}

// sleep waits on the clock between two batches. It reports false when ctx ended.
func (x *semanticIndexer) sleep(ctx context.Context, d time.Duration) bool {
	timer := x.clock.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C():
		return true
	}
}

// failure turns an error into the end state of a run. The reason never carries
// message text: the embed package copies none into its errors, and the other
// errors come from the database and the file system.
func (x *semanticIndexer) failure(ctx context.Context, what string, err error) outcome {
	switch {
	case ctx.Err() != nil:
		return outcome{state: rpc.SemanticIndexIdle, reason: semanticCancelled}
	case errors.Is(err, embed.ErrUnavailable):
		return outcome{state: rpc.SemanticIndexUnavailable, reason: "Ollama is not reachable at " + x.cfg.OllamaURL}
	case errors.Is(err, embed.ErrModelMissing):
		return outcome{state: rpc.SemanticIndexModelMissing, reason: "Ollama does not have the model " + x.cfg.Model}
	}
	reason := what + ": " + redact.Text(err.Error())
	if len(reason) > semanticReasonLimit {
		reason = reason[:semanticReasonLimit]
	}
	return outcome{state: rpc.SemanticIndexError, reason: reason}
}

// semanticIndexStatus is the status of the indexer; it exists also when the
// feature is off, and then it touches neither Ollama nor the file.
func (d *daemon) semanticIndexStatus() rpc.SemanticIndexStatus {
	switch {
	case !d.opts.SemanticSearch.Enabled:
		return rpc.SemanticIndexStatus{State: rpc.SemanticIndexDisabled}
	case d.semanticIndex == nil:
		reason := d.semanticFailure
		if reason == "" {
			reason = "the indexer is not running"
		}
		return rpc.SemanticIndexStatus{Enabled: true, State: rpc.SemanticIndexError, Reason: reason}
	}
	return d.semanticIndex.status()
}

// errSemanticOff is the answer of a command that needs the feature while it is off.
func errSemanticOff() *rpc.Error {
	return rpc.NewError(rpc.CodeInvalidParams, "semantic search is off: set [semantic_search] enabled = true")
}

// requireSemanticIndexer returns the worker or the error that explains why
// there is none.
func (d *daemon) requireSemanticIndexer() (*semanticIndexer, error) {
	if !d.opts.SemanticSearch.Enabled {
		return nil, errSemanticOff()
	}
	if d.semanticIndex == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "semantic index is not running: "+d.semanticFailure)
	}
	return d.semanticIndex, nil
}

func (d *daemon) searchIndexStart(_ context.Context, params json.RawMessage) (any, error) {
	var p rpc.SearchIndexParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	x, err := d.requireSemanticIndexer()
	if err != nil {
		return nil, err
	}
	return x.startNow(), nil
}

func (d *daemon) searchIndexStatus(_ context.Context, params json.RawMessage) (any, error) {
	var p rpc.SearchIndexParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	return d.semanticIndexStatus(), nil
}

func (d *daemon) searchIndexCancel(_ context.Context, params json.RawMessage) (any, error) {
	var p rpc.SearchIndexParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	x, err := d.requireSemanticIndexer()
	if err != nil {
		return nil, err
	}
	return x.cancelRunNow(), nil
}
