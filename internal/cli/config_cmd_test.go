package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if got.Profiles["default"].Proxy != "socks5://localhost:1080" || got.Profiles["default"].Client != "desktop" {
		t.Fatalf("unexpected profile: %+v", got.Profiles["default"])
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
	var got configShowResult
	if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Profiles["default"].Device != "Laptop" {
		t.Fatal("show did not migrate profile")
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
	if doc.SchemaVersion != 1 || len(doc.Config) != 14 {
		t.Fatalf("config schema: %+v", doc.Config)
	}
	for _, item := range doc.Config {
		if item.Key == "behavior.expect_user" && item.Environment == "TG_CLI_EXPECT_USER" && item.Flag == "--expect-user" {
			return
		}
	}
	t.Fatal("expect_user config schema missing")
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
// configuration file.
func TestConfigShowNamesTheDatabaseDirectory(t *testing.T) {
	e := newEnv(t)
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
