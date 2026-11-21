package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVoiceTranscriptionDefaults(t *testing.T) {
	for name, content := range map[string]string{"missing file": "", "template": Template()} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadBackgroundSync(t, content)
			if err != nil {
				t.Fatal(err)
			}
			values, err := cfg.VoiceTranscription.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			want := VoiceTranscriptionValues{
				Enabled: true,
				FFmpeg:  "ffmpeg", MaxVoiceMB: 30, MaxVideoNoteMB: 50, MaxVideoMB: 500, MaxVideoMinutes: 120,
				DailyDownloadMB: 5000, DailyFiles: 500, Pause: 3 * time.Second, Threads: 4,
			}
			if values != want {
				t.Fatalf("values = %+v, want %+v (enabled)", values, want)
			}
		})
	}
}

func TestVoiceTranscriptionFromFile(t *testing.T) {
	cfg, err := loadBackgroundSync(t, `[voice_transcription]
enabled = true
ffmpeg = "/opt/homebrew/bin/ffmpeg"
max_voice_mb = 10
max_video_note_mb = 20
max_video_mb = 1000
max_video_minutes = 60
daily_download_mb = 500
daily_files = 50
pause = "0s"
threads = 16
`)
	if err != nil {
		t.Fatal(err)
	}
	values, err := cfg.VoiceTranscription.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := VoiceTranscriptionValues{
		Enabled: true, FFmpeg: "/opt/homebrew/bin/ffmpeg", MaxVoiceMB: 10, MaxVideoNoteMB: 20, MaxVideoMB: 1000, MaxVideoMinutes: 60,
		DailyDownloadMB: 500, DailyFiles: 50, Pause: 0, Threads: 16,
	}
	if values != want {
		t.Fatalf("values = %+v, want %+v", values, want)
	}
}

func TestVoiceTranscriptionAcceptsTheBounds(t *testing.T) {
	for name, body := range map[string]string{
		"limit low":     "max_voice_mb = 1",
		"limit high":    "max_video_mb = 1000000",
		"pause hour":    `pause = "1h"`,
		"pause zero":    `pause = "0s"`,
		"threads one":   "threads = 1",
		"threads 16":    "threads = 16",
		"ffmpeg longer": `ffmpeg = "` + strings.Repeat("f", 1024) + `"`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadBackgroundSync(t, "[voice_transcription]\n"+body+"\n"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestVoiceTranscriptionRejectsInvalidValues covers every rule of the section:
// each invalid file is a usage error (code 2) that names the key.
func TestVoiceTranscriptionRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"ffmpeg empty", `ffmpeg = ""`, "voice_transcription.ffmpeg must be 1 to 1024 characters"},
		{"ffmpeg blank", `ffmpeg = "  "`, "voice_transcription.ffmpeg must be 1 to 1024 characters"},
		{"ffmpeg too long", `ffmpeg = "` + strings.Repeat("f", 1025) + `"`, "voice_transcription.ffmpeg must be 1 to 1024 characters"},
		{"ffmpeg control character", `ffmpeg = "ff\u0007mpeg"`, "voice_transcription.ffmpeg must not contain control characters"},
		{"voice mb zero", `max_voice_mb = 0`, "voice_transcription.max_voice_mb must be a whole number from 1 to 1000000"},
		{"voice mb negative", `max_voice_mb = -1`, "voice_transcription.max_voice_mb must be a whole number"},
		{"note mb zero", `max_video_note_mb = 0`, "voice_transcription.max_video_note_mb"},
		{"video mb negative", `max_video_mb = -500`, "voice_transcription.max_video_mb"},
		{"video mb huge", `max_video_mb = 1000001`, "voice_transcription.max_video_mb"},
		{"minutes zero", `max_video_minutes = 0`, "voice_transcription.max_video_minutes"},
		{"daily mb zero", `daily_download_mb = 0`, "voice_transcription.daily_download_mb"},
		{"daily files negative", `daily_files = -3`, "voice_transcription.daily_files"},
		{"pause not a duration", `pause = "slowly"`, "voice_transcription.pause must be a duration from 0s to 1h"},
		{"pause negative", `pause = "-1s"`, "voice_transcription.pause must be a duration from 0s to 1h"},
		{"pause above an hour", `pause = "1h1s"`, "voice_transcription.pause must be a duration from 0s to 1h"},
		{"pause without unit", `pause = "3"`, "voice_transcription.pause must be a duration from 0s to 1h"},
		{"threads zero", `threads = 0`, "voice_transcription.threads must be a whole number from 1 to 16"},
		{"threads negative", `threads = -2`, "voice_transcription.threads must be a whole number from 1 to 16"},
		{"threads 17", `threads = 17`, "voice_transcription.threads must be a whole number from 1 to 16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadBackgroundSync(t, "[voice_transcription]\n"+tc.body+"\n")
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code() != 2 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want a code 2 error containing %q", err, tc.want)
			}
		})
	}
}

func TestVoiceTranscriptionTypesAreChecked(t *testing.T) {
	for name, body := range map[string]string{
		"enabled string": `enabled = "yes"`,
		"limit string":   `max_voice_mb = "30"`,
		"limit float":    `max_voice_mb = 30.5`,
		"pause int":      `pause = 3`,
		"threads string": `threads = "4"`,
		"unknown key":    `model = "x"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadBackgroundSync(t, "[voice_transcription]\n"+body+"\n")
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code() != 2 {
				t.Fatalf("Load error = %v, want a code 2 error", err)
			}
		})
	}
}
