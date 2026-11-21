package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/config"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// VoiceTranscriptionConfig holds the settings of the local voice transcription.
// The caller checks them (see config.VoiceTranscription); the worker trusts
// them. Without Enabled the daemon queues no job by itself and makes no new
// Telegram request for transcription; only a manual command starts the worker.
type VoiceTranscriptionConfig struct {
	// Enabled turns the sources and the worker on.
	Enabled bool
	// Home is the base directory of the profile data; the engine lives in
	// <Home>/models/gigaam.
	Home string
	// FFmpeg is the program that converts media to WAV.
	FFmpeg string
	// MaxVoiceMB, MaxVideoNoteMB and MaxVideoMB are the largest files, in
	// mebibytes, the worker downloads; MaxVideoMinutes is the longest video.
	MaxVoiceMB      int
	MaxVideoNoteMB  int
	MaxVideoMB      int
	MaxVideoMinutes int
	// DailyDownloadMB and DailyFiles are the budget of one local day.
	DailyDownloadMB int
	DailyFiles      int
	// Pause is the wait between two downloads.
	Pause time.Duration
	// Threads is the number of threads of the speech engine.
	Threads int
}

// withDefaults fills the fields a hand-built value leaves at zero with the
// defaults of the configuration. A real value comes checked and complete.
func (c VoiceTranscriptionConfig) withDefaults() VoiceTranscriptionConfig {
	if c.FFmpeg == "" {
		c.FFmpeg = "ffmpeg"
	}
	for _, f := range []struct {
		value *int
		def   int
	}{
		{&c.MaxVoiceMB, 30}, {&c.MaxVideoNoteMB, 50}, {&c.MaxVideoMB, 500}, {&c.MaxVideoMinutes, 120},
		{&c.DailyDownloadMB, config.DefaultVoiceDailyDownloadMB}, {&c.DailyFiles, config.DefaultVoiceDailyFiles}, {&c.Threads, 4},
	} {
		if *f.value <= 0 {
			*f.value = f.def
		}
	}
	return c
}

// VoiceDownload says where and how much one voice download may write.
type VoiceDownload struct {
	// Dir is the private working directory (an absolute path).
	Dir string
	// MaxBytes is the largest file accepted.
	MaxBytes int64
	// ExpectedSize is the size known from the message metadata, or zero.
	ExpectedSize int64
}

// VoiceFile is a downloaded file. The worker removes it.
type VoiceFile struct {
	Path string
	Size int64
}

// Errors a VoiceDownloader returns for the two limits the worker acts on.
var (
	// ErrVoiceTooLarge: the file is larger than the limit.
	ErrVoiceTooLarge = errors.New("file exceeds the size limit")
	// ErrVoiceSizeMismatch: the downloaded size is not the size Telegram reported.
	ErrVoiceSizeMismatch = errors.New("file size does not match")
)

// VoiceDownloader downloads the media of one message into a private working
// directory. The Telegram client implements it; main adapts the real one and a
// test replaces it. A download that fails leaves no file behind.
type VoiceDownloader interface {
	DownloadVoiceFile(ctx context.Context, ref chatref.Ref, messageID int, req VoiceDownload) (VoiceFile, error)
	// CleanVoiceTempDir removes the content of the working directory and keeps
	// the directory; a missing directory is not an error.
	CleanVoiceTempDir(dir string) error
}

// VoiceSegment is one phrase the engine recognised. The text is untrusted data.
type VoiceSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// VoiceEngine is what the worker needs from ffmpeg and the speech engine. The
// real one is in voice_engine.go; a test replaces it.
type VoiceEngine interface {
	// Missing lists what is not installed: "ffmpeg", "runtime", "model" or
	// "vad". An empty list means the engine is ready. It runs no program.
	Missing(ffmpeg string) []string
	// Convert turns a media file into a 16 kHz mono WAV.
	Convert(ctx context.Context, ffmpeg, in, out string, timeout time.Duration) error
	// Transcribe recognises the speech of a WAV file.
	Transcribe(ctx context.Context, wav string, threads int, timeout time.Duration) ([]VoiceSegment, error)
	// WavSeconds is the length of a WAV file in seconds.
	WavSeconds(path string) (float64, error)
	// CheckDisk fails when the free space of dir is below twice need.
	CheckDisk(dir string, need int64) error
	// Timeout is the time a program may take for audioSeconds of sound.
	Timeout(audioSeconds float64) time.Duration
}

const (
	// voiceTempDirName is the working directory of the worker, inside the
	// database directory of the profile.
	voiceTempDirName = "tmp-voice"
	// voiceEngineName is the engine name stored with every local text.
	voiceEngineName = "gigaam-v3"
	// voiceIdleInterval is how long an idle worker sleeps before it looks at the
	// queue again, when no signal wakes it earlier.
	voiceIdleInterval = time.Minute
	// voiceBytesPerWavSecond is the size of one second of 16 kHz mono PCM16.
	voiceBytesPerWavSecond = 32000
	// voiceReasonLimit bounds a reason kept in memory.
	voiceReasonLimit = 64
)

// voiceRetryDelays are the pauses before the retries of a download that failed
// on the network: three retries, then the job fails.
var voiceRetryDelays = [...]time.Duration{10 * time.Second, 60 * time.Second, 300 * time.Second}

var errVoiceDisabled = errors.New("voice transcription is off")

// voiceRun is the job in progress, for the status and for cancel.
type voiceRun struct {
	job       store.VoiceJob
	cancel    context.CancelFunc
	cancelled bool
	stage     string // guarded by the worker mutex
}

// voiceWorker is the single worker of the voice queue. One goroutine takes the
// queued jobs one at a time. It exists for every daemon, but its goroutine runs
// only when voice_transcription.enabled is on, or after the first manual job.
type voiceWorker struct {
	d      *daemon
	cfg    VoiceTranscriptionConfig
	engine VoiceEngine
	clock  historySyncClock
	// dir is the private working directory of the downloads.
	dir string
	// root is the context of the daemon run; the goroutine ends with it.
	root context.Context
	done chan struct{}
	wake chan struct{}

	startOnce sync.Once

	mu sync.Mutex
	// started is true once the goroutine runs.
	started bool
	// changed is closed and replaced at every change a waiting command watches.
	changed chan struct{}
	current *voiceRun
	// paused is the reason the queue waits, empty when it does not; pausedUntil
	// is the end of a budget pause.
	paused          string
	pausedUntil     time.Time
	lastDownloadEnd time.Time
	doneDay         string
	doneToday       int
	lastFailure     string
}

// newVoiceWorker builds the worker; start launches it. A nil clock means the
// real one.
func newVoiceWorker(d *daemon, root context.Context, cfg VoiceTranscriptionConfig, engine VoiceEngine, clock historySyncClock) *voiceWorker {
	if clock == nil {
		clock = realHistorySyncClock{}
	}
	return &voiceWorker{
		d: d, cfg: cfg.withDefaults(), engine: engine, clock: clock, root: root,
		dir:  filepath.Join(d.opts.Paths.DatabaseDir, voiceTempDirName),
		done: make(chan struct{}), wake: make(chan struct{}, 1), changed: make(chan struct{}),
	}
}

// startVoice creates the worker of the daemon and starts it when the setting is
// on. Without the setting there is no goroutine until a manual job asks for it.
func (d *daemon) startVoice(ctx context.Context) {
	engine := d.opts.VoiceEngine
	if engine == nil {
		engine = newRealVoiceEngine(d.opts.VoiceTranscription.Home)
	}
	d.voice = newVoiceWorker(d, ctx, d.opts.VoiceTranscription, engine, nil)
	if d.opts.VoiceTranscription.Enabled {
		d.voice.ensureStarted()
	}
}

// ensureStarted launches the goroutine once.
func (w *voiceWorker) ensureStarted() {
	w.startOnce.Do(func() {
		w.mu.Lock()
		w.started = true
		w.mu.Unlock()
		go w.loop(w.root)
	})
}

// wait joins the goroutine when it was started.
func (w *voiceWorker) wait() {
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if started {
		<-w.done
	}
}

// poke wakes an idle worker and tells the waiting commands that something
// changed.
func (w *voiceWorker) poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
	w.notify()
}

// notify closes the current change channel and opens a new one.
func (w *voiceWorker) notify() {
	w.mu.Lock()
	defer w.mu.Unlock()
	close(w.changed)
	w.changed = make(chan struct{})
}

// changes returns a channel that closes at the next change.
func (w *voiceWorker) changes() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.changed
}

// enqueue puts a job into the queue and wakes the worker. A job from a source
// other than a manual command is refused while the setting is off: nothing
// queues itself then. A manual job starts the worker when it does not run yet.
func (w *voiceWorker) enqueue(ctx context.Context, job store.VoiceJob, retry bool) (store.VoiceEnqueueOutcome, error) {
	if !w.cfg.Enabled && job.Source != store.VoiceSourceManual {
		return store.VoiceUnchanged, errVoiceDisabled
	}
	outcome, err := w.d.store.EnqueueVoice(ctx, job, retry)
	if err != nil {
		return outcome, err
	}
	w.ensureStarted()
	w.poke()
	return outcome, nil
}

func (w *voiceWorker) loop(ctx context.Context) {
	defer close(w.done)
	w.recover(ctx)
	defer w.shutdown()
	for ctx.Err() == nil {
		sleep, again := w.pass(ctx)
		if again {
			continue
		}
		w.idle(ctx, sleep)
	}
}

// recover returns the jobs a stop or a crash cut off to the queue and prepares
// an empty working directory.
func (w *voiceWorker) recover(ctx context.Context) {
	bg := context.WithoutCancel(ctx)
	if _, err := w.d.store.RecoverRunningVoice(bg); err != nil {
		w.d.log.Warn("voice queue recovery failed")
	}
	w.cleanTemp()
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		w.d.log.Warn("voice working directory could not be created")
		return
	}
	_ = os.Chmod(w.dir, 0o700)
}

// shutdown leaves nothing behind: a job cut off by the stop goes back to the
// queue and the working directory is emptied.
func (w *voiceWorker) shutdown() {
	if _, err := w.d.store.RecoverRunningVoice(context.Background()); err != nil {
		w.d.log.Warn("voice queue recovery failed")
	}
	w.cleanTemp()
}

func (w *voiceWorker) cleanTemp() {
	var err error
	if dl, ok := w.d.opts.Telegram.(VoiceDownloader); ok {
		err = dl.CleanVoiceTempDir(w.dir)
	} else {
		err = os.RemoveAll(w.dir)
	}
	if err != nil {
		w.d.log.Warn("voice working directory could not be cleaned")
	}
}

// idle waits for a signal, the sleep timer, or the end of the daemon. A worker
// that runs only for manual jobs has no timer: it looks at the queue when a
// command wakes it.
func (w *voiceWorker) idle(ctx context.Context, sleep time.Duration) {
	var timer <-chan time.Time
	if sleep > 0 && w.cfg.Enabled {
		t := w.clock.NewTimer(sleep)
		defer t.Stop()
		timer = t.C()
	}
	select {
	case <-ctx.Done():
	case <-w.wake:
	case <-timer:
	}
}

// sleep waits for d, for the end of the daemon or a stop of the job. It reports
// false when ctx ended first.
func (w *voiceWorker) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := w.clock.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C():
		return true
	}
}

// nextLocalDay is the start of the local day after t.
func nextLocalDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
}

// pass takes at most one job from the queue and works it. It returns how long
// to sleep when it found nothing to do, and again=true when it did work and the
// queue should be looked at right away.
func (w *voiceWorker) pass(ctx context.Context) (time.Duration, bool) {
	now := w.clock.Now()
	w.mu.Lock()
	if w.paused == rpc.VoicePausedBudget && !now.Before(w.pausedUntil) {
		w.paused, w.pausedUntil = "", time.Time{}
	}
	paused, until := w.paused, w.pausedUntil
	w.mu.Unlock()
	// While the engine is missing no job can run, manual ones included: look at
	// the files before touching the queue.
	if paused == rpc.VoicePausedEngineMissing {
		if len(w.engine.Missing(w.cfg.FFmpeg)) > 0 {
			return voiceIdleInterval, false
		}
		w.setPause("", time.Time{})
	}
	bg := context.WithoutCancel(ctx)
	job, ok, err := w.d.store.ClaimNextVoice(bg, now)
	if err != nil {
		w.d.log.Warn("voice queue read failed")
		return voiceIdleInterval, false
	}
	if !ok {
		// An empty queue ends the pauses: nothing waits for them.
		w.setPause("", time.Time{})
		return voiceIdleInterval, false
	}
	// A worker that runs only for manual jobs leaves every other job queued.
	if !w.cfg.Enabled && job.Source != store.VoiceSourceManual {
		w.release(ctx)
		return 0, false
	}
	// During a budget pause only a manual job, which ignores the budget, runs.
	if paused == rpc.VoicePausedBudget && job.Source != store.VoiceSourceManual {
		w.release(ctx)
		return max(until.Sub(now), time.Second), false
	}
	if out := w.process(ctx, job); out.release {
		// The job went back: looking again at once would find it again.
		return voiceIdleInterval, false
	}
	return 0, true
}

// release gives the claimed job back to the queue without spending an attempt.
func (w *voiceWorker) release(ctx context.Context) {
	if _, err := w.d.store.RecoverRunningVoice(context.WithoutCancel(ctx)); err != nil {
		w.d.log.Warn("voice queue recovery failed")
	}
	w.notify()
}

func (w *voiceWorker) setPause(reason string, until time.Time) {
	w.mu.Lock()
	changed := w.paused != reason
	w.paused, w.pausedUntil = reason, until
	w.mu.Unlock()
	if changed {
		w.notify()
	}
}

// busy reports whether a job is in progress, so the idle stop leaves it alone.
func (w *voiceWorker) busy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current != nil
}

// clearPause ends a pause of the given reason; another reason stays.
func (w *voiceWorker) clearPause(reason string) {
	w.mu.Lock()
	changed := w.paused == reason
	if changed {
		w.paused, w.pausedUntil = "", time.Time{}
	}
	w.mu.Unlock()
	if changed {
		w.notify()
	}
}

// cancel stops the job in progress when it matches (all matches everything) and
// reports whether there was one to stop.
func (w *voiceWorker) cancel(chatID int64, messageID int, all bool) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	run := w.current
	if run == nil || (!all && (run.job.ChatID != chatID || run.job.MessageID != messageID)) {
		return false
	}
	run.cancelled = true
	run.cancel()
	return true
}

// recordFailure keeps the reason of the latest failed or skipped job for the
// status. The reason is a machine word.
func (w *voiceWorker) recordFailure(reason string) {
	if len(reason) > voiceReasonLimit {
		reason = reason[:voiceReasonLimit]
	}
	w.mu.Lock()
	w.lastFailure = reason
	w.mu.Unlock()
}

func (w *voiceWorker) recordDone() {
	day := localDay(w.clock.Now())
	w.mu.Lock()
	if w.doneDay != day {
		w.doneDay, w.doneToday = day, 0
	}
	w.doneToday++
	w.mu.Unlock()
}

// status is the state of the queue for `tg voice status`.
func (w *voiceWorker) status(ctx context.Context) (rpc.VoiceStatus, error) {
	now := w.clock.Now()
	st := rpc.VoiceStatus{
		Enabled:    w.cfg.Enabled,
		DailyFiles: w.cfg.DailyFiles,
		DailyBytes: int64(w.cfg.DailyDownloadMB) << 20,
	}
	st.Missing = w.engine.Missing(w.cfg.FFmpeg)
	st.Installed = len(st.Missing) == 0
	counts, err := w.d.store.VoiceQueueCounts(ctx)
	if err != nil {
		return rpc.VoiceStatus{}, err
	}
	st.Queue = rpc.VoiceQueueCounts{Queued: counts.Queued, Running: counts.Running, Done: counts.Done, Failed: counts.Failed, Skipped: counts.Skipped}
	usage, err := w.d.store.VoiceUsage(ctx, localDay(now))
	if err != nil {
		return rpc.VoiceStatus{}, err
	}
	st.FilesToday, st.BytesToday = usage.Files, usage.Bytes
	w.mu.Lock()
	defer w.mu.Unlock()
	st.LastFailure = w.lastFailure
	if w.doneDay == localDay(now) {
		st.DoneToday = w.doneToday
	}
	switch {
	case !w.started:
		st.State = rpc.VoiceStateDisabled
	case w.paused == rpc.VoicePausedBudget && now.Before(w.pausedUntil):
		st.State, st.Paused = rpc.VoiceStatePaused, w.paused
		until := w.pausedUntil.Unix()
		st.PausedUntil = &until
	case w.paused != "" && w.paused != rpc.VoicePausedBudget:
		st.State, st.Paused = rpc.VoiceStatePaused, w.paused
	case w.current != nil:
		st.State = rpc.VoiceStateWorking
	default:
		st.State = rpc.VoiceStateIdle
	}
	if w.current != nil {
		j := w.current.job
		st.Current = &rpc.VoiceCurrentJob{ChatID: j.ChatID, MessageID: j.MessageID, Kind: j.Kind, Source: j.Source, StartedAt: j.StartedAt, Stage: w.current.stage}
	}
	return st, nil
}
