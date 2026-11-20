package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// voiceOutcome is how a job ended. State and Reason go to the queue row; the
// reason is a machine word and never carries message text, a path or a name.
type voiceOutcome struct {
	state  string
	reason string
	bytes  int64
	// release gives the job back to the queue without spending an attempt.
	release bool
	// retry asks for another attempt after a pause: the download failed on the
	// network.
	retry bool
}

func voiceDone(reason string, bytes int64) voiceOutcome {
	return voiceOutcome{state: store.VoiceStateDone, reason: reason, bytes: bytes}
}
func voiceFailed(reason string) voiceOutcome {
	return voiceOutcome{state: store.VoiceStateFailed, reason: reason}
}
func voiceSkipped(reason string) voiceOutcome {
	return voiceOutcome{state: store.VoiceStateSkipped, reason: reason}
}
func voiceReleased() voiceOutcome { return voiceOutcome{release: true} }

// voiceMedia is what the saved message says about its file.
type voiceMedia struct {
	// Size is the file size in bytes, zero when the message does not say.
	Size int64
	// Seconds is the duration, zero when the message does not say.
	Seconds float64
}

// voiceMediaInfo reads the size and the duration of the media of a stored
// message from its raw Telegram object. An old or foreign shape gives zeros: the
// limits then act on what is known and on the program timeouts.
func voiceMediaInfo(raw json.RawMessage) voiceMedia {
	var m struct {
		Media struct {
			Document struct {
				Size       int64 `json:"size"`
				Attributes []struct {
					Type     string  `json:"_"`
					Duration float64 `json:"duration"`
				} `json:"attributes"`
			} `json:"document"`
		} `json:"media"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return voiceMedia{}
	}
	out := voiceMedia{}
	if m.Media.Document.Size > 0 {
		out.Size = m.Media.Document.Size
	}
	for _, a := range m.Media.Document.Attributes {
		if (a.Type == "documentAttributeAudio" || a.Type == "documentAttributeVideo") && a.Duration > out.Seconds {
			out.Seconds = a.Duration
		}
	}
	return out
}

// voiceMessageMedia reads the size and the duration of a stored message. It is
// the one place the worker looks into messages.raw, and it keeps only these two
// numbers: nothing of the Telegram object leaves it.
func voiceMessageMedia(msg store.Message) voiceMedia { return voiceMediaInfo(msg.Raw) }

// limitBytes is the largest file the worker downloads for a kind.
func (w *voiceWorker) limitBytes(kind string) int64 {
	mb := w.cfg.MaxVoiceMB
	switch kind {
	case store.VoiceKindVideoNote:
		mb = w.cfg.MaxVideoNoteMB
	case store.VoiceKindVideo:
		mb = w.cfg.MaxVideoMB
	}
	return int64(mb) << 20
}

func (w *voiceWorker) maxVideoSeconds() float64 { return float64(w.cfg.MaxVideoMinutes) * 60 }

// process works one claimed job from start to end and records how it ended.
func (w *voiceWorker) process(ctx context.Context, job store.VoiceJob) voiceOutcome {
	runCtx, cancel := context.WithCancel(ctx)
	run := &voiceRun{job: job, cancel: cancel}
	w.mu.Lock()
	w.current = run
	w.mu.Unlock()
	w.notify()
	var out voiceOutcome
	func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				w.d.log.Error("voice job panicked")
				out = voiceFailed("internal")
			}
		}()
		out = w.execute(runCtx, run)
	}()
	w.mu.Lock()
	w.current = nil
	w.mu.Unlock()
	w.conclude(ctx, job, out)
	return out
}

// conclude writes the end of a job to the queue. The context of the daemon may
// be over already: the write must still happen.
func (w *voiceWorker) conclude(ctx context.Context, job store.VoiceJob, out voiceOutcome) {
	bg := context.WithoutCancel(ctx)
	switch {
	case out.release:
		w.release(ctx)
		return
	case out.retry && job.Attempts < len(voiceRetryDelays):
		if _, err := w.d.store.RequeueVoice(bg, job.ChatID, job.MessageID, "download_retry"); err == nil {
			w.notify()
			// The pause is the worker's own: other jobs wait too, and a restart
			// of the daemon forgets it.
			w.sleep(ctx, voiceRetryDelays[job.Attempts])
			return
		}
		w.d.log.Warn("voice job could not be queued again")
		out = voiceFailed("download")
	case out.retry:
		out = voiceFailed("download")
	}
	if err := w.d.store.FinishVoice(bg, job.ChatID, job.MessageID, out.state, out.reason, out.bytes, w.clock.Now()); err != nil {
		w.d.log.Warn("voice job could not be finished")
	}
	switch out.state {
	case store.VoiceStateDone:
		w.recordDone()
	case store.VoiceStateFailed, store.VoiceStateSkipped:
		if out.reason != store.VoiceReasonCancelled {
			w.recordFailure(out.reason)
		}
	}
	w.notify()
}

// abort is the end of a job whose context ended: a stop of the daemon gives the
// job back to the queue, a cancel command skips it.
func (w *voiceWorker) abort(run *voiceRun) voiceOutcome {
	if w.root.Err() != nil {
		return voiceReleased()
	}
	w.mu.Lock()
	cancelled := run.cancelled
	w.mu.Unlock()
	if cancelled {
		return voiceSkipped(store.VoiceReasonCancelled)
	}
	return voiceFailed("interrupted")
}

// execute is the chain of one job: checks, download, conversion, recognition
// and save. Every file it makes is removed on every way out.
func (w *voiceWorker) execute(ctx context.Context, run *voiceRun) voiceOutcome {
	job := run.job
	d := w.d
	if !d.policy().Allowed(job.ChatID) {
		return voiceSkipped("closed")
	}
	msg, err := d.store.GetMessage(ctx, job.ChatID, job.MessageID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return voiceFailed("no_message")
	case err != nil:
		return voiceFailed("store")
	}
	if msg.DeletedAt != nil || msg.MediaType == nil || *msg.MediaType != job.Kind {
		return voiceSkipped("no_media")
	}
	// A text that exists already is not made again, except by a manual job: a
	// manual job that finds one is a retry the owner asked for.
	if job.Source != store.VoiceSourceManual {
		if t, err := d.store.GetTranscription(ctx, job.ChatID, job.MessageID); err == nil && !t.Pending {
			return voiceDone("exists", 0)
		}
	}
	info := voiceMessageMedia(msg)
	limit := w.limitBytes(job.Kind)
	if info.Size > limit {
		return voiceSkipped("too_large")
	}
	if job.Kind == store.VoiceKindVideo && info.Seconds > w.maxVideoSeconds() {
		return voiceSkipped("too_long")
	}
	if len(w.engine.Missing(w.cfg.FFmpeg)) > 0 {
		w.setPause(rpc.VoicePausedEngineMissing, time.Time{})
		return voiceReleased()
	}
	w.clearPause(rpc.VoicePausedEngineMissing)
	now := w.clock.Now()
	if job.Source != store.VoiceSourceManual {
		usage, err := d.store.VoiceUsage(ctx, localDay(now))
		if err == nil && (usage.Files >= w.cfg.DailyFiles || usage.Bytes >= int64(w.cfg.DailyDownloadMB)<<20) {
			w.setPause(rpc.VoicePausedBudget, nextLocalDay(now))
			return voiceReleased()
		}
	}
	need := info.Size
	if need == 0 {
		need = limit
	}
	if err := w.engine.CheckDisk(w.dir, need); err != nil {
		return voiceFailed("disk")
	}
	if !w.waitDownloadPause(ctx) {
		return w.abort(run)
	}
	file, err := w.download(ctx, job, VoiceDownload{Dir: w.dir, MaxBytes: limit, ExpectedSize: info.Size})
	w.markDownloadEnd()
	if err != nil {
		return w.downloadOutcome(ctx, run, err)
	}
	defer os.Remove(file.Path)
	w.clearPause(rpc.VoicePausedOffline)
	// The file crossed the network: it counts in the budget whatever follows.
	if err := d.store.AddVoiceUsage(context.WithoutCancel(ctx), localDay(w.clock.Now()), 1, file.Size); err != nil {
		d.log.Warn("voice usage could not be recorded")
	}
	if ctx.Err() != nil {
		return w.abort(run)
	}

	wav := filepath.Join(w.dir, fmt.Sprintf("%d_%d.wav", job.ChatID, job.MessageID))
	defer os.Remove(wav)
	guess := info.Seconds
	if guess <= 0 && job.Kind == store.VoiceKindVideo {
		guess = w.maxVideoSeconds()
	}
	if info.Seconds > 0 {
		if err := w.engine.CheckDisk(w.dir, int64(info.Seconds*voiceBytesPerWavSecond)); err != nil {
			return voiceFailed("disk")
		}
	}
	if err := w.engine.Convert(ctx, w.cfg.FFmpeg, file.Path, wav, w.engine.Timeout(guess)); err != nil {
		if ctx.Err() != nil {
			return w.abort(run)
		}
		return voiceFailed("convert")
	}
	// The media is no longer needed; its space may be needed by the engine.
	_ = os.Remove(file.Path)
	seconds, err := w.engine.WavSeconds(wav)
	if err != nil {
		return voiceFailed("convert")
	}
	if job.Kind == store.VoiceKindVideo && seconds > w.maxVideoSeconds() {
		return voiceSkipped("too_long")
	}
	segments, err := w.engine.Transcribe(ctx, wav, w.cfg.Threads, w.engine.Timeout(seconds))
	if err != nil {
		if ctx.Err() != nil {
			return w.abort(run)
		}
		return voiceFailed("engine")
	}
	if ctx.Err() != nil {
		return w.abort(run)
	}
	// The access list may have changed while the job ran.
	if !d.policy().Allowed(job.ChatID) {
		return voiceSkipped("closed")
	}
	text := voiceText(segments)
	raw, err := json.Marshal(map[string]any{"voice_local": map[string]any{"engine": voiceEngineName, "segments": nonNilSegments(segments)}})
	if err != nil {
		return voiceFailed("store")
	}
	if err := d.store.PutTranscription(context.WithoutCancel(ctx), store.Transcription{
		ChatID: job.ChatID, MessageID: job.MessageID, Text: text, Raw: raw,
		Source: store.TranscriptionSourceLocal, Engine: voiceEngineName, Duration: &seconds,
	}); err != nil {
		return voiceFailed("store")
	}
	d.triggerSemanticIndex()
	reason := ""
	if text == "" {
		reason = "no_speech"
	}
	return voiceDone(reason, file.Size)
}

func nonNilSegments(s []VoiceSegment) []VoiceSegment {
	if s == nil {
		return []VoiceSegment{}
	}
	return s
}

// voiceText joins the phrases of a recognition with single spaces.
func voiceText(segments []VoiceSegment) string {
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// waitDownloadPause keeps the pause between two downloads. It reports false
// when the job was stopped meanwhile.
func (w *voiceWorker) waitDownloadPause(ctx context.Context) bool {
	w.mu.Lock()
	last := w.lastDownloadEnd
	w.mu.Unlock()
	if last.IsZero() || w.cfg.Pause <= 0 {
		return ctx.Err() == nil
	}
	return w.sleep(ctx, last.Add(w.cfg.Pause).Sub(w.clock.Now()))
}

func (w *voiceWorker) markDownloadEnd() {
	w.mu.Lock()
	w.lastDownloadEnd = w.clock.Now()
	w.mu.Unlock()
}

// download leases the Telegram client the way a request handler does and asks
// it for the file.
func (w *voiceWorker) download(ctx context.Context, job store.VoiceJob, req VoiceDownload) (VoiceFile, error) {
	d := w.d
	if d.opts.NewTelegram != nil {
		leased, client, release, err := d.acquireTelegram(ctx)
		if err != nil {
			return VoiceFile{}, err
		}
		defer release()
		ctx = context.WithValue(leased, telegramContextKey{}, client)
	}
	if err := d.requireAccount(rpc.WithConnectionWait(ctx)); err != nil {
		return VoiceFile{}, err
	}
	dl, ok := d.telegram(ctx).(VoiceDownloader)
	if !ok {
		return VoiceFile{}, errVoiceNoDownloader
	}
	return dl.DownloadVoiceFile(ctx, chatref.Ref{Kind: chatref.ID, ID: job.ChatID}, job.MessageID, req)
}

var errVoiceNoDownloader = errors.New("the Telegram client cannot download voice files")

// downloadOutcome sorts a failed download: a stop, a limit, an absent connection
// (the queue waits), a network failure (retried after a pause) or a refusal that
// retrying does not change.
func (w *voiceWorker) downloadOutcome(ctx context.Context, run *voiceRun, err error) voiceOutcome {
	if ctx.Err() != nil {
		return w.abort(run)
	}
	if errors.Is(err, ErrVoiceTooLarge) {
		return voiceSkipped("too_large")
	}
	var re *rpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case rpc.CodeOffline, rpc.CodeNotAuthorized:
			w.setPause(rpc.VoicePausedOffline, time.Time{})
			return voiceReleased()
		case rpc.CodeNotFound:
			return voiceFailed("no_media")
		}
	}
	if voiceNetworkError(err) {
		return voiceOutcome{retry: true}
	}
	return voiceFailed("download")
}

// voiceNetworkError reports whether a failed download is worth another try: a
// broken or timed-out connection, or a flood wait that came back to the caller.
func voiceNetworkError(err error) bool {
	var re *rpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case rpc.CodeFloodWait:
			return true
		case rpc.CodeTelegram:
			// The client reports a broken connection as a Telegram error with a
			// fixed message and the network error as its cause.
			var ne net.Error
			return errors.As(err, &ne) || strings.HasPrefix(re.Message, "telegram request failed:")
		}
		return false
	}
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}
