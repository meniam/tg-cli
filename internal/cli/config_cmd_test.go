package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tg-cli/internal/config"
	"tg-cli/internal/exitcode"
)

func TestConfigPathAndShow(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(e.home, ".config", "tg-cli", "config.toml")
	if r := e.run("config", "path"); r.code != exitcode.OK || !strings.Contains(r.stdout, path) {
		t.Fatalf("path: %d %s", r.code, r.stdout)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "[profiles.default]\nclient = 'desktop'\nproxy = 'socks5://alice:SECRET123@localhost:1080'\n[profiles.other]\nproxy = 'tg://proxy?server=localhost&port=443&secret=0123456789abcdef0123456789abcdef'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("config", "show")
	if r.code != exitcode.OK {
		t.Fatalf("show: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout+r.stderr, "SECRET123") || strings.Contains(r.stdout, "alice") || strings.Contains(r.stdout+r.stderr, "0123456789abcdef0123456789abcdef") {
		t.Fatal("proxy credentials leaked")
	}
	var got configShowResult
	if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.DefaultProfile != "default" || got.DBDir != e.dbRoot() {
		t.Fatalf("unexpected config: %+v", got)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(parse(t, r).Data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("unexpected config fields: %v", fields)
	}
	r = e.run("config", "migrate")
	if r.code != exitcode.OK {
		t.Fatalf("migrate: %d %s", r.code, r.stdout)
	}
	if got := e.savedProfile("default"); got.Client != "desktop" || got.Proxy != "socks5://alice:SECRET123@localhost:1080" {
		t.Fatalf("settings not migrated: %+v", got)
	}
	if value := string(e.setting("profile.proxy")); value != `"[REDACTED]"` {
		t.Fatalf("proxy not redacted: %s", value)
	}
}
func TestConfigMigrateAndShow(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.home, "default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client"), []byte("desktop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("config", "migrate")
	if r.code != exitcode.OK {
		t.Fatalf("migration: %d %s", r.code, r.stdout)
	}
	var result configMigrateResult
	if err := json.Unmarshal(parse(t, r).Data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Migrated || result.Backup == "" || len(result.Warnings) != 0 {
		t.Fatalf("result: %+v", result)
	}
	r = e.run("config", "migrate")
	if err := json.Unmarshal(parse(t, r).Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Migrated {
		t.Fatal("second migration changed files")
	}
	if err := os.WriteFile(filepath.Join(dir, "device"), []byte("Laptop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = e.run("config", "show")
	if r.code != exitcode.OK {
		t.Fatalf("show: %d %s", r.code, r.stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "device")); err != nil {
		t.Fatalf("show changed legacy file: %v", err)
	}
	r = e.run("config", "migrate")
	if r.code != exitcode.Usage || !strings.Contains(r.stdout, "device") {
		t.Fatalf("late legacy file: %d %s", r.code, r.stdout)
	}
	if got := e.savedProfile("default").Client; got != "desktop" {
		t.Fatalf("migrated client changed: %q", got)
	}
}
func TestConfigShowChecksAllSocketPaths(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(e.home, ".config", "tg-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[profiles."+strings.Repeat("a", 32)+"]\nclient = 'desktop'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.env["TG_CLI_HOME"] = "/" + strings.Repeat("a", 70)
	if r := e.run("config", "show"); r.code != exitcode.Usage || !strings.Contains(r.stdout, "socket path") {
		t.Fatalf("exit %d: %s", r.code, r.stdout)
	}
}
func TestSchemaConfigKeys(t *testing.T) {
	e := newEnv(t)
	r := e.run("schema")
	if r.code != exitcode.OK {
		t.Fatalf("schema: %d", r.code)
	}
	var doc schemaDoc
	if err := json.Unmarshal(parse(t, r).Data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 4 {
		t.Fatalf("schema version %d", doc.SchemaVersion)
	}
	var keys []string
	for _, item := range doc.Config {
		keys = append(keys, item.Key)
	}
	wantKeys := []string{
		"home", "default_profile",
		"files.database", "files.session", "files.log", "files.deletions", "files.access",
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("config schema keys:\n got %q\nwant %q", keys, wantKeys)
	}
	expected := map[string]bool{}
	for _, key := range config.SettingKeys() {
		expected[key.Name] = true
	}
	for _, item := range doc.Settings {
		if !expected[item.Key] {
			t.Errorf("unexpected setting %s", item.Key)
		}
		delete(expected, item.Key)
		if item.Key == "behavior.expect_user" && (item.Environment != "TG_CLI_EXPECT_USER" || item.Flag != "--expect-user") {
			t.Fatal("expect_user overrides missing")
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing settings: %v", expected)
	}
}

func TestSchemaConfigCommands(t *testing.T) {
	commands := schemaCommands(t)
	for path, want := range map[string]string{"config path": "read", "config show": "local_write", "config migrate": "local_write"} {
		cmd, ok := commands[path]
		if !ok || cmd.Safety != want || len(cmd.Fields) == 0 {
			t.Errorf("%s: %+v", path, cmd)
		}
	}
}

// TestConfigShowNamesTheDatabaseDirectory covers Q5: home no longer says where
// the history lives, so the answer names the database directory next to the
// configuration file. The default home is that same directory, so the test sets
// a home elsewhere, as an owner who moves the profile data does.
func TestConfigShowNamesTheDatabaseDirectory(t *testing.T) {
	e := newEnv(t)
	configPath := filepath.Join(e.home, ".config", "tg-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("home = '"+filepath.Join(e.home, "profiles")+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("config", "show")
	if r.code != exitcode.OK {
		t.Fatalf("show: %d %s", r.code, r.stdout)
	}
	var got configShowResult
	if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.DBDir != e.dbRoot() {
		t.Errorf("db_dir = %q, want %q", got.DBDir, e.dbRoot())
	}
	if strings.HasPrefix(got.DBDir, got.Home) {
		t.Errorf("the database directory must not follow home: %q under %q", got.DBDir, got.Home)
	}
}

// TestConfigInit writes the template once and refuses to replace it: the owner
// keeps their edits.
func TestConfigInit(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(e.home, ".config", "tg-cli", "config.toml")
	r := e.run("config", "init")
	if r.code != exitcode.OK {
		t.Fatalf("init: %d %s %s", r.code, r.stdout, r.stderr)
	}
	var got configInitResult
	if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Created || got.Path != path {
		t.Fatalf("init answer: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the config file: mode %o, want 0600", info.Mode().Perm())
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != config.Template() {
		t.Error("the file is not the template")
	}
	if r := e.run("config", "init"); r.code != exitcode.Usage || !strings.Contains(r.stdout, "already exists") {
		t.Fatalf("second init: %d %q", r.code, r.stdout)
	}
	again, err := os.ReadFile(path)
	if err != nil || string(again) != string(written) {
		t.Fatalf("the refused init changed the file: %v", err)
	}
}

// TestConfigTemplateCoversSchema keeps the template and `tg schema` in step: a key
// the tool understands must have its documented line in the template.
func TestConfigTemplateCoversSchema(t *testing.T) {
	e := newEnv(t)
	r := e.run("schema")
	if r.code != exitcode.OK {
		t.Fatalf("schema: %d", r.code)
	}
	var doc schemaDoc
	if err := json.Unmarshal(parse(t, r).Data, &doc); err != nil {
		t.Fatal(err)
	}
	template := config.Template()
	for _, item := range doc.Config {
		// The schema names a key by its full path, the template by the leaf
		// inside its table: files.database is `# database = ` under [files],
		// profiles.<profile>.device is `# device = ` under [profiles.default].
		table, leaf := "", item.Key
		if parent, rest, ok := strings.Cut(item.Key, "."); ok {
			table, leaf = parent, rest
			if parent == "profiles" {
				table, leaf = "profiles.default", strings.TrimPrefix(rest, "<profile>.")
			}
		}
		if table != "" && !strings.Contains(template, "["+table+"]") {
			t.Errorf("the template has no [%s] table for the key %q", table, item.Key)
		}
		if !strings.Contains(template, "# "+leaf+" = ") {
			t.Errorf("the template has no line for the key %q", item.Key)
		}
	}
}
