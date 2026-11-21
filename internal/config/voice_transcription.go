package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// VoiceTranscription holds the settings of the local transcription of voice
// messages, video notes and videos. The values are kept as the owner wrote
// them; Resolve checks and converts them.
type VoiceTranscription struct {
	// Enabled turns the sources and the queue worker on by default.
	Enabled bool `toml:"enabled"`
	// FFmpeg is the program that converts media to WAV: a name looked up in
	// PATH or a path.
	FFmpeg string `toml:"ffmpeg"`
	// MaxVoiceMB, MaxVideoNoteMB and MaxVideoMB are the largest files, in
	// megabytes, the worker downloads for a voice message, a video note and a
	// video. MaxVideoMinutes is the longest video it accepts.
	MaxVoiceMB      int `toml:"max_voice_mb"`
	MaxVideoNoteMB  int `toml:"max_video_note_mb"`
	MaxVideoMB      int `toml:"max_video_mb"`
	MaxVideoMinutes int `toml:"max_video_minutes"`
	// DailyDownloadMB and DailyFiles are the budget of one local day.
	DailyDownloadMB int `toml:"daily_download_mb"`
	DailyFiles      int `toml:"daily_files"`
	// Pause is the wait between two downloads, as a Go duration.
	Pause string `toml:"pause"`
	// Threads is the number of threads of the speech engine.
	Threads int `toml:"threads"`
}

// VoiceTranscriptionValues is a checked VoiceTranscription.
type VoiceTranscriptionValues struct {
	Enabled         bool
	FFmpeg          string
	MaxVoiceMB      int
	MaxVideoNoteMB  int
	MaxVideoMB      int
	MaxVideoMinutes int
	DailyDownloadMB int
	DailyFiles      int
	Pause           time.Duration
	Threads         int
}

const (
	// DefaultVoiceDailyDownloadMB and DefaultVoiceDailyFiles bound daily downloads.
	DefaultVoiceDailyDownloadMB = 5000
	DefaultVoiceDailyFiles      = 500
	// MaxVoiceFFmpegRunes is the longest accepted program name or path.
	MaxVoiceFFmpegRunes = 1024
	// MaxVoiceLimit is the largest accepted size, count or minutes: far above
	// any real need, small enough that a value in megabytes never overflows
	// when it becomes bytes.
	MaxVoiceLimit = 1_000_000
	// MaxVoicePause is the longest accepted pause between two downloads.
	MaxVoicePause = time.Hour
	// MinVoiceThreads and MaxVoiceThreads bound the engine threads.
	MinVoiceThreads = 1
	MaxVoiceThreads = 16
)

func defaultVoiceTranscription() VoiceTranscription {
	return VoiceTranscription{
		Enabled:    true,
		FFmpeg:     "ffmpeg",
		MaxVoiceMB: 30, MaxVideoNoteMB: 50, MaxVideoMB: 500, MaxVideoMinutes: 120,
		DailyDownloadMB: DefaultVoiceDailyDownloadMB, DailyFiles: DefaultVoiceDailyFiles,
		Pause: "3s", Threads: 4,
	}
}

// CheckVoiceFFmpeg reports why a program name or path is not usable. The error
// text is the tail of the message that names the key.
func CheckVoiceFFmpeg(value string) error {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > MaxVoiceFFmpegRunes {
		return fmt.Errorf("must be 1 to %d characters", MaxVoiceFFmpegRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	return nil
}

// ParseVoiceLimit reads a whole positive number: megabytes, minutes or a count
// of files.
func ParseVoiceLimit(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > MaxVoiceLimit {
		return 0, fmt.Errorf("must be a whole number from 1 to %d", MaxVoiceLimit)
	}
	return n, nil
}

// ParseVoicePause reads a Go duration from zero to one hour.
func ParseVoicePause(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 || d > MaxVoicePause {
		return 0, fmt.Errorf("must be a duration from 0s to 1h such as 3s or 1m")
	}
	return d, nil
}

// ParseVoiceThreads reads the number of engine threads.
func ParseVoiceThreads(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < MinVoiceThreads || n > MaxVoiceThreads {
		return 0, fmt.Errorf("must be a whole number from %d to %d", MinVoiceThreads, MaxVoiceThreads)
	}
	return n, nil
}

// Resolve checks every setting and returns the converted values. An error names
// the config key; it is a usage error.
func (v VoiceTranscription) Resolve() (VoiceTranscriptionValues, error) {
	if err := CheckVoiceFFmpeg(v.FFmpeg); err != nil {
		return VoiceTranscriptionValues{}, invalid("voice_transcription.ffmpeg %v", err)
	}
	out := VoiceTranscriptionValues{Enabled: v.Enabled, FFmpeg: v.FFmpeg}
	for _, item := range []struct {
		key   string
		value int
		out   *int
	}{
		{"max_voice_mb", v.MaxVoiceMB, &out.MaxVoiceMB},
		{"max_video_note_mb", v.MaxVideoNoteMB, &out.MaxVideoNoteMB},
		{"max_video_mb", v.MaxVideoMB, &out.MaxVideoMB},
		{"max_video_minutes", v.MaxVideoMinutes, &out.MaxVideoMinutes},
		{"daily_download_mb", v.DailyDownloadMB, &out.DailyDownloadMB},
		{"daily_files", v.DailyFiles, &out.DailyFiles},
	} {
		n, err := ParseVoiceLimit(strconv.Itoa(item.value))
		if err != nil {
			return VoiceTranscriptionValues{}, invalid("voice_transcription.%s %v", item.key, err)
		}
		*item.out = n
	}
	pause, err := ParseVoicePause(v.Pause)
	if err != nil {
		return VoiceTranscriptionValues{}, invalid("voice_transcription.pause %v", err)
	}
	out.Pause = pause
	threads, err := ParseVoiceThreads(strconv.Itoa(v.Threads))
	if err != nil {
		return VoiceTranscriptionValues{}, invalid("voice_transcription.threads %v", err)
	}
	out.Threads = threads
	return out, nil
}
