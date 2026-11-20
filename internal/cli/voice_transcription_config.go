package cli

import (
	"strings"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// voiceTranscriptionEnv names, per config key, the environment variable that
// overrides it.
var voiceTranscriptionEnv = struct {
	enabled, ffmpeg, maxVoiceMB, maxVideoNoteMB, maxVideoMB, maxVideoMinutes, dailyDownloadMB, dailyFiles, pause, threads string
}{
	"TG_CLI_VOICE_TRANSCRIPTION_ENABLED",
	"TG_CLI_VOICE_TRANSCRIPTION_FFMPEG",
	"TG_CLI_VOICE_TRANSCRIPTION_MAX_VOICE_MB",
	"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_NOTE_MB",
	"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MB",
	"TG_CLI_VOICE_TRANSCRIPTION_MAX_VIDEO_MINUTES",
	"TG_CLI_VOICE_TRANSCRIPTION_DAILY_DOWNLOAD_MB",
	"TG_CLI_VOICE_TRANSCRIPTION_DAILY_FILES",
	"TG_CLI_VOICE_TRANSCRIPTION_PAUSE",
	"TG_CLI_VOICE_TRANSCRIPTION_THREADS",
}

// voiceTranscriptionSettings is the only parser for the settings of the local
// voice transcription: config.toml first, then the TG_CLI_VOICE_TRANSCRIPTION_*
// environment over it. The background parent validates the result; the
// foreground process parses the inherited environment again, as it does for the
// background sync.
func (a *app) voiceTranscriptionSettings() (config.VoiceTranscription, config.VoiceTranscriptionValues, error) {
	settings := a.config.VoiceTranscription
	overridden := false
	ints := func(name string, parse func(string) (int, error), target *int) (bool, error) {
		value := strings.TrimSpace(a.deps.Getenv(name))
		if value == "" {
			return false, nil
		}
		n, err := parse(value)
		if err != nil {
			return false, fail(exitcode.Usage, "%s %v", name, err)
		}
		*target = n
		return true, nil
	}
	for _, item := range []struct {
		name   string
		parse  func(string) (int, error)
		target *int
	}{
		{voiceTranscriptionEnv.maxVoiceMB, config.ParseVoiceLimit, &settings.MaxVoiceMB},
		{voiceTranscriptionEnv.maxVideoNoteMB, config.ParseVoiceLimit, &settings.MaxVideoNoteMB},
		{voiceTranscriptionEnv.maxVideoMB, config.ParseVoiceLimit, &settings.MaxVideoMB},
		{voiceTranscriptionEnv.maxVideoMinutes, config.ParseVoiceLimit, &settings.MaxVideoMinutes},
		{voiceTranscriptionEnv.dailyDownloadMB, config.ParseVoiceLimit, &settings.DailyDownloadMB},
		{voiceTranscriptionEnv.dailyFiles, config.ParseVoiceLimit, &settings.DailyFiles},
		{voiceTranscriptionEnv.threads, config.ParseVoiceThreads, &settings.Threads},
	} {
		set, err := ints(item.name, item.parse, item.target)
		if err != nil {
			return config.VoiceTranscription{}, config.VoiceTranscriptionValues{}, err
		}
		overridden = overridden || set
	}
	if value := strings.TrimSpace(a.deps.Getenv(voiceTranscriptionEnv.ffmpeg)); value != "" {
		if err := config.CheckVoiceFFmpeg(value); err != nil {
			return config.VoiceTranscription{}, config.VoiceTranscriptionValues{}, fail(exitcode.Usage, "%s %v", voiceTranscriptionEnv.ffmpeg, err)
		}
		settings.FFmpeg = value
		overridden = true
	}
	if value := strings.TrimSpace(a.deps.Getenv(voiceTranscriptionEnv.pause)); value != "" {
		if _, err := config.ParseVoicePause(value); err != nil {
			return config.VoiceTranscription{}, config.VoiceTranscriptionValues{}, fail(exitcode.Usage, "%s %v", voiceTranscriptionEnv.pause, err)
		}
		settings.Pause = value
		overridden = true
	}
	switch value := strings.TrimSpace(a.deps.Getenv(voiceTranscriptionEnv.enabled)); value {
	case "":
	case "true", "false":
		settings.Enabled = value == "true"
		overridden = true
	default:
		return config.VoiceTranscription{}, config.VoiceTranscriptionValues{}, fail(exitcode.Usage, "%s must be true or false", voiceTranscriptionEnv.enabled)
	}
	values, err := settings.Resolve()
	if err != nil {
		if overridden {
			return config.VoiceTranscription{}, config.VoiceTranscriptionValues{}, fail(exitcode.Usage, "%v (after the TG_CLI_VOICE_TRANSCRIPTION_* overrides)", err)
		}
		return config.VoiceTranscription{}, config.VoiceTranscriptionValues{}, fail(exitcode.Usage, "%v", err)
	}
	return settings, values, nil
}

// daemonVoiceTranscription turns the checked settings into the daemon's type.
func (a *app) daemonVoiceTranscription() (daemon.VoiceTranscriptionConfig, error) {
	_, v, err := a.voiceTranscriptionSettings()
	if err != nil {
		return daemon.VoiceTranscriptionConfig{}, err
	}
	return daemon.VoiceTranscriptionConfig{
		Enabled: v.Enabled, Home: a.voiceHome(), FFmpeg: v.FFmpeg,
		MaxVoiceMB: v.MaxVoiceMB, MaxVideoNoteMB: v.MaxVideoNoteMB, MaxVideoMB: v.MaxVideoMB, MaxVideoMinutes: v.MaxVideoMinutes,
		DailyDownloadMB: v.DailyDownloadMB, DailyFiles: v.DailyFiles, Pause: v.Pause, Threads: v.Threads,
	}, nil
}

// voiceHome is the base directory of the profile data, where the models live:
// TG_CLI_HOME over the home of the configuration, as for the profile paths.
func (a *app) voiceHome() string {
	if home := a.deps.Getenv("TG_CLI_HOME"); home != "" {
		return home
	}
	return a.config.Home
}
