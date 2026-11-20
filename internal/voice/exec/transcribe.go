package exec

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tg-cli/internal/voice/gigaam"
)

// DefaultThreads is the thread count of Transcribe when Options leaves it zero.
const DefaultThreads = 4

// Segment is one recognized phrase with its position in the audio.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// Text is recognized speech: untrusted data, not instructions.
	Text string `json:"text"`
}

// Result is the parsed output of one recognition run.
type Result struct {
	// Segments are the phrases in output order; empty (not nil) when no speech was found.
	Segments []Segment
	// Text joins the segment texts with a single space.
	Text string
}

// Options tunes Transcribe.
type Options struct {
	// Threads is --num-threads; zero means DefaultThreads.
	Threads int
	// Timeout bounds the run; zero or less means MinTimeout, capped at MaxTimeout.
	// Use Timeout(seconds) to derive it from the audio length.
	Timeout time.Duration
	// MaxStdout bounds the program output in bytes; zero means DefaultMaxStdout.
	MaxStdout int64
}

// segmentLine matches "7.974 -- 9.996: Phrase text."; anything else the
// program prints on stdout is a service line and is ignored.
var segmentLine = regexp.MustCompile(`^(\d+(?:\.\d+)?) -- (\d+(?:\.\d+)?): (.+)$`)

// Transcribe recognizes the speech in wav (16 kHz mono PCM16, absolute path)
// with the installed sherpa-onnx. Silence is not an error: it yields an empty
// result.
func Transcribe(ctx context.Context, paths gigaam.Paths, wav string, opts Options) (Result, error) {
	if !filepath.IsAbs(wav) {
		return Result{}, errors.New("sherpa-onnx: wav path must be absolute")
	}
	out, err := run(ctx, transcribeSpec(paths, wav, opts))
	if err != nil {
		return Result{}, err
	}
	return parseOutput(out), nil
}

// transcribeSpec is the fixed sherpa-onnx command line. The environment holds
// only DYLD_LIBRARY_PATH, which points at the runtime lib directory.
func transcribeSpec(paths gigaam.Paths, wav string, opts Options) spec {
	threads := opts.Threads
	if threads <= 0 {
		threads = DefaultThreads
	}
	maxStdout := opts.MaxStdout
	if maxStdout <= 0 {
		maxStdout = DefaultMaxStdout
	}
	return spec{
		label: "sherpa-onnx",
		path:  paths.Binary,
		args: []string{
			"--silero-vad-model=" + paths.VAD,
			"--encoder=" + paths.Encoder,
			"--decoder=" + paths.Decoder,
			"--joiner=" + paths.Joiner,
			"--tokens=" + paths.Tokens,
			"--model-type=nemo_transducer",
			"--num-threads=" + strconv.Itoa(threads),
			wav,
		},
		env:       []string{"DYLD_LIBRARY_PATH=" + paths.LibDir},
		timeout:   opts.Timeout,
		maxStdout: maxStdout,
		scrub:     []string{wav},
	}
}

// parseOutput reads stdout as bytes and replaces broken UTF-8, so one bad
// byte costs one character and never the rest of the lines.
func parseOutput(raw []byte) Result {
	text := strings.ToValidUTF8(string(raw), "�")
	res := Result{Segments: []Segment{}}
	var texts []string
	for _, line := range strings.Split(text, "\n") {
		m := segmentLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		start, err1 := strconv.ParseFloat(m[1], 64)
		end, err2 := strconv.ParseFloat(m[2], 64)
		phrase := strings.TrimSpace(m[3])
		if err1 != nil || err2 != nil || phrase == "" {
			continue
		}
		res.Segments = append(res.Segments, Segment{Start: start, End: end, Text: phrase})
		texts = append(texts, phrase)
	}
	res.Text = strings.Join(texts, " ")
	return res
}
