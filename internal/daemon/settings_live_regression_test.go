package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/throttle"
)

func TestLiveBackgroundSettingsAffectNextCalculation(t *testing.T) {
	d, c, _ := settingsFixture(t)
	initial := BackgroundSyncConfig{Enabled: true, WindowStart: 8 * time.Hour, WindowEnd: 20 * time.Hour, IntervalMin: 40 * time.Minute, IntervalMax: 3 * time.Hour, PagePauseMin: 2 * time.Second, PagePauseMax: 8 * time.Second}
	d.opts.BackgroundSync = initial
	var lo, hi time.Duration
	d.background = newBackgroundSync(d, initial, nil, func(a, b time.Duration) time.Duration { lo, hi = a, b; return a })
	set := func(key, value string) {
		t.Helper()
		var out rpc.SettingsWriteResult
		if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: key, Value: json.RawMessage(value)}, &out); err != nil || out.Applied != "live" {
			t.Fatalf("set %s: %+v %v", key, out, err)
		}
	}
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	set("background_sync.window_start", `"09:00"`)
	if got := d.background.scheduleFrom(day.Add(7 * time.Hour)); !got.Equal(day.Add(9 * time.Hour)) {
		t.Fatalf("start window: %v", got)
	}
	set("background_sync.window_end", `"21:00"`)
	if got := d.background.scheduleFrom(day.Add(20 * time.Hour)); !got.Equal(day.Add(20*time.Hour + 40*time.Minute)) {
		t.Fatalf("end window: %v", got)
	}
	set("background_sync.interval_min", `"50m"`)
	d.background.scheduleFrom(day.Add(10 * time.Hour))
	if lo != 50*time.Minute || hi != 3*time.Hour {
		t.Fatalf("interval min: %v %v", lo, hi)
	}
	set("background_sync.interval_max", `"2h"`)
	d.background.scheduleFrom(day.Add(10 * time.Hour))
	if lo != 50*time.Minute || hi != 2*time.Hour {
		t.Fatalf("interval max: %v %v", lo, hi)
	}
	set("background_sync.page_pause_min", `"3s"`)
	d.background.pagePause()
	if lo != 3*time.Second || hi != 8*time.Second {
		t.Fatalf("pause min: %v %v", lo, hi)
	}
	set("background_sync.page_pause_max", `"6s"`)
	d.background.pagePause()
	if lo != 3*time.Second || hi != 6*time.Second {
		t.Fatalf("pause max: %v %v", lo, hi)
	}
	var out rpc.SettingsWriteResult
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "background_sync.enabled", Value: []byte(`false`)}, &out); err != nil || !out.RestartRequired || out.Applied != "restart_required" {
		t.Fatalf("enabled: %+v %v", out, err)
	}
	if !d.background.config().Enabled {
		t.Fatal("scheduler state changed before restart")
	}
}

func TestLiveExpectedAccountAppliesOnNextRequest(t *testing.T) {
	d, c, _ := settingsFixture(t)
	d.account.set(&rpc.User{ID: 42, Username: "owner"})
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "behavior.expect_user", Value: []byte(`"43"`)}, nil); err != nil {
		t.Fatal(err)
	}
	req := rpc.Request{Method: rpc.MethodMessageHistory}
	settingsCode(t, d.guardRequest(req), rpc.CodeNotAuthorized)
	req.ExpectUser = "42"
	if err := d.guardRequest(req); err != nil {
		t.Fatalf("request override: %v", err)
	}
	if err := rpcCall(t, c, rpc.MethodSettingsSet, rpc.SettingsSetParams{Key: "behavior.expect_user", Value: []byte(`"42"`)}, nil); err != nil {
		t.Fatal(err)
	}
	req.ExpectUser = ""
	if err := d.guardRequest(req); err != nil {
		t.Fatalf("updated account: %v", err)
	}
}

func TestLiveSemanticThresholdAffectsNextSearch(t *testing.T) {
	e := newSemanticEnv(t)
	withGradedFragments(t, e)
	e.d.opts.SemanticSearch.MinSimilarity = 0.5
	before := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Limit: 100})
	raw, _ := json.Marshal(rpc.SettingsSetParams{Key: "semantic_search.min_similarity", Value: []byte(`0.7`)})
	if _, err := e.d.settingsSet(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	after := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Limit: 100})
	if before.VectorDropped != 2 || after.VectorDropped != 3 {
		t.Fatalf("threshold change: before=%d after=%d", before.VectorDropped, after.VectorDropped)
	}
	override := 0.3
	result := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Limit: 100, MinSimilarity: &override})
	if result.VectorDropped != 1 {
		t.Fatalf("request override: %d", result.VectorDropped)
	}
}

func TestLiveThrottleSettingsAffectQueuedRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		cfg := throttle.DefaultConfig()
		cfg.GeneralBurst = 1
		cfg.GeneralPerSecond = 30
		limiter, err := throttle.New(cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		d := &daemon{store: db, opts: Options{TelegramConfig: TelegramConfig{Throttle: limiter}}}
		set := func(key, value string) rpc.SettingsWriteResult {
			t.Helper()
			raw, _ := json.Marshal(rpc.SettingsSetParams{Key: key, Value: []byte(value)})
			v, err := d.settingsSet(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			return v.(rpc.SettingsWriteResult)
		}
		if got := set("behavior.throttle_percent", "1"); got.Applied != "live" {
			t.Fatalf("rate update: %+v", got)
		}
		p, err := limiter.Acquire(context.Background(), throttle.General, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
		d.opts.SettingsOverrides = map[string]SettingOverride{"behavior.throttle_percent": {Value: []byte(`1`), Source: "env"}}
		if got := set("behavior.throttle_percent", "90"); got.OverriddenBy != "env" || string(got.Value) != "1" {
			t.Fatalf("override: %+v", got)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if p, err = limiter.Acquire(ctx, throttle.General, "", nil); err == nil {
			p.Release()
			t.Fatal("overridden rate changed limiter")
		}
		d.opts.SettingsOverrides = nil
		if got := set("behavior.throttle_percent", "100"); got.Applied != "live" {
			t.Fatalf("rate update: %+v", got)
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel2()
		p, err = limiter.Acquire(ctx2, throttle.General, "", nil)
		if err != nil {
			t.Fatalf("new rate did not apply: %v", err)
		}
		p.Release()
		limiter.PauseBackground(10 * time.Second)
		done := make(chan error, 1)
		queuedCtx, cancelQueued := context.WithCancel(context.Background())
		defer cancelQueued()
		go func() {
			p, err := limiter.Acquire(queuedCtx, throttle.General, "", nil)
			if err == nil {
				p.Release()
			}
			done <- err
		}()
		synctest.Wait()
		if got := set("behavior.flood_autowait", `"3s"`); got.Applied != "live" {
			t.Fatalf("flood update: %+v", got)
		}
		synctest.Wait()
		select {
		case err := <-done:
			var flood *throttle.FloodWaitError
			if !errors.As(err, &flood) {
				t.Fatalf("queued flood request: %v", err)
			}
		default:
			t.Fatal("flood request did not wake")
		}
	})
}

func TestNewDefaultsKeepStoredOptOuts(t *testing.T) {
	d, c, db := settingsFixture(t)
	ctx := context.Background()
	settings := map[string]json.RawMessage{"background_sync.enabled": []byte(`false`), "semantic_search.enabled": []byte(`false`), "profile.client": []byte(`"ios"`)}
	if err := db.SetSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := d.initSettings(ctx); err != nil {
		t.Fatal(err)
	}
	for key, want := range settings {
		var got rpc.SettingValue
		if err := rpcCall(t, c, rpc.MethodSettingsGet, rpc.SettingsGetParams{Key: key}, &got); err != nil {
			t.Fatal(err)
		}
		if got.Source != "database" || string(got.Value) != string(want) || string(got.Stored) != string(want) {
			t.Fatalf("stored %s changed: %+v", key, got)
		}
	}
}
