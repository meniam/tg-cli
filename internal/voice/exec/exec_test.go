package exec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tg-cli/internal/voice/gigaam"
)

// script writes an executable /bin/sh stub and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stub")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nPATH=/usr/bin:/bin\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// enginePaths returns engine paths whose program is the stub.
func enginePaths(t *testing.T, stub string) gigaam.Paths {
	t.Helper()
	p := gigaam.NewPaths(t.TempDir())
	p.Binary = stub
	return p
}

func wavPath(t *testing.T) string { return filepath.Join(t.TempDir(), "voice-4711.wav") }

const lastArg = `for last; do :; done` + "\n"

func TestExecConvertArgumentsAndWorkingDirectory(t *testing.T) {
	stub := script(t, lastArg+`printf '%s\n' "$@" > "$last.args"; pwd > "$last.cwd"; echo wav > "$last"`+"\n")
	in := filepath.Join(t.TempDir(), "in.ogg")
	out := wavPath(t)
	if err := Convert(context.Background(), stub, in, out, 0); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out + ".args")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-nostdin", "-v", "error", "-y", "-i", in, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", out}
	if got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ffmpeg args = %q, want %q", got, want)
	}

	cwd, _ := os.ReadFile(out + ".cwd")
	runDir := strings.TrimSpace(string(cwd))
	here, _ := os.Getwd()
	if runDir == "" || runDir == here || runDir == filepath.Dir(out) {
		t.Errorf("working directory %q must be a separate temporary directory", runDir)
	}
	if _, err := os.Stat(runDir); err == nil {
		t.Errorf("working directory %q was not removed", runDir)
	}
}

func TestExecConvertSpecIsFixed(t *testing.T) {
	s := convertSpec("/usr/bin/ffmpeg", "/a/in.ogg", "/a/out.wav", time.Minute)
	if len(s.env) != 0 {
		t.Errorf("ffmpeg environment = %v, want empty", s.env)
	}
	if s.path != "/usr/bin/ffmpeg" || s.timeout != time.Minute {
		t.Errorf("spec = %+v", s)
	}
}

func TestExecConvertRejectsBadArguments(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "x")
	cases := map[string][3]string{
		"empty ffmpeg":    {"", abs + ".ogg", abs + ".wav"},
		"relative input":  {"/bin/true", "in.ogg", abs + ".wav"},
		"relative output": {"/bin/true", abs + ".ogg", "out.wav"},
		"not a wav":       {"/bin/true", abs + ".ogg", abs + ".mp3"},
	}
	for name, c := range cases {
		if err := Convert(context.Background(), c[0], c[1], c[2], 0); err == nil {
			t.Errorf("%s: Convert succeeded", name)
		}
	}
}

func TestExecConvertWithoutOutputIsAnError(t *testing.T) {
	stub := script(t, "exit 0\n")
	err := Convert(context.Background(), stub, filepath.Join(t.TempDir(), "in.ogg"), wavPath(t), 0)
	if err == nil || !strings.Contains(err.Error(), "no output file") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecConvertStderrIsShortAndHasNoPaths(t *testing.T) {
	in := filepath.Join(t.TempDir(), "voice-secret-chat.ogg")
	out := wavPath(t)
	stub := script(t, `echo "Error opening input file `+in+`: Invalid data found when processing input" >&2
echo "Output `+out+` (/Users/someone/private dir/file.wav) failed" >&2
head -c 3000 /dev/zero | tr '\0' 'x' >&2
exit 1
`)
	err := Convert(context.Background(), stub, in, out, 0)
	if !errors.Is(err, ErrExit) {
		t.Fatalf("error = %v, want ErrExit", err)
	}
	msg := err.Error()
	for _, leak := range []string{in, out, "voice-secret-chat", "voice-4711", "/Users", "someone", filepath.Dir(in)} {
		if strings.Contains(msg, leak) {
			t.Errorf("error leaks %q: %s", leak, msg)
		}
	}
	if !strings.Contains(msg, "Invalid data found") {
		t.Errorf("error lost the useful part: %s", msg)
	}
	excerpt := msg[strings.LastIndex(msg, "exit status 1: ")+len("exit status 1: "):]
	if n := utf8.RuneCountInString(excerpt); n > MaxStderrText {
		t.Errorf("stderr excerpt has %d characters, limit %d", n, MaxStderrText)
	}
}

// A video recorded without sound is told apart from a broken file by the text
// ffmpeg prints, in both wordings of its versions; every other failure stays a
// plain failure, and neither error leaks a path.
func TestExecConvertTellsAFileWithoutAudioFromABrokenOne(t *testing.T) {
	in := filepath.Join(t.TempDir(), "silent-secret-chat.mp4")
	for name, stderr := range map[string]string{
		"new wording":   "[wav @ 0x600003e5c000] Output file does not contain any stream",
		"old wording":   "Output file #0 does not contain any stream",
		"other case":    "OUTPUT FILE #0 DOES NOT CONTAIN ANY STREAM",
		"after a noise": "Error opening filters!\n" + strings.Repeat("x", 3000) + "\nOutput file does not contain any stream",
	} {
		t.Run(name, func(t *testing.T) {
			stub := script(t, "cat >&2 <<'EOT'\n"+stderr+"\nEOT\nexit 1\n")
			err := Convert(context.Background(), stub, in, wavPath(t), 0)
			if !errors.Is(err, ErrNoAudio) || !errors.Is(err, ErrExit) {
				t.Fatalf("error = %v, want ErrNoAudio and ErrExit", err)
			}
			if strings.Contains(err.Error(), "silent-secret-chat") || strings.Contains(err.Error(), filepath.Dir(in)) {
				t.Errorf("error leaks the input: %s", err)
			}
		})
	}
	for name, body := range map[string]string{
		"broken file":     `echo "Invalid data found when processing input" >&2` + "\nexit 1\n",
		"no stderr":       "exit 1\n",
		"another stream":  `echo "Stream map '0:a' matches no streams." >&2` + "\nexit 1\n",
		"success but odd": `echo "Output file does not contain any stream" >&2` + "\n" + lastArg + `echo wav > "$last"` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			err := Convert(context.Background(), script(t, body), in, wavPath(t), 0)
			if errors.Is(err, ErrNoAudio) {
				t.Fatalf("error = %v, must not be ErrNoAudio", err)
			}
			if name != "success but odd" && !errors.Is(err, ErrExit) {
				t.Fatalf("error = %v, want ErrExit", err)
			}
			if name == "success but odd" && err != nil {
				t.Fatalf("a run that succeeded gave %v", err)
			}
		})
	}
}

func TestExecConvertTimeoutKillsProcessGroup(t *testing.T) {
	// The shell starts a child that holds the pipes; killing only the shell would hang Wait.
	stub := script(t, "sleep 60 &\nsleep 60\n")
	start := time.Now()
	err := Convert(context.Background(), stub, filepath.Join(t.TempDir(), "in.ogg"), wavPath(t), 200*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > waitDelay {
		t.Errorf("took %s to stop the process tree", d)
	}
}

func TestExecRunStopsOnParentCancel(t *testing.T) {
	stub := script(t, "sleep 60\n")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	err := Convert(ctx, stub, filepath.Join(t.TempDir(), "in.ogg"), wavPath(t), time.Hour)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestExecRunMissingProgram(t *testing.T) {
	err := Convert(context.Background(), filepath.Join(t.TempDir(), "no-such-ffmpeg"),
		filepath.Join(t.TempDir(), "in.ogg"), wavPath(t), 0)
	if err == nil || errors.Is(err, ErrExit) || errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want a start failure", err)
	}
}

func TestExecRunEnvironmentIsExactlyWhatTheSpecSays(t *testing.T) {
	t.Setenv("TG_VOICE_TEST_SECRET", "leak")
	t.Setenv("USER", "someone")
	stub := script(t, "export -p\n")
	out, err := run(context.Background(), spec{
		label: "stub", path: stub, env: []string{"TG_ALLOWED=1"}, timeout: 10 * time.Second, maxStdout: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "TG_ALLOWED") {
		t.Errorf("allowed variable is missing:\n%s", s)
	}
	for _, leak := range []string{"TG_VOICE_TEST_SECRET", "USER", "HOME"} {
		if strings.Contains(s, leak) {
			t.Errorf("environment leaks %s:\n%s", leak, s)
		}
	}
}

func TestExecTranscribeSpecIsFixed(t *testing.T) {
	p := gigaam.NewPaths("/home/x")
	s := transcribeSpec(p, "/tmp/a.wav", Options{Threads: 2, Timeout: time.Minute})
	want := []string{
		"--silero-vad-model=" + p.VAD,
		"--encoder=" + p.Encoder,
		"--decoder=" + p.Decoder,
		"--joiner=" + p.Joiner,
		"--tokens=" + p.Tokens,
		"--model-type=nemo_transducer",
		"--num-threads=2",
		"/tmp/a.wav",
	}
	if s.path != p.Binary || !reflect.DeepEqual(s.args, want) {
		t.Errorf("command = %s %q, want %s %q", s.path, s.args, p.Binary, want)
	}
	if !reflect.DeepEqual(s.env, []string{"DYLD_LIBRARY_PATH=" + p.LibDir}) {
		t.Errorf("env = %v, want only DYLD_LIBRARY_PATH", s.env)
	}
	if s.maxStdout != DefaultMaxStdout || DefaultMaxStdout != 16<<20 {
		t.Errorf("default stdout limit = %d", s.maxStdout)
	}
	if d := transcribeSpec(p, "/tmp/a.wav", Options{}); d.args[6] != "--num-threads=4" {
		t.Errorf("default threads: %s", d.args[6])
	}
}

func TestExecTranscribeRejectsRelativeWav(t *testing.T) {
	if _, err := Transcribe(context.Background(), gigaam.NewPaths(t.TempDir()), "a.wav", Options{}); err == nil {
		t.Fatal("relative wav accepted")
	}
}

func TestExecTranscribeParsesSegmentsAndSkipsServiceLines(t *testing.T) {
	stub := script(t, `cat <<'EOF'
Started
OfflineRecognizerConfig(model=/Users/someone/model)
7.974 -- 9.996: Привет, как дела?
Segment buffer: 1.0 -- 2.0 is not a result
12 -- 15.5: Ответ без дробной части в начале.
0.000 -- 1.000:
Elapsed seconds: 3.2
18.250 -- 20.000: Последняя фраза.
EOF
`)
	res, err := Transcribe(context.Background(), enginePaths(t, stub), wavPath(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{
		{Start: 7.974, End: 9.996, Text: "Привет, как дела?"},
		{Start: 12, End: 15.5, Text: "Ответ без дробной части в начале."},
		{Start: 18.25, End: 20, Text: "Последняя фраза."},
	}
	if !reflect.DeepEqual(res.Segments, want) {
		t.Fatalf("segments = %+v, want %+v", res.Segments, want)
	}
	if wantText := "Привет, как дела? Ответ без дробной части в начале. Последняя фраза."; res.Text != wantText {
		t.Errorf("text = %q, want %q", res.Text, wantText)
	}
	if strings.Contains(res.Text, "Started") || strings.Contains(res.Text, "Elapsed") {
		t.Errorf("service lines leaked into the text: %q", res.Text)
	}
}

func TestExecTranscribeBrokenUTF8CostsOneCharacter(t *testing.T) {
	// Octal escapes: \320 is a lone lead byte, then a valid word "Привет" (\320\237...).
	stub := script(t, `printf '1.000 -- 2.000: a\320b\n'
printf '3.000 -- 4.000: \320\237\321\200\320\270\320\262\320\265\321\202\n'
printf '5.000 -- 6.000: \377\376 tail\n'
`)
	res, err := Transcribe(context.Background(), enginePaths(t, stub), wavPath(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Segments) != 3 {
		t.Fatalf("segments = %+v, want 3", res.Segments)
	}
	if res.Segments[0].Text != "a\uFFFDb" || res.Segments[1].Text != "Привет" || !strings.HasSuffix(res.Segments[2].Text, " tail") {
		t.Errorf("segments = %+v", res.Segments)
	}
	if !utf8.ValidString(res.Text) {
		t.Error("result text is not valid UTF-8")
	}
}

func TestExecTranscribeReadsBothStreamsConcurrently(t *testing.T) {
	// 3 MB on stderr and 200k result lines on stdout at the same time: reading
	// the streams one after another would block on a full pipe.
	stub := script(t, `(yes "1.0 -- 2.0: слово" | head -n 200000) &
head -c 3000000 /dev/zero | tr '\0' 'e' >&2
wait
`)
	done := make(chan error, 1)
	var res Result
	go func() {
		var err error
		res, err = Transcribe(context.Background(), enginePaths(t, stub), wavPath(t), Options{Timeout: time.Minute})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("Transcribe blocked: streams are not read concurrently")
	}
	if len(res.Segments) != 200000 {
		t.Fatalf("segments = %d, want 200000", len(res.Segments))
	}
}

func TestExecTranscribeStdoutLimitKillsProgram(t *testing.T) {
	stub := script(t, `yes "1.0 -- 2.0: слово"`+"\n")
	start := time.Now()
	_, err := Transcribe(context.Background(), enginePaths(t, stub), wavPath(t), Options{MaxStdout: 4096, Timeout: time.Minute})
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("error = %v, want ErrOutputTooLarge", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("limit hit after %s; the program was not killed", d)
	}
}

func TestExecTranscribeTimeoutKillsProgram(t *testing.T) {
	stub := script(t, "echo '1.0 -- 2.0: start'\nsleep 60\n")
	start := time.Now()
	_, err := Transcribe(context.Background(), enginePaths(t, stub), wavPath(t), Options{Timeout: 300 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > waitDelay+time.Second {
		t.Errorf("took %s to stop", d)
	}
}

func TestExecTranscribeEmptyResultIsNotAnError(t *testing.T) {
	stub := script(t, "echo 'Started'\necho 'Done'\n")
	res, err := Transcribe(context.Background(), enginePaths(t, stub), wavPath(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Segments == nil || len(res.Segments) != 0 || res.Text != "" {
		t.Fatalf("result = %+v, want empty non-nil segments and empty text", res)
	}
}

func TestExecTranscribeFailureCarriesScrubbedStderr(t *testing.T) {
	wav := wavPath(t)
	stub := script(t, `echo "Failed to load /Users/someone/models/gigaam/model/encoder.int8.onnx for `+wav+`" >&2
echo "1.0 -- 2.0: partial result"
exit 3
`)
	_, err := Transcribe(context.Background(), enginePaths(t, stub), wav, Options{})
	if !errors.Is(err, ErrExit) {
		t.Fatalf("error = %v, want ErrExit", err)
	}
	for _, leak := range []string{"/Users", "someone", "encoder.int8.onnx", "voice-4711"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error leaks %q: %v", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "Failed to load") {
		t.Errorf("error lost the message: %v", err)
	}
}

func TestExecScrubStderr(t *testing.T) {
	long := strings.Repeat("я", 500)
	cases := []struct{ name, in, want string }{
		{"absolute path", "cannot open /var/folders/ab/cd/in.ogg: no such file", "cannot open <file>: no such file"},
		{"home path", "load ~/models/x.onnx failed", "load <file> failed"},
		{"relative path", "see ./a/b and ../c", "see <file> and <file>"},
		{"after equals and quote", `config(encoder=/opt/e.onnx, name="/tmp/n")`, `config(encoder=<file>, name="<file>")`},
		{"plain words with slash kept", "and/or audio/mp4 fail", "and/or audio/mp4 fail"},
		{"newlines collapse", "line one\n\n  line two\t", "line one line two"},
		{"bad utf8", "bad \xff byte", "bad \uFFFD byte"},
		{"long text is cut", long, long[:MaxStderrText*2]}, // 2 bytes per rune
	}
	for _, tc := range cases {
		if got := scrubStderr(tc.in, nil); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := scrubStderr("open /d/voice-1.ogg then voice-1.ogg", []string{"/d/voice-1.ogg"}); strings.Contains(got, "voice-1") {
		t.Errorf("known file names must go: %q", got)
	}
	// A short base name must not eat ordinary words.
	if got := scrubStderr("a b c", []string{"/d/a"}); got != "a b c" {
		t.Errorf("short base name replaced: %q", got)
	}
}

func TestExecTimeout(t *testing.T) {
	nan := func() float64 { var z float64; return z / z }()
	cases := []struct {
		seconds float64
		want    time.Duration
	}{
		{0, 120 * time.Second},
		{-5, 120 * time.Second},
		{nan, 120 * time.Second},
		{60, 120 * time.Second},
		{120, 120 * time.Second},
		{200.5, 200500 * time.Millisecond},
		{7200, 2 * time.Hour},
		{10799, 10799 * time.Second},
		{10800, 3 * time.Hour},
		{1e9, 3 * time.Hour},
		{1e300, 3 * time.Hour},
	}
	for _, tc := range cases {
		if got := Timeout(tc.seconds); got != tc.want {
			t.Errorf("Timeout(%v) = %s, want %s", tc.seconds, got, tc.want)
		}
	}
	if clampTimeout(0) != MinTimeout || clampTimeout(48*time.Hour) != MaxTimeout || clampTimeout(time.Minute) != time.Minute {
		t.Error("clampTimeout")
	}
}
