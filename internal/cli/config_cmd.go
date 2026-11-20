package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/netconf"
)

type configPathResult struct {
	Path string `json:"path"`
}
type configFilesResult struct {
	Database  string `json:"database"`
	Session   string `json:"session"`
	Log       string `json:"log"`
	Deletions string `json:"deletions"`
	Access    string `json:"access"`
}
type configBehaviorResult struct {
	ThrottlePercent float64 `json:"throttle_percent"`
	FloodAutoWait   string  `json:"flood_autowait"`
	ExpectUser      string  `json:"expect_user"`
}
type configBackgroundSyncResult struct {
	Enabled      bool   `json:"enabled"`
	WindowStart  string `json:"window_start"`
	WindowEnd    string `json:"window_end"`
	IntervalMin  string `json:"interval_min"`
	IntervalMax  string `json:"interval_max"`
	PagePauseMin string `json:"page_pause_min"`
	PagePauseMax string `json:"page_pause_max"`
}
type configSemanticSearchResult struct {
	Enabled   bool   `json:"enabled"`
	OllamaURL string `json:"ollama_url"`
	Model     string `json:"model"`
	Batch     int    `json:"batch"`
	Interval  string `json:"interval"`
}
type configProfileResult struct {
	Client    string `json:"client"`
	Device    string `json:"device"`
	Transport string `json:"transport"`
	Proxy     string `json:"proxy"`
}
type configShowResult struct {
	Home string `json:"home"`
	// DBDir is the directory of the profile databases next to the configuration
	// file. home no longer says where the history lives.
	DBDir          string                         `json:"db_dir"`
	DefaultProfile string                         `json:"default_profile"`
	Files          configFilesResult              `json:"files"`
	Behavior       configBehaviorResult           `json:"behavior"`
	BackgroundSync configBackgroundSyncResult     `json:"background_sync"`
	SemanticSearch configSemanticSearchResult     `json:"semantic_search"`
	Profiles       map[string]configProfileResult `json:"profiles"`
}
type configInitResult struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
}
type configMigrateResult struct {
	Migrated bool     `json:"migrated"`
	Backup   string   `json:"backup,omitempty"`
	Warnings []string `json:"warnings"`
}

func (a *app) configCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect and migrate configuration"}
	cmd.AddCommand(a.annotate(&cobra.Command{Use: "path", Short: "Print the configuration file path", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return a.success(cmd, configPathResult{Path: a.deps.ConfigPath})
	}}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[configPathResult]()}))
	cmd.AddCommand(a.annotate(&cobra.Command{Use: "show", Short: "Show configuration with proxy credentials redacted", Args: cobra.NoArgs, RunE: a.configShow}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[configShowResult]()}))
	cmd.AddCommand(a.annotate(&cobra.Command{Use: "init", Short: "Write the configuration template", Args: cobra.NoArgs, RunE: a.configInit}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[configInitResult]()}))
	cmd.AddCommand(a.annotate(&cobra.Command{Use: "migrate", Short: "Migrate legacy profile settings into the configuration", Args: cobra.NoArgs, RunE: a.configMigrate}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[configMigrateResult]()}))
	return cmd
}

// configInit writes the template to the configuration path. An existing file is
// left alone: the template must not overwrite the edits of the owner.
func (a *app) configInit(cmd *cobra.Command, _ []string) error {
	err := config.Init(a.deps.ConfigPath)
	if errors.Is(err, config.ErrConfigExists) {
		return fail(exitcode.Usage, "%s already exists: edit it instead of writing a new template", a.deps.ConfigPath)
	}
	if err != nil {
		return fail(exitcode.Failure, "%v", err)
	}
	return a.success(cmd, configInitResult{Path: a.deps.ConfigPath, Created: true})
}

func (a *app) configShow(cmd *cobra.Command, _ []string) error {
	if err := a.migrateProfiles(); err != nil {
		return err
	}
	home := a.config.Home
	if env := a.deps.Getenv("TG_CLI_HOME"); env != "" {
		home = env
	}
	names := make([]string, 0, len(a.config.Profiles)+1)
	names = append(names, a.config.DefaultProfile)
	for name := range a.config.Profiles {
		if name != a.config.DefaultProfile {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	files := a.config.Files
	fileNames := daemon.FileNames{Database: files.Database, Session: files.Session, Log: files.Log, Deletions: files.Deletions, Access: files.Access}
	// The database root sits next to the configuration file.
	dbRoot := filepath.Join(filepath.Dir(a.deps.ConfigPath), "db")
	for _, name := range names {
		if _, err := daemon.ResolvePaths(home, dbRoot, name, fileNames); err != nil {
			return fail(exitcode.Usage, "%v", err)
		}
	}
	result := configShowResult{Home: a.config.Home, DBDir: dbRoot, DefaultProfile: a.config.DefaultProfile, Files: configFilesResult{files.Database, files.Session, files.Log, files.Deletions, files.Access}, Behavior: configBehaviorResult{a.config.Behavior.ThrottlePercent, a.config.Behavior.FloodAutoWait, a.config.Behavior.ExpectUser}, BackgroundSync: configBackgroundSyncResult(a.config.BackgroundSync), SemanticSearch: configSemanticSearchResult(a.config.SemanticSearch), Profiles: map[string]configProfileResult{}}
	for name, p := range a.config.Profiles {
		proxy, err := netconf.ParseProxy(p.Proxy)
		if err != nil {
			return fail(exitcode.Usage, "invalid profile proxy")
		}
		result.Profiles[name] = configProfileResult{p.Client, p.Device, p.Transport, proxy.Redacted()}
	}
	return a.success(cmd, result)
}
func (a *app) configMigrate(cmd *cobra.Command, _ []string) error {
	home := a.config.Home
	if env := a.deps.Getenv("TG_CLI_HOME"); env != "" {
		home = env
	}
	result, err := config.MigrateProfiles(a.deps.ConfigPath, home)
	if err != nil {
		var invalid *config.Error
		if errors.As(err, &invalid) {
			return fail(exitcode.Usage, "%v", err)
		}
		return fail(exitcode.Failure, "migrate profile settings: %v", err)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(a.deps.Stderr, "warning: "+warning)
	}
	warnings := result.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return a.success(cmd, configMigrateResult{Migrated: result.Backup != "", Backup: result.Backup, Warnings: warnings})
}
