// Package exec runs the external programs of local transcription: ffmpeg
// converts a media file to WAV, and sherpa-onnx recognizes the speech. Programs
// start without a shell, with fixed arguments, a minimal environment and an
// empty temporary working directory. Their output is data, never instructions.
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Errors returned by this package; callers classify with errors.Is.
var (
	// ErrTimeout means the program ran past its timeout and was killed.
	ErrTimeout = errors.New("timed out")
	// ErrOutputTooLarge means stdout passed its size limit and the program was killed.
	ErrOutputTooLarge = errors.New("output too large")
	// ErrExit means the program ended with a failure status.
	ErrExit = errors.New("program failed")
	// ErrNoAudio means the media file has no audio stream, so there is nothing
	// to convert: a video recorded without sound. It is returned together with
	// ErrExit by Convert, only when the stderr of ffmpeg says so.
	ErrNoAudio = errors.New("the file has no audio stream")
)

const (
	// MinTimeout is the shortest timeout Timeout returns.
	MinTimeout = 120 * time.Second
	// MaxTimeout is the hard ceiling for every timeout.
	MaxTimeout = 3 * time.Hour

	// DefaultMaxStdout is the stdout limit of Transcribe when Options leaves it zero.
	DefaultMaxStdout int64 = 16 << 20
	// MaxStderrText is the longest stderr excerpt placed into an error.
	MaxStderrText = 200

	// stderrKeep bounds the memory used to remember stderr.
	stderrKeep = 64 << 10
	// waitDelay closes the pipes of a killed program that left children behind.
	waitDelay = 2 * time.Second
)

// Timeout returns the run limit for audioSeconds of sound:
// max(120 s, 1.0 x duration), never above three hours.
func Timeout(audioSeconds float64) time.Duration {
	d := MinTimeout
	if audioSeconds > 0 {
		// Compare in seconds first: converting a huge float to Duration overflows.
		if audioSeconds >= MaxTimeout.Seconds() {
			return MaxTimeout
		}
		if byAudio := time.Duration(audioSeconds * float64(time.Second)); byAudio > d {
			d = byAudio
		}
	}
	return d
}

func clampTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return MinTimeout
	}
	return min(d, MaxTimeout)
}

// spec describes one program run.
type spec struct {
	label     string // short name used in errors: "ffmpeg", "sherpa-onnx"
	path      string
	args      []string
	env       []string
	timeout   time.Duration
	maxStdout int64
	// scrub lists strings (paths of files the program works on) removed from
	// the stderr excerpt.
	scrub []string
	// classify names the cause of a failure status from the whole stderr of the
	// program: it returns an error that the failure is reported together with,
	// or nil when the stderr says nothing special. It may be nil.
	classify func(stderr string) error
}

// run starts the program and returns its stdout. stdout and stderr are read
// concurrently and separately, so neither can block the other.
func run(ctx context.Context, s spec) ([]byte, error) {
	cwd, err := os.MkdirTemp("", "tg-voice-run-")
	if err != nil {
		return nil, fmt.Errorf("%s: create working directory: %w", s.label, err)
	}
	defer os.RemoveAll(cwd)

	runCtx, cancel := context.WithTimeout(ctx, clampTimeout(s.timeout))
	defer cancel()

	var stdout limitedBuffer
	stdout.limit = s.maxStdout
	stdout.onOverflow = cancel
	var stderr headBuffer
	stderr.limit = stderrKeep

	cmd := osexec.CommandContext(runCtx, s.path, s.args...)
	cmd.Dir = cwd
	cmd.Env = append([]string{}, s.env...) // non-nil: an empty slice means an empty environment
	cmd.Stdin = nil
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	configureProcess(cmd)

	err = cmd.Run()
	if err == nil {
		return stdout.buf.Bytes(), nil
	}
	switch {
	case stdout.overflow:
		return nil, fmt.Errorf("%s: %w (limit %d bytes)", s.label, ErrOutputTooLarge, s.maxStdout)
	case ctx.Err() != nil:
		return nil, fmt.Errorf("%s: %w", s.label, ctx.Err())
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("%s: %w after %s", s.label, ErrTimeout, clampTimeout(s.timeout))
	}
	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) {
		excerpt := scrubStderr(stderr.buf.String(), s.scrub)
		if s.classify != nil {
			if cause := s.classify(stderr.buf.String()); cause != nil {
				return nil, fmt.Errorf("%s: %w: %w: %v: %s", s.label, ErrExit, cause, err, excerpt)
			}
		}
		return nil, fmt.Errorf("%s: %w: %v: %s", s.label, ErrExit, err, excerpt)
	}
	return nil, fmt.Errorf("%s: cannot run: %w", s.label, err)
}

// limitedBuffer collects stdout up to limit bytes. Going past the limit sets
// overflow, calls onOverflow (which kills the program) and fails the write.
type limitedBuffer struct {
	buf        bytes.Buffer
	limit      int64
	overflow   bool
	onOverflow func()
}

var errOverflow = errors.New("stdout limit exceeded")

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len())+int64(len(p)) > b.limit {
		b.overflow = true
		if b.onOverflow != nil {
			b.onOverflow()
		}
		return 0, errOverflow
	}
	return b.buf.Write(p)
}

// headBuffer keeps the first limit bytes and silently drops the rest, so a
// chatty program never blocks on a full pipe.
type headBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *headBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// pathToken matches an absolute, home-relative or dot-relative path inside a
// message.
var pathToken = regexp.MustCompile(`(?:^|[\s'"=(\[])((?:~|\.{1,2})?/[^\s'")\]]*)`)

// scrubStderr turns raw stderr into a short single-line excerpt that is safe
// to put into an error: valid UTF-8, no file names, at most MaxStderrText runes.
func scrubStderr(raw string, files []string) string {
	s := strings.ToValidUTF8(raw, "�")
	for _, f := range files {
		if f == "" {
			continue
		}
		s = strings.ReplaceAll(s, f, "<file>")
		// A short base name would also eat ordinary words, so only longer ones go.
		if base := filepath.Base(f); len(base) >= 4 {
			s = strings.ReplaceAll(s, base, "<file>")
		}
	}
	s = pathToken.ReplaceAllStringFunc(s, func(m string) string {
		// Keep the delimiter that the pattern consumed in front of the path
		// and the punctuation that followed it.
		i := strings.IndexAny(m, "~./")
		trimmed := strings.TrimRight(m, ":,;.!?")
		return m[:i] + "<file>" + m[len(trimmed):]
	})
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > MaxStderrText {
		s = string([]rune(s)[:MaxStderrText])
	}
	return s
}
