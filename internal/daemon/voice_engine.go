package daemon

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"tg-cli/internal/voice/exec"
	"tg-cli/internal/voice/gigaam"
)

// realVoiceEngine runs the installed ffmpeg and the GigaAM engine of sherpa-onnx
// from <home>/models/gigaam. It knows nothing about Telegram or the database.
type realVoiceEngine struct {
	paths gigaam.Paths
}

func newRealVoiceEngine(home string) realVoiceEngine {
	return realVoiceEngine{paths: gigaam.NewPaths(home)}
}

// VoiceEngineMissing lists what is not installed for the voice engine under
// home, as the names ffmpeg, runtime, model and vad. It runs no program: it only
// looks at the files and at PATH. An empty list means the engine is ready.
func VoiceEngineMissing(home, ffmpeg string) []string {
	return newRealVoiceEngine(home).Missing(ffmpeg)
}

func (e realVoiceEngine) Missing(ffmpeg string) []string {
	var missing []string
	if !ffmpegFound(ffmpeg) {
		missing = append(missing, "ffmpeg")
	}
	seen := map[string]bool{}
	for _, p := range gigaam.Check(e.paths) {
		if !seen[p.Component] {
			seen[p.Component] = true
			missing = append(missing, p.Component)
		}
	}
	return missing
}

// ffmpegFound reports whether the program can be started: a name that PATH
// resolves, or a path to an executable file.
func ffmpegFound(name string) bool {
	if name == "" {
		name = "ffmpeg"
	}
	path, err := osexec.LookPath(name)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func (e realVoiceEngine) Convert(ctx context.Context, ffmpeg, in, out string, timeout time.Duration) error {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	// A bare name is resolved here, so the program that runs is the one found.
	if !strings.ContainsRune(ffmpeg, filepath.Separator) {
		if path, err := osexec.LookPath(ffmpeg); err == nil {
			ffmpeg = path
		}
	}
	return exec.Convert(ctx, ffmpeg, in, out, timeout)
}

func (e realVoiceEngine) Transcribe(ctx context.Context, wav string, threads int, timeout time.Duration) ([]VoiceSegment, error) {
	res, err := exec.Transcribe(ctx, e.paths, wav, exec.Options{Threads: threads, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	segments := make([]VoiceSegment, 0, len(res.Segments))
	for _, s := range res.Segments {
		segments = append(segments, VoiceSegment{Start: s.Start, End: s.End, Text: s.Text})
	}
	return segments, nil
}

func (realVoiceEngine) WavSeconds(path string) (float64, error) { return exec.WavSeconds(path) }
func (realVoiceEngine) CheckDisk(dir string, need int64) error  { return exec.CheckDisk(dir, need) }
func (realVoiceEngine) Timeout(seconds float64) time.Duration   { return exec.Timeout(seconds) }
