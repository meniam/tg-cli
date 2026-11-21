package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func settingsFixture(t *testing.T) (*daemon, *rpc.Client, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.DiscardHandler)
	d := &daemon{store: db, log: log, srv: rpc.NewServer(log)}
	d.ownerSecrets = NewOwnerSecrets(db)
	d.refreshAccess(context.Background())
	d.initSettings(context.Background())
	d.register()
	d.srv.SetRequestGuard(d.guardRequest)
	server, conn := net.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); d.srv.ServeConn(server) }()
	client := rpc.NewClient(conn)
	t.Cleanup(func() { client.Close(); <-done })
	return d, client, db
}
func settingsCode(t *testing.T, err error, want int) {
	t.Helper()
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != want {
		t.Fatalf("error = %v, want code %d", err, want)
	}
}

func TestSettingsRPCOwnerAuthorizationAndPolicy(t *testing.T) {
	d, c, db := settingsFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	d.ownerSecrets.now = func() time.Time { return now }
	phrase := "valid owner passphrase"
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	change := rpc.SettingsSetParams{Key: "access.block", Value: []byte(`[42]`)}
	checkUnchanged := func() {
		t.Helper()
		v, err := db.Settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := v["access.block"]; ok || !d.policy().Allowed(42) {
			t.Fatal("rejected write changed policy")
		}
	}
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsSet, change, nil), rpc.CodeWriteDisabled)
	checkUnchanged()
	change.Passphrase = "wrong"
	for i := 0; i < 5; i++ {
		settingsCode(t, rpcCall(t, c, rpc.MethodSettingsSet, change, nil), rpc.CodeWriteDisabled)
		checkUnchanged()
	}
	change.Passphrase = phrase
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsSet, change, nil), rpc.CodeWriteDisabled)
	checkUnchanged()
	now = now.Add(time.Minute)
	change.Passphrase = "wrong"
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsSet, change, nil), rpc.CodeWriteDisabled)
	checkUnchanged()
	now = now.Add(2 * time.Minute)
	change.Passphrase = phrase
	var result rpc.SettingsWriteResult
	if err := rpcCall(t, c, rpc.MethodSettingsSet, change, &result); err != nil {
		t.Fatal(err)
	}
	if d.policy().Allowed(42) || result.Applied != "live" {
		t.Fatalf("policy/result = %+v", result)
	}
	var hidden rpc.SettingValue
	if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "access.block"}, &hidden); err != nil || string(hidden.Value) != "1" {
		t.Fatalf("hidden block count: %+v %v", hidden, err)
	}
	var list rpc.SettingsListResult
	if err := rpcCall(t, c, rpc.MethodSettingsList, rpc.SettingsListParams{Prefix: "access.block"}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Settings) != 1 || string(list.Settings[0].Value) != "1" || strings.Contains(string(list.Settings[0].Stored), "42") {
		t.Fatalf("list leaked ID: %+v", list)
	}
	var got rpc.SettingValue
	if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "access.block", Passphrase: phrase}, &got); err != nil || string(got.Value) != "[42]" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "behavior.throttle_percent", Value: []byte(`70`)}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsRPCValidationListsAndCorruption(t *testing.T) {
	d, c, db := settingsFixture(t)
	ctx := context.Background()
	phrase := "valid owner passphrase"
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "background_sync.interval_max", Value: []byte(`"1h"`)}, nil); err != nil {
		t.Fatal(err)
	}
	err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "background_sync.interval_min", Value: []byte(`"2h"`)}, nil)
	settingsCode(t, err, rpc.CodeInvalidParams)
	vals, err := db.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vals["background_sync.interval_min"]; ok {
		t.Fatal("invalid value written")
	}
	list := rpc.SettingsListChangeParams{Key: "access.allow", Values: []json.RawMessage{[]byte(`42`), []byte(`-1`), []byte(`42`)}, Passphrase: phrase}
	if err := rpcCall(t, c, rpc.MethodSettingsAdd, list, nil); err != nil {
		t.Fatal(err)
	}
	if !d.policy().Allowed(42) || d.policy().Allowed(99) {
		t.Fatal("added policy not applied")
	}
	list.Values = []json.RawMessage{[]byte(`42`)}
	if err := rpcCall(t, c, rpc.MethodSettingsRemove, list, nil); err != nil {
		t.Fatal(err)
	}
	if d.policy().Allowed(42) || !d.policy().Allowed(-1) {
		t.Fatal("removed policy not applied")
	}
	rawDB, err := sql.Open("sqlite", db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`UPDATE settings SET value='null' WHERE key='access.allow'`); err != nil {
		t.Fatal(err)
	}
	rawDB.Close()
	err = rpcCall(t, c, rpc.MethodSettingsList, rpc.SettingsListParams{}, nil)
	settingsCode(t, err, rpc.CodeInternal)
	if !d.policy().Closed() || d.policy().Allowed(42) {
		t.Fatal("corrupted policy did not close access")
	}
}

func TestSettingsRPCSourcesAndSecret(t *testing.T) {
	d, c, _ := settingsFixture(t)
	d.opts.SettingsOverrides = map[string]SettingOverride{
		"behavior.throttle_percent": {Value: []byte(`35`), Source: "env"},
		"profile.transport":         {Value: []byte(`"wss"`), Source: "flag"},
	}
	var write rpc.SettingsWriteResult
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "behavior.throttle_percent", Value: []byte(`75`)}, &write); err != nil {
		t.Fatal(err)
	}
	if string(write.Value) != "35" || string(write.Stored) != "75" || write.Source != "env" || write.OverriddenBy != "env" {
		t.Fatalf("override = %+v", write)
	}
	var current rpc.SettingValue
	if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "behavior.throttle_percent"}, &current); err != nil || !current.RestartRequired || current.Source != "env" || string(current.Stored) != "75" {
		t.Fatalf("pending override = %+v, %v", current, err)
	}
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "profile.transport", Value: []byte(`"tcp"`)}, &write); err != nil || write.Source != "flag" || write.OverriddenBy != "flag" || string(write.Value) != `"wss"` || string(write.Stored) != `"tcp"` {
		t.Fatalf("flag override = %+v, %v", write, err)
	}
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "profile.proxy", Value: []byte(`"socks5://user:password@localhost:1080"`)}, &write); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(write)
	if strings.Contains(string(data), "password") || strings.Contains(string(data), "localhost") {
		t.Fatalf("proxy leaked: %s", data)
	}
	for _, method := range []string{rpc.MethodSettingsList, rpc.MethodSettingsGet, rpc.MethodSettingsSet, rpc.MethodSettingsUnset, rpc.MethodSettingsAdd, rpc.MethodSettingsRemove, rpc.MethodSettingsTokenInit, rpc.MethodSettingsTokenChange} {
		if needsTelegram(method) || accountMethod(method) {
			t.Errorf("%s needs Telegram or account", method)
		}
	}
}

func TestSettingsRPCRestartValuesRemainAtStartup(t *testing.T) {
	_, c, _ := settingsFixture(t)
	cases := []struct{ name, value, initial string }{
		{"semantic_search.enabled", `true`, `false`}, {"semantic_search.ollama_url", `"http://localhost:1234"`, `"http://127.0.0.1:11434"`},
		{"semantic_search.model", `"another-model"`, `"bge-m3"`}, {"semantic_search.batch", `32`, `16`}, {"semantic_search.interval", `"20m"`, `"10m"`},
		{"profile.client", `"desktop"`, `""`}, {"profile.device", `"test-device"`, `""`}, {"profile.transport", `"wss"`, `"tcp"`},
		{"profile.proxy", `"socks5://localhost:1080"`, `"[REDACTED]"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var result rpc.SettingsWriteResult
			if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: tc.name, Value: []byte(tc.value)}, &result); err != nil {
				t.Fatal(err)
			}
			if !result.RestartRequired || result.Applied != "restart_required" || string(result.Value) != tc.initial {
				t.Fatalf("result = %+v", result)
			}
			if tc.name != "profile.proxy" && string(result.Stored) != tc.value {
				t.Fatalf("stored = %s", result.Stored)
			}
		})
	}
}

func TestSettingsRPCJournalDoesNotLeakValues(t *testing.T) {
	d, c, db := settingsFixture(t)
	logs := &safeBuffer{}
	d.log = slog.New(slog.NewTextHandler(logs, nil))
	phrase := "valid owner passphrase"
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	err := rpcCall(t, c, rpc.MethodSettingsAdd, rpc.SettingsListChangeParams{Key: "access.allow", Values: []json.RawMessage{[]byte(`987654321`)}, Passphrase: phrase}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "profile.proxy", Value: []byte(`"socks5://user:password@localhost:1080"`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hash, found, err := db.OwnerSecret(context.Background())
	if err != nil || !found {
		t.Fatalf("owner hash unavailable: %v", err)
	}
	text := logs.String()
	if strings.Contains(text, hash) {
		t.Fatal("owner hash found in journal")
	}
	if !strings.Contains(text, "access.allow") || !strings.Contains(text, "added=1") || !strings.Contains(text, "source=rpc") {
		t.Fatalf("missing journal data: %s", text)
	}
	for _, secret := range []string{phrase, "987654321", "password", "localhost"} {
		if strings.Contains(text, secret) {
			t.Fatal("secret found in journal")
		}
	}
	settingsCode(t, rpcCall(t, c, "settings.token_reset", struct{}{}, nil), rpc.CodeMethodNotFound)
}

func TestSettingsRPCBatchAtomicAndAuthorized(t *testing.T) {
	d, c, db := settingsFixture(t)
	ctx := context.Background()
	phrase := "valid owner passphrase"
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	invalid := rpc.SettingsSetParams{Changes: map[string]json.RawMessage{"access.block": []byte(`[42]`), "background_sync.interval_min": []byte(`"5h"`)}, Passphrase: phrase}
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsSet, invalid, nil), rpc.CodeInvalidParams)
	values, err := db.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 || !d.policy().Allowed(42) {
		t.Fatalf("partial invalid batch: %v", values)
	}
	valid := rpc.SettingsSetParams{Changes: map[string]json.RawMessage{"access.block": []byte(`[42]`), "background_sync.interval_min": []byte(`"5h"`), "background_sync.interval_max": []byte(`"6h"`)}}
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsSet, valid, nil), rpc.CodeWriteDisabled)
	values, err = db.Settings(ctx)
	if err != nil || len(values) != 0 {
		t.Fatalf("unauthorized batch changed DB: %v, %v", values, err)
	}
	valid.Passphrase = phrase
	var result rpc.SettingsBatchWriteResult
	if err := rpcCall(t, c, rpc.MethodSettingsSet, valid, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Settings) != 3 || d.policy().Allowed(42) {
		t.Fatalf("batch result/policy = %+v", result)
	}
	values, err = db.Settings(ctx)
	if err != nil || len(values) != 3 {
		t.Fatalf("batch DB = %v, %v", values, err)
	}
}

func TestSettingsRPCConcurrentListUpdates(t *testing.T) {
	d, c, db := settingsFixture(t)
	phrase := "valid owner passphrase"
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 4)
	for _, id := range []string{"1", "2", "3", "4"} {
		go func(id string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			results <- c.Call(ctx, rpc.MethodSettingsAdd, rpc.SettingsListChangeParams{Key: "access.block", Values: []json.RawMessage{[]byte(id)}, Passphrase: phrase}, nil)
		}(id)
	}
	for range 4 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	values, err := db.Settings(context.Background())
	if err != nil || string(values["access.block"]) != "[1,2,3,4]" {
		t.Fatalf("concurrent writes = %s, %v", values["access.block"], err)
	}
	for _, id := range []int64{1, 2, 3, 4} {
		if d.policy().Allowed(id) {
			t.Errorf("chat %d was opened", id)
		}
	}
}

func TestSettingsRPCTokenChange(t *testing.T) {
	_, c, _ := settingsFixture(t)
	old := "valid owner passphrase"
	next := "another secure passphrase"
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: old}, nil); err != nil {
		t.Fatal(err)
	}
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: next}, nil), rpc.CodeWriteDisabled)
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsTokenChange, rpc.SettingsTokenChangeParams{OldPassphrase: "wrong", NewPassphrase: next}, nil), rpc.CodeWriteDisabled)
	if err := rpcCall(t, c, rpc.MethodSettingsTokenChange, rpc.SettingsTokenChangeParams{OldPassphrase: old, NewPassphrase: next}, nil); err != nil {
		t.Fatal(err)
	}
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "access.allow", Passphrase: old}, nil), rpc.CodeWriteDisabled)
	if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "access.allow", Passphrase: next}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsRPCUnsetAndListRestrictions(t *testing.T) {
	d, c, _ := settingsFixture(t)
	phrase := "valid owner passphrase"
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "access.allow", Value: []byte(`[42]`), Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsUnset, rpc.SettingsUnsetParams{Key: "access.allow"}, nil), rpc.CodeWriteDisabled)
	if d.policy().Allowed(43) {
		t.Fatal("unauthorized unset opened access")
	}
	settingsCode(t, rpcCall(t, c, rpc.MethodSettingsAdd, rpc.SettingsListChangeParams{Key: "behavior.expect_user", Values: []json.RawMessage{[]byte(`"x"`)}}, nil), rpc.CodeInvalidParams)
	if err := rpcCall(t, c, rpc.MethodSettingsUnset, rpc.SettingsUnsetParams{Key: "access.allow", Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	if !d.policy().Allowed(43) {
		t.Fatal("authorized unset did not restore default")
	}
}

func TestSettingsRPCTokenChangeRefusalMessages(t *testing.T) {
	d, c, _ := settingsFixture(t)
	phrase := "valid owner passphrase"
	next := "another secure passphrase"
	now := time.Unix(1000, 0)
	d.ownerSecrets.now = func() time.Time { return now }
	if err := rpcCall(t, c, rpc.MethodSettingsTokenInit, rpc.SettingsTokenInitParams{Passphrase: phrase}, nil); err != nil {
		t.Fatal(err)
	}
	check := func(old, want string) {
		t.Helper()
		err := rpcCall(t, c, rpc.MethodSettingsTokenChange, rpc.SettingsTokenChangeParams{OldPassphrase: old, NewPassphrase: next}, nil)
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != rpc.CodeWriteDisabled || re.Message != want {
			t.Fatalf("error = %v, want write_disabled %q", err, want)
		}
	}
	check("", "owner passphrase required")
	for range 5 {
		check("wrong", "owner passphrase is wrong")
	}
	check(phrase, "owner passphrase temporarily locked")
}

func TestSettingsWriteReadFailureDeniesExistingPolicy(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch=%t", batch), func(t *testing.T) {
			d, c, db := settingsFixture(t)
			ctx := context.Background()
			if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`)}); err != nil {
				t.Fatal(err)
			}
			d.refreshAccess(ctx)
			if !d.policy().WriteAllowed(42) {
				t.Fatal("initial policy did not permit writes")
			}
			raw, err := sql.Open("sqlite", db.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err := raw.Exec(`CREATE TRIGGER break_policy AFTER INSERT ON settings WHEN NEW.key='behavior.throttle_percent' BEGIN UPDATE settings SET value='null' WHERE key='access.write'; END`); err != nil {
				t.Fatal(err)
			}
			p := rpc.SettingsSetParams{Key: "behavior.throttle_percent", Value: []byte(`70`)}
			if batch {
				p = rpc.SettingsSetParams{Changes: map[string]json.RawMessage{"behavior.throttle_percent": []byte(`70`)}}
			}
			err = rpcCall(t, c, rpc.MethodSettingsSet, p, nil)
			settingsCode(t, err, rpc.CodeInternal)
			if d.policy().WriteAllowed(42) || !d.policy().Closed() {
				t.Fatal("failed policy read left writes enabled")
			}
		})
	}
}

func TestSettingsHideClosedIDsInWriteAndAdmin(t *testing.T) {
	d, c, db := settingsFixture(t)
	ctx := context.Background()
	phrase := "owner passphrase fixture"
	if err := d.ownerSecrets.Init(ctx, phrase); err != nil {
		t.Fatal(err)
	}
	values := map[string]json.RawMessage{"access.allow": []byte(`[1,2,3]`), "access.block": []byte(`[2]`), "access.write": []byte(`[1,2,4]`), "access.admin": []byte(`[1,2,4]`)}
	if err := db.SetSettings(ctx, values); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	for _, key := range []string{"access.write", "access.admin"} {
		var out rpc.SettingValue
		if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: key}, &out); err != nil {
			t.Fatal(err)
		}
		if string(out.Value) != "[1]" || string(out.Stored) != "[1]" {
			t.Errorf("unprotected %s leaked IDs: %+v", key, out)
		}
		if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: key, Passphrase: phrase}, &out); err != nil {
			t.Fatal(err)
		}
		if string(out.Value) != "[1,2,4]" || string(out.Stored) != "[1,2,4]" {
			t.Errorf("authorized %s: %+v", key, out)
		}
	}
	for _, tc := range []struct{ phrase, want string }{{"", "[1]"}, {phrase, "[1,2,4]"}} {
		var out rpc.SettingsListResult
		if err := rpcCall(t, c, rpc.MethodSettingsList, map[string]string{"prefix": "access.", "passphrase": tc.phrase}, &out); err != nil {
			t.Fatal(err)
		}
		for _, v := range out.Settings {
			if v.Key == "access.write" || v.Key == "access.admin" {
				if string(v.Value) != tc.want || string(v.Stored) != tc.want {
					t.Errorf("list %s: %+v", v.Key, v)
				}
			}
		}
	}
	d.accessMu.Lock()
	denied := access.Denied()
	d.accessSnapshot = &denied
	d.accessMu.Unlock()
	var deniedView rpc.SettingValue
	if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: "access.write"}, &deniedView); err != nil {
		t.Fatal(err)
	}
	if string(deniedView.Value) != "[]" || string(deniedView.Stored) != "[]" {
		t.Errorf("denied snapshot leaked IDs: %+v", deniedView)
	}

}

func TestInitSettingsReadFailureClosesExistingSnapshot(t *testing.T) {
	d, _, db := settingsFixture(t)
	ctx := context.Background()
	if err := db.SetSettings(ctx, map[string]json.RawMessage{"access.write": []byte(`[42]`), "behavior.expect_user": []byte(`"42"`)}); err != nil {
		t.Fatal(err)
	}
	d.refreshAccess(ctx)
	if !d.policy().WriteAllowed(42) {
		t.Fatal("fixture policy denied")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d.initSettings(ctx)
	if p := d.policy(); !p.Closed() || p.WriteAllowed(42) {
		t.Fatal("initialization read failure left policy open")
	}
}
