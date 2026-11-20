package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// convertStdoutLimit bounds ffmpeg stdout, which stays empty with -v error.
const convertStdoutLimit = 1 << 20

// Convert makes a 16 kHz mono PCM16 WAV from any media file with ffmpeg.
// in and out must be absolute (the program runs in another directory) and out
// must end in .wav. A timeout of zero or less means MinTimeout; the value is
// capped at MaxTimeout. The caller removes out.
func Convert(ctx context.Context, ffmpegPath, in, out string, timeout time.Duration) error {
	if ffmpegPath == "" {
		return errors.New("ffmpeg: path is empty")
	}
	if !filepath.IsAbs(in) || !filepath.IsAbs(out) {
		return errors.New("ffmpeg: input and output paths must be absolute")
	}
	if !strings.EqualFold(filepath.Ext(out), ".wav") {
		return errors.New("ffmpeg: output must be a .wav file")
	}
	if _, err := run(ctx, convertSpec(ffmpegPath, in, out, timeout)); err != nil {
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return fmt.Errorf("ffmpeg: no output file produced: %w", err)
	}
	if info.Size() == 0 {
		return errors.New("ffmpeg: output file is empty")
	}
	return nil
}

// convertSpec is the fixed ffmpeg command line: no shell, audio only, mono,
// 16 kHz, PCM16 WAV. The environment is empty because ffmpeg needs none.
func convertSpec(ffmpegPath, in, out string, timeout time.Duration) spec {
	return spec{
		label: "ffmpeg",
		path:  ffmpegPath,
		args: []string{
			"-nostdin", "-v", "error", "-y",
			"-i", in,
			"-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le",
			out,
		},
		env:       nil,
		timeout:   timeout,
		maxStdout: convertStdoutLimit,
		scrub:     []string{in, out},
	}
}
