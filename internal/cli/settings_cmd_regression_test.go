package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/sessionstr"
	"tg-cli/internal/store"
)

func runSettingsFixture(t *testing.T, e *testEnv, args []string, secrets []string, dial func(context.Context, string, string) (*rpc.Client, error)) result {
	t.Helper()
	e.secrets = append([]string(nil), secrets...)
	var out, stderr safeBuffer
	deps := e.deps(&out, &stderr)
	deps.DialRPC = dial
	code := Execute(context.Background(), args, deps)
	if len(e.secrets) != 0 {
		t.Fatalf("%v did not read every expected prompt", args[:2])
	}
	return result{code: code, stdout: out.String(), stderr: stderr.String()}
}

func TestSettingsOfflineRoundTripAndSecretPrompts(t *testing.T) {
	e := newEnv(t)
	phrase := "owner passphrase fixture"
	next := "replacement phrase fixture"
	offline := func(context.Context, string, string) (*rpc.Client, error) { return nil, os.ErrNotExist }
	run := func(args []string, secrets ...string) result {
		t.Helper()
		r := runSettingsFixture(t, e, args, secrets, offline)
		if strings.Contains(r.stdout+r.stderr, phrase) || strings.Contains(r.stdout+r.stderr, next) {
			t.Fatalf("owner phrase leaked for %s %s: %s", args[1], args[2], strings.ReplaceAll(strings.ReplaceAll(r.stdout+r.stderr, phrase, "<redacted>"), next, "<redacted>"))
		}
		return r
	}
	cases := []struct {
		args    []string
		secrets []string
		want    string
	}{
		{[]string{"settings", "list", "--prefix", "behavior."}, nil, `"behavior.throttle_percent"`},
		{[]string{"settings", "get", "behavior.throttle_percent"}, nil, `"value":60`},
		{[]string{"settings", "set", "behavior.throttle_percent", "75"}, nil, `"applied":"next_start"`},
		{[]string{"settings", "get", "behavior.throttle_percent"}, nil, `"value":75`},
		{[]string{"settings", "unset", "behavior.throttle_percent"}, nil, `"value":60`},
		{[]string{"settings", "token", "init"}, []string{phrase, phrase}, `"updated":true`},
		{[]string{"settings", "add", "access.block", "42", "43"}, []string{phrase}, `[42,43]`},
		{[]string{"settings", "get", "access.block"}, []string{phrase}, `[42,43]`},
		{[]string{"settings", "remove", "access.block", "42"}, []string{phrase}, `[43]`},
		{[]string{"settings", "token", "change"}, []string{phrase, next, next}, `"updated":true`},
		{[]string{"settings", "add", "access.block", "42"}, []string{next}, `[42,43]`},
	}
	for _, tc := range cases {
		r := run(tc.args, tc.secrets...)
		if r.code != exitcode.OK || !strings.Contains(r.stdout, tc.want) {
			t.Fatalf("%v: exit %d %s %s", tc.args, r.code, r.stdout, r.stderr)
		}
	}
	for _, tc := range []struct {
		args    []string
		secrets []string
	}{
		{[]string{"settings", "token", "init", phrase}, nil},
		{[]string{"settings", "get", "access.allow", phrase}, nil},
		{[]string{"settings", "set", "access.dialogs", "true", phrase}, nil},
		{[]string{"settings", "token", "init", "--passphrase=" + phrase}, nil},
		{[]string{"settings", "token", "--passphrase=" + phrase, "init"}, nil}, {[]string{"settings", "token", "change", phrase, next}, nil},
		{[]string{"settings", "token", "init"}, []string{phrase, "different phrase"}}, {[]string{"settings", "token", "change"}, []string{next, phrase, "different phrase"}},
	} {
		r := run(tc.args, tc.secrets...)
		if r.code != exitcode.Usage {
			t.Fatalf("expected usage for %v: %d %s", tc.args[:2], r.code, r.stdout)
		}
	}
}

func TestSettingsRequireSecretBeforeConnecting(t *testing.T) {
	e := newEnv(t)
	calls := 0
	dial := func(context.Context, string, string) (*rpc.Client, error) { calls++; return nil, os.ErrNotExist }
	for _, args := range [][]string{{"settings", "get", "access.allow"}, {"settings", "add", "access.write", "42"}, {"settings", "token", "init"}, {"settings", "token", "change"}, {"settings", "token", "reset"}} {
		r := runSettingsFixture(t, e, args, nil, dial)
		if r.code != exitcode.Usage || calls != 0 {
			t.Fatalf("%v: code %d calls %d: %s", args, r.code, calls, r.stdout)
		}
	}
}

func TestSettingsOnlineRPCDispatchAndRefusal(t *testing.T) {
	cases := []struct {
		args    []string
		method  string
		secrets []string
	}{
		{[]string{"settings", "list"}, rpc.MethodSettingsList, nil}, {[]string{"settings", "get", "behavior.throttle_percent"}, rpc.MethodSettingsGet, nil},
		{[]string{"settings", "set", "behavior.throttle_percent", "75"}, rpc.MethodSettingsSet, nil}, {[]string{"settings", "unset", "behavior.throttle_percent"}, rpc.MethodSettingsUnset, nil},
		{[]string{"settings", "add", "access.write", "42"}, rpc.MethodSettingsAdd, []string{"secret phrase fixture"}}, {[]string{"settings", "remove", "access.write", "42"}, rpc.MethodSettingsRemove, []string{"secret phrase fixture"}},
		{[]string{"settings", "token", "init"}, rpc.MethodSettingsTokenInit, []string{"secret phrase fixture", "secret phrase fixture"}},
		{[]string{"settings", "token", "change"}, rpc.MethodSettingsTokenChange, []string{"old secret fixture", "secret phrase fixture", "secret phrase fixture"}},
	}
	for _, tc := range cases {
		for _, denied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/denied=%t", tc.method, denied), func(t *testing.T) {
				e := newEnv(t)
				server, client := net.Pipe()
				srv := rpc.NewServer(nil)
				defer srv.Close()
				received := make(chan json.RawMessage, 1)
				srv.Handle(tc.method, func(_ context.Context, raw json.RawMessage) (any, error) {
					received <- append(json.RawMessage(nil), raw...)
					if denied {
						return nil, rpc.NewError(rpc.CodeWriteDisabled, "operation denied")
					}
					switch tc.method {
					case rpc.MethodSettingsList:
						return rpc.SettingsListResult{Settings: []rpc.SettingValue{}}, nil
					case rpc.MethodSettingsGet:
						return rpc.SettingValue{Key: "behavior.throttle_percent", Value: []byte(`60`)}, nil
					case rpc.MethodSettingsTokenInit, rpc.MethodSettingsTokenChange:
						return rpc.SettingsTokenResult{Updated: true}, nil
					default:
						return rpc.SettingsWriteResult{SettingValue: rpc.SettingValue{Key: "behavior.throttle_percent", Value: []byte(`75`)}}, nil
					}
				})
				done := make(chan struct{})
				go func() { defer close(done); srv.ServeConn(server) }()
				r := runSettingsFixture(t, e, tc.args, tc.secrets, func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(client), nil })
				client.Close()
				<-done
				want := exitcode.OK
				if denied {
					want = exitcode.WriteDisabled
				}
				if r.code != want {
					t.Fatalf("exit %d: %s", r.code, r.stdout)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(<-received, &fields); err != nil {
					t.Fatal(err)
				}
				switch tc.method {
				case rpc.MethodSettingsGet, rpc.MethodSettingsSet, rpc.MethodSettingsUnset, rpc.MethodSettingsAdd, rpc.MethodSettingsRemove:
					var key string
					if err := json.Unmarshal(fields["key"], &key); err != nil || key != tc.args[2] {
						t.Fatalf("wrong RPC key: %q %v", key, err)
					}
				}
				if tc.method == rpc.MethodSettingsSet && string(fields["value"]) != "75" {
					t.Fatalf("wrong set value: %s", fields["value"])
				}
				if (tc.method == rpc.MethodSettingsAdd || tc.method == rpc.MethodSettingsRemove) && string(fields["values"]) != "[42]" {
					t.Fatalf("wrong list values: %s", fields["values"])
				}
				if len(tc.secrets) > 0 && !strings.Contains(string(fields["passphrase"])+string(fields["old_passphrase"])+string(fields["new_passphrase"]), tc.secrets[0]) {
					t.Fatalf("secret not passed to RPC")
				}
				for _, secret := range tc.secrets {
					if strings.Contains(r.stdout+r.stderr, secret) {
						t.Fatal("secret leaked")
					}
				}
			})
		}
	}
}

func TestSettingsTokenResetNeedsConfirmationAndUnlockedProfile(t *testing.T) {
	e := newEnv(t)
	phrase := "owner passphrase fixture"
	offline := func(context.Context, string, string) (*rpc.Client, error) { return nil, os.ErrNotExist }
	r := runSettingsFixture(t, e, []string{"settings", "token", "init"}, []string{phrase, phrase}, offline)
	if r.code != exitcode.OK {
		t.Fatalf("init: %d %s", r.code, r.stdout)
	}
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	readHash := func() string {
		t.Helper()
		db, err := store.Open(paths.Database)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		h, ok, err := db.OwnerSecret(context.Background())
		if err != nil || !ok {
			t.Fatalf("hash missing: %v", err)
		}
		return h
	}
	before := readHash()
	r = runSettingsFixture(t, e, []string{"settings", "token", "reset"}, []string{"no"}, offline)
	if r.code != exitcode.Usage || readHash() != before {
		t.Fatalf("unconfirmed reset: %d %s", r.code, r.stdout)
	}
	err = daemon.WithProfileLock(paths, func() error {
		r = runSettingsFixture(t, e, []string{"settings", "token", "reset"}, []string{"yes"}, offline)
		if r.code != exitcode.Failure || !strings.Contains(r.stdout, "stop the daemon") || strings.Contains(r.stdout, e.home) {
			t.Errorf("locked reset: %d %s", r.code, r.stdout)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readHash() != before {
		t.Fatal("failed reset changed hash")
	}
	releaseDB, err := daemon.LockDatabase(paths)
	if err != nil {
		t.Fatal(err)
	}
	r = runSettingsFixture(t, e, []string{"settings", "token", "reset"}, []string{"yes"}, offline)
	releaseDB()
	if r.code != exitcode.Failure || !strings.Contains(r.stdout, "profile database") || strings.Contains(r.stdout, e.home) {
		t.Fatalf("busy database reset: %d %s", r.code, r.stdout)
	}
	if readHash() != before {
		t.Fatal("busy database reset changed hash")
	}
	r = runSettingsFixture(t, e, []string{"settings", "token", "reset"}, []string{"yes"}, offline)
	if r.code != exitcode.OK || !strings.Contains(r.stdout, "tg settings token init") {
		t.Fatalf("reset: %d %s", r.code, r.stdout)
	}
	r = runSettingsFixture(t, e, []string{"settings", "token", "init"}, []string{phrase, phrase}, offline)
	if r.code != exitcode.OK {
		t.Fatalf("reinit: %d %s", r.code, r.stdout)
	}
}

func TestSettingsSchemaContract(t *testing.T) {
	e := newEnv(t)
	r := e.run("schema")
	if r.code != exitcode.OK {
		t.Fatal(r.stdout)
	}
	var doc schemaDoc
	if err := json.Unmarshal(parse(t, r).Data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 2 || len(doc.Settings) != 30 {
		t.Fatalf("schema version %d settings %d", doc.SchemaVersion, len(doc.Settings))
	}
	for _, key := range doc.Settings {
		definition, ok := config.LookupSetting(key.Key)
		if !ok || key.Type != definition.Type || key.Protected != strings.HasPrefix(key.Key, "access.") {
			t.Fatalf("setting metadata: %+v", key)
		}
	}
	paths := map[string]bool{"settings list": false, "settings get": false, "settings set": false, "settings unset": false, "settings add": false, "settings remove": false, "settings token init": false, "settings token change": false, "settings token reset": false}
	for _, c := range doc.Commands {
		if !strings.HasPrefix(c.Path, "settings ") {
			continue
		}
		if _, ok := paths[c.Path]; !ok {
			t.Errorf("unexpected settings command %s", c.Path)
		}
		paths[c.Path] = true
		want := "local_write"
		if c.Path == "settings list" || c.Path == "settings get" {
			want = "read"
		}
		if c.Safety != want {
			t.Errorf("%s safety %s", c.Path, c.Safety)
		}
	}
	for path, found := range paths {
		if !found {
			t.Errorf("missing settings command %s", path)
		}
	}
}

func TestSettingsLateSourcesRefuseStartAndMigration(t *testing.T) {
	for _, kind := range []string{"access", "config"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			r := e.run("config", "migrate")
			if r.code != exitcode.OK {
				t.Fatalf("initial migration: %d %s", r.code, r.stdout)
			}
			path := filepath.Join(e.home, "default", "access.json")
			source := path
			data := `{"block":[42]}`
			if kind == "config" {
				path = filepath.Join(e.home, ".config", "tg-cli", "config.toml")
				source = "behavior"
				data = "[behavior]\nthrottle_percent = 45\n"
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"config", "migrate"}, {"daemon", "start"}} {
				r = e.run(args...)
				if r.code != exitcode.Usage || !strings.Contains(r.stdout, source) || strings.Contains(strings.ReplaceAll(r.stdout, e.home, "<home>"), "45") {
					t.Fatalf("%v accepted legacy source: %d %s", args, r.code, r.stdout)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != data {
				t.Fatalf("legacy source changed: %v", err)
			}
		})
	}
}

func TestSettingsPersistedBeforeBackgroundLaunch(t *testing.T) {
	e, f := backgroundEnv(t)
	e.usePreset()
	secret := "proxy-secret-fixture"
	proxy := "socks5://user:" + secret + "@proxy.example:1080"
	e.env["TG_CLI_PROXY"] = proxy
	var launched bool
	f.onLaunch = func() {
		launched = true
		p := e.savedProfile("default")
		if p.Client != "desktop" || p.Device != "fixture-device" || p.Transport != "wss" || p.Proxy != proxy {
			t.Errorf("settings missing at launch: client=%q device=%q transport=%q proxy_present=%t", p.Client, p.Device, p.Transport, p.Proxy == proxy)
		}
		f.exited <- nil
	}
	r := e.run("daemon", "start", "--client", "desktop", "--device", "fixture-device", "--transport", "wss")
	if !launched || r.code != exitcode.Failure {
		t.Fatalf("launcher: %t exit=%d", launched, r.code)
	}
	for _, value := range append(append([]string{}, f.args...), r.stdout, r.stderr) {
		if strings.Contains(value, secret) || strings.Contains(value, proxy) {
			t.Fatal("proxy exposed")
		}
	}
}

func TestRunningNetworkComparisonReadsSettingsRPC(t *testing.T) {
	for _, tc := range []struct {
		name, transport, proxy string
		want                   int
	}{
		{"same transport", "wss", "", exitcode.OK}, {"different transport", "tcp", "", exitcode.Usage}, {"explicit proxy", "wss", "socks5://localhost:1080", exitcode.Usage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			cfg, err := config.Load(filepath.Join(e.home, ".config", "tg-cli", "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			var out, stderr safeBuffer
			deps := e.deps(&out, &stderr)
			server, client := net.Pipe()
			srv := rpc.NewServer(nil)
			defer srv.Close()
			var keys []string
			srv.Handle(rpc.MethodStatus, func(context.Context, json.RawMessage) (any, error) {
				return rpc.StatusResult{ConnectionInfo: &rpc.ConnectionInfo{Transport: "tcp"}}, nil
			})
			srv.Handle(rpc.MethodSettingsGet, func(_ context.Context, raw json.RawMessage) (any, error) {
				var p rpc.SettingsGetParams
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				keys = append(keys, p.Key)
				value := json.RawMessage(`"wss"`)
				if p.Key == "profile.proxy" {
					value = json.RawMessage(`"[REDACTED]"`)
				}
				return rpc.SettingValue{Key: p.Key, Value: value}, nil
			})
			done := make(chan struct{})
			go func() { defer close(done); srv.ServeConn(server) }()
			deps.DialRPC = func(context.Context, string, string) (*rpc.Client, error) { return rpc.NewClient(client), nil }
			a := &app{deps: deps, config: cfg, specs: map[*cobra.Command]commandSpec{}}
			root := a.rootCommand()
			cmd, _, err := root.Find([]string{"daemon", "start"})
			if err != nil {
				t.Fatal(err)
			}
			cmd.SetContext(context.Background())
			cmd.Flags().AddFlagSet(root.PersistentFlags())
			if err := cmd.Flags().Set("transport", tc.transport); err != nil {
				t.Fatal(err)
			}
			if tc.proxy != "" {
				if err := cmd.Flags().Set("proxy", tc.proxy); err != nil {
					t.Fatal(err)
				}
			}
			paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			err = a.checkRunningNetwork(cmd, paths)
			client.Close()
			<-done
			if tc.want == exitcode.OK && err != nil {
				t.Fatal(err)
			}
			if tc.want != exitcode.OK {
				var ce *cmdError
				if !errors.As(err, &ce) || ce.exit != tc.want {
					t.Fatalf("expected refusal: %v", err)
				}
			}
			if !reflect.DeepEqual(keys, []string{"profile.transport", "profile.proxy"}) {
				t.Fatalf("settings RPC keys: %v", keys)
			}
		})
	}
}

func TestSessionImportRollsBackPresetWhenSessionWriteFails(t *testing.T) {
	e := newEnv(t)
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Lock, paths.Log} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw := audit02SessionFixture(t, 42)
	encoded, _, err := sessionstr.Encode(raw, "desktop", 0)
	if err != nil {
		t.Fatal(err)
	}
	e.stdin = strings.NewReader(encoded)
	if err := os.Chmod(paths.Dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(paths.Dir, 0700)
	r := e.run("session", "import", "--from", "-", "--yes")
	if r.code != exitcode.Failure || !strings.Contains(r.stdout, "cannot write session") || strings.Contains(r.stdout+r.stderr, encoded) {
		t.Fatalf("failed write: %d %s", r.code, r.stdout)
	}
	if err := os.Chmod(paths.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Session); !os.IsNotExist(err) {
		t.Fatalf("session created: %v", err)
	}
	err = daemon.WithProfileDatabase(paths, func(db daemon.ProfileDatabase) error {
		values, err := db.Settings(context.Background())
		if err != nil {
			return err
		}
		if _, ok := values["profile.client"]; ok {
			t.Fatal("preset was not rolled back")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSettingsTokenResetWhileDaemonRunning(t *testing.T) {
	e := newEnv(t)
	phrase := "owner passphrase fixture"
	offline := func(context.Context, string, string) (*rpc.Client, error) { return nil, os.ErrNotExist }
	r := runSettingsFixture(t, e, []string{"settings", "token", "init"}, []string{phrase, phrase}, offline)
	if r.code != exitcode.OK {
		t.Fatalf("init: %d %s", r.code, r.stdout)
	}
	d := e.startDaemon()
	e.secrets = []string{"yes"}
	r = e.run("settings", "token", "reset")
	if r.code != exitcode.Failure || !strings.Contains(r.stdout, "stop the daemon") || strings.Contains(r.stdout, e.home) {
		t.Fatalf("running reset: %d %s", r.code, r.stdout)
	}
	e.stopDaemon(d)
	e.secrets = []string{phrase, phrase}
	r = e.run("settings", "token", "init")
	if r.code != exitcode.WriteDisabled {
		t.Fatalf("hash changed after refused reset: %d %s", r.code, r.stdout)
	}
}

func TestSettingsResetRejectsLateLegacySources(t *testing.T) {
	for _, kind := range []string{"access", "config"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			phrase := "owner passphrase fixture"
			offline := func(context.Context, string, string) (*rpc.Client, error) { return nil, os.ErrNotExist }
			r := runSettingsFixture(t, e, []string{"settings", "token", "init"}, []string{phrase, phrase}, offline)
			if r.code != exitcode.OK {
				t.Fatalf("init: %d %s", r.code, r.stdout)
			}
			paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
			if err != nil {
				t.Fatal(err)
			}
			source := paths.Access
			content := `{"block":[42]}`
			if kind == "config" {
				source = filepath.Join(e.home, ".config", "tg-cli", "config.toml")
				content = "[behavior]\nthrottle_percent = 45\n"
			}
			if err := os.WriteFile(source, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			r = runSettingsFixture(t, e, []string{"settings", "token", "reset"}, []string{"yes"}, offline)
			wanted := source
			if kind == "config" {
				wanted = "[behavior]"
			}
			if r.code != exitcode.Usage || !strings.Contains(r.stdout, wanted) {
				t.Fatalf("reset accepted late source: %d %s", r.code, r.stdout)
			}
			db, err := store.Open(paths.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, found, err := db.OwnerSecret(context.Background())
			if err != nil || !found {
				t.Fatalf("owner hash removed: %v", err)
			}
		})
	}
}
