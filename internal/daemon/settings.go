package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/config"
	"tg-cli/internal/rpc"
)

// SettingOverride is an effective value supplied at daemon startup.
type SettingOverride struct {
	Value  json.RawMessage
	Source string
}

// validateSettingOverrides detaches and validates startup values before use.
func validateSettingOverrides(input map[string]SettingOverride) (map[string]SettingOverride, error) {
	out := make(map[string]SettingOverride, len(input))
	for name, override := range input {
		key, ok := config.LookupSetting(name)
		if !ok || (override.Source == "env" && key.Env == "") || (override.Source == "flag" && key.Flag == "") || (override.Source != "env" && override.Source != "flag") {
			return nil, errors.New("invalid settings override")
		}
		value, err := config.ParseSettingJSON(name, override.Value)
		if err != nil {
			return nil, errors.New("invalid settings override")
		}
		out[name] = SettingOverride{Value: value, Source: override.Source}
	}
	return out, nil
}

// liveSetting returns a detached value already applied to a live component.
func (d *daemon) liveSetting(name string) (json.RawMessage, bool) {
	d.liveMu.RLock()
	defer d.liveMu.RUnlock()
	value, ok := d.liveValues[name]
	return bytes.Clone(value), ok
}

func (d *daemon) backgroundConfig() BackgroundSyncConfig {
	d.liveMu.RLock()
	defer d.liveMu.RUnlock()
	if d.liveBackground != nil {
		return *d.liveBackground
	}
	return d.opts.BackgroundSync
}

// applyBackgroundSettings publishes related scheduler values together.
func (d *daemon) applyBackgroundSettings(names []string, stored map[string]json.RawMessage) {
	changed := false
	for _, name := range names {
		if strings.HasPrefix(name, "background_sync.") && name != "background_sync.enabled" {
			if _, overridden := d.opts.SettingsOverrides[name]; !overridden {
				changed = true
				break
			}
		}
	}
	if !changed {
		return
	}
	cfg := d.backgroundConfig()
	for _, name := range []string{
		"background_sync.window_start", "background_sync.window_end",
		"background_sync.interval_min", "background_sync.interval_max",
		"background_sync.page_pause_min", "background_sync.page_pause_max",
	} {
		key, _ := config.LookupSetting(name)
		value := key.Default
		if v, ok := stored[name]; ok {
			value = v
		}
		if override, ok := d.opts.SettingsOverrides[name]; ok {
			value = override.Value
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			continue
		}
		var duration time.Duration
		var err error
		if key.Type == "clock" {
			duration, err = config.ParseClock(text)
		} else {
			duration, err = config.ParsePositiveDuration(text)
		}
		if err != nil {
			continue
		}
		switch name {
		case "background_sync.window_start":
			cfg.WindowStart = duration
		case "background_sync.window_end":
			cfg.WindowEnd = duration
		case "background_sync.interval_min":
			cfg.IntervalMin = duration
		case "background_sync.interval_max":
			cfg.IntervalMax = duration
		case "background_sync.page_pause_min":
			cfg.PagePauseMin = duration
		case "background_sync.page_pause_max":
			cfg.PagePauseMax = duration
		}
	}
	d.liveMu.Lock()
	d.liveBackground = &cfg
	d.liveMu.Unlock()
	if d.background != nil {
		d.background.setConfig(cfg)
	}
}

// applyLiveSetting is called under settingsMu after the database commit.
func (d *daemon) applyLiveSetting(key config.SettingKey, stored map[string]json.RawMessage) bool {
	if key.Protected {
		return true
	}
	if key.Apply != "live" {
		return false
	}
	value := key.Default
	if v, ok := stored[key.Name]; ok {
		value = v
	}
	if override, ok := d.opts.SettingsOverrides[key.Name]; ok {
		value = override.Value
	}
	switch key.Name {
	case "behavior.throttle_percent":
		if d.opts.TelegramConfig.Throttle == nil {
			return false
		}
		var n float64
		if json.Unmarshal(value, &n) != nil || d.opts.TelegramConfig.Throttle.SetPercent(n) != nil {
			return false
		}
	case "behavior.flood_autowait":
		if d.opts.TelegramConfig.Throttle == nil {
			return false
		}
		var v string
		if json.Unmarshal(value, &v) != nil {
			return false
		}
		duration, err := time.ParseDuration(v)
		if err != nil || d.opts.TelegramConfig.Throttle.SetFloodAutoWait(duration) != nil {
			return false
		}
	case "semantic_search.min_similarity", "behavior.expect_user":
	case "background_sync.window_start", "background_sync.window_end", "background_sync.interval_min", "background_sync.interval_max", "background_sync.page_pause_min", "background_sync.page_pause_max":
	default:
		return false
	}
	d.liveMu.Lock()
	if d.liveValues == nil {
		d.liveValues = make(map[string]json.RawMessage)
	}
	d.liveValues[key.Name] = bytes.Clone(value)
	d.liveMu.Unlock()
	return true
}

func (d *daemon) settingsWriteSource() string {
	if d.settingsSource == "offline" {
		return "offline"
	}
	return "rpc"
}

func (d *daemon) registerSettings() {
	d.handle(rpc.MethodSettingsList, d.settingsList)
	d.handle(rpc.MethodSettingsGet, d.settingsGet)
	d.handle(rpc.MethodSettingsSet, d.settingsSet)
	d.handle(rpc.MethodSettingsUnset, d.settingsUnset)
	d.handle(rpc.MethodSettingsAdd, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return d.settingsListChange(ctx, raw, true)
	})
	d.handle(rpc.MethodSettingsRemove, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return d.settingsListChange(ctx, raw, false)
	})
	d.handle(rpc.MethodSettingsTokenInit, d.settingsTokenInit)
	d.handle(rpc.MethodSettingsTokenChange, d.settingsTokenChange)
}

func (d *daemon) initSettings(ctx context.Context) error {
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	if d.store == nil {
		d.denyAfterSettingsReadFailure(false)
		return errors.New("settings unavailable")
	}
	values, err := d.store.Settings(ctx)
	if err != nil {
		d.denyAfterSettingsReadFailure(false)
		return errors.New("settings unavailable")
	}
	effective := make(map[string]json.RawMessage, len(values)+len(d.opts.SettingsOverrides))
	for name, value := range values {
		effective[name] = value
	}
	for name, override := range d.opts.SettingsOverrides {
		effective[name] = override.Value
	}
	if err := config.ValidateSettings(effective); err != nil {
		d.denyAfterSettingsReadFailure(false)
		return errors.New("settings unavailable")
	}
	d.settingsInitial = values
	names := make([]string, 0, len(values)+len(d.opts.SettingsOverrides))
	seen := make(map[string]bool)
	for name := range values {
		names = append(names, name)
		seen[name] = true
	}
	for name := range d.opts.SettingsOverrides {
		if !seen[name] {
			names = append(names, name)
		}
	}
	d.applyBackgroundSettings(names, values)
	for _, name := range names {
		key, ok := config.LookupSetting(name)
		if ok && !key.Protected {
			d.applyLiveSetting(key, values)
		}
	}
	return nil
}

func (d *daemon) settingsRead(ctx context.Context) (map[string]json.RawMessage, error) {
	if d.store == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "settings unavailable")
	}
	values, err := d.store.Settings(ctx)
	if err != nil {
		d.refreshAccess(ctx)
		return nil, rpc.NewError(rpc.CodeInternal, "settings unavailable")
	}
	if d.settingsInitial == nil {
		d.settingsInitial = values
	}
	return values, nil
}

func (d *daemon) settingValue(key config.SettingKey, stored map[string]json.RawMessage, hideIDs bool) rpc.SettingValue {
	value := key.Default
	source := "default"
	if key.Apply == "restart" && d.settingsInitial != nil {
		if initial, ok := d.settingsInitial[key.Name]; ok {
			value = initial
			source = "database"
		}
	} else if v, ok := stored[key.Name]; ok {
		value = v
		source = "database"
	}
	override := ""
	if o, ok := d.opts.SettingsOverrides[key.Name]; ok && (o.Source == "env" || o.Source == "flag") {
		value = o.Value
		source = o.Source
		override = o.Source
	}
	saved := stored[key.Name]
	if key.Secret {
		value = json.RawMessage(`"[REDACTED]"`)
		if saved != nil {
			saved = json.RawMessage(`"[REDACTED]"`)
		}
	} else if hideIDs && (key.Name == "access.allow" || key.Name == "access.block") {
		value = countSettingIDs(value)
		if saved != nil {
			saved = countSettingIDs(saved)
		}
	}
	return rpc.SettingValue{Key: key.Name, Value: value, Source: source, Stored: saved, OverriddenBy: override, RestartRequired: key.Apply == "restart" || d.settingsPending[key.Name]}
}
func countSettingIDs(value json.RawMessage) json.RawMessage {
	var ids []int64
	_ = json.Unmarshal(value, &ids)
	out, _ := json.Marshal(len(ids))
	return out
}
func settingsKey(name string) (config.SettingKey, error) {
	key, ok := config.LookupSetting(name)
	if !ok {
		return key, rpc.NewError(rpc.CodeInvalidParams, "unknown setting")
	}
	return key, nil
}
func (d *daemon) requireOwner(ctx context.Context, phrase string) error {
	if d.ownerSecrets == nil {
		return rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase required")
	}
	err := d.ownerSecrets.Verify(ctx, phrase)
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrOwnerPassphraseRequired):
		return rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase required")
	case errors.Is(err, ErrOwnerPassphraseWrong):
		return rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase is wrong")
	case errors.Is(err, ErrOwnerPassphraseLocked):
		return rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase temporarily locked")
	default:
		return rpc.NewError(rpc.CodeInternal, "owner secret unavailable")
	}
}
func visibleSettingIDs(raw json.RawMessage, policies ...access.Policy) json.RawMessage {
	if raw == nil {
		return nil
	}
	var ids []int64
	if json.Unmarshal(raw, &ids) != nil {
		return json.RawMessage(`[]`)
	}
	visible := make([]int64, 0, len(ids))
	for _, id := range ids {
		allowed := true
		for _, policy := range policies {
			if !policy.Allowed(id) {
				allowed = false
				break
			}
		}
		if allowed {
			visible = append(visible, id)
		}
	}
	result, _ := json.Marshal(visible)
	return result
}

func (d *daemon) filterSettingVisibility(value rpc.SettingValue, stored map[string]json.RawMessage) rpc.SettingValue {
	if value.Key != "access.write" && value.Key != "access.admin" {
		return value
	}
	policy, err := access.FromSettings(stored)
	if err != nil {
		policy = access.Denied()
	}
	current := d.policy()
	value.Value = visibleSettingIDs(value.Value, policy, current)
	value.Stored = visibleSettingIDs(value.Stored, policy, current)
	return value
}

func (d *daemon) settingsList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SettingsListParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	authorized := false
	if p.Passphrase != "" {
		if err := d.requireOwner(ctx, p.Passphrase); err != nil {
			return nil, err
		}
		authorized = true
	}
	stored, err := d.settingsRead(ctx)
	if err != nil {
		return nil, err
	}
	out := rpc.SettingsListResult{Settings: []rpc.SettingValue{}}
	for _, key := range config.SettingKeys() {
		if strings.HasPrefix(key.Name, p.Prefix) {
			value := d.settingValue(key, stored, !authorized)
			if !authorized {
				value = d.filterSettingVisibility(value, stored)
			}
			out.Settings = append(out.Settings, value)
		}
	}
	return out, nil
}
func (d *daemon) settingsGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SettingsGetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	key, err := settingsKey(p.Key)
	if err != nil {
		return nil, err
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	authorized := false
	if (key.Name == "access.allow" || key.Name == "access.block" || key.Name == "access.write" || key.Name == "access.admin") && p.Passphrase != "" {
		if err := d.requireOwner(ctx, p.Passphrase); err != nil {
			return nil, err
		}
		authorized = true
	}
	stored, err := d.settingsRead(ctx)
	if err != nil {
		return nil, err
	}
	value := d.settingValue(key, stored, !authorized)
	if !authorized {
		value = d.filterSettingVisibility(value, stored)
	}
	return value, nil
}
func (d *daemon) settingsSet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SettingsSetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Changes != nil {
		if p.Key != "" || len(p.Value) != 0 || len(p.Changes) == 0 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid settings update")
		}
		return d.settingsWriteBatch(ctx, p.Changes, p.Passphrase)
	}
	return d.settingsWrite(ctx, p.Key, p.Passphrase, "set", p.Value, nil)
}

// denyAfterSettingsReadFailure replaces the snapshot while preserving refresh order.
// When locked is true, the caller already owns both access locks.
func (d *daemon) denyAfterSettingsReadFailure(locked bool) {
	if !locked {
		d.accessRefreshMu.Lock()
		defer d.accessRefreshMu.Unlock()
		d.accessMu.Lock()
		defer d.accessMu.Unlock()
	}
	policy := access.Denied()
	d.accessSnapshot = &policy
}

func (d *daemon) settingsWriteBatch(ctx context.Context, changes map[string]json.RawMessage, phrase string) (any, error) {
	keys := make([]string, 0, len(changes))
	parsed := make(map[string]json.RawMessage, len(changes))
	protected := false
	for name, value := range changes {
		key, err := settingsKey(name)
		if err != nil {
			return nil, err
		}
		canonical, err := config.ParseSettingJSON(name, value)
		if err != nil {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid setting value")
		}
		protected = protected || key.Protected
		keys = append(keys, name)
		parsed[name] = canonical
	}
	sort.Strings(keys)
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	if d.settingsSource == "offline" && d.legacyAccessPresent() {
		return nil, &LegacySettingsError{Source: d.opts.Paths.Access}
	}
	if protected {
		if err := d.requireOwner(ctx, phrase); err != nil {
			return nil, err
		}
	}
	stored, err := d.settingsRead(ctx)
	if err != nil {
		return nil, err
	}
	candidate := make(map[string]json.RawMessage, len(stored)+len(parsed))
	for name, value := range stored {
		candidate[name] = value
	}
	for name, value := range parsed {
		candidate[name] = value
	}
	for name, override := range d.opts.SettingsOverrides {
		candidate[name] = override.Value
	}
	if err := config.ValidateSettings(candidate); err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid settings combination")
	}
	if protected {
		d.accessRefreshMu.Lock()
		defer d.accessRefreshMu.Unlock()
		d.accessMu.Lock()
		defer d.accessMu.Unlock()
	}
	if d.settingsSource == "offline" && d.legacyAccessPresent() {
		return nil, &LegacySettingsError{Source: d.opts.Paths.Access}
	}
	if err := d.store.SetSettings(ctx, parsed); err != nil {
		var ce *config.Error
		if errors.As(err, &ce) {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid settings combination")
		}
		return nil, rpc.NewError(rpc.CodeInternal, "settings update failed")
	}
	after, err := d.store.Settings(ctx)
	if err != nil {
		d.denyAfterSettingsReadFailure(protected)
		return nil, rpc.NewError(rpc.CodeInternal, "settings unavailable")
	}
	if protected {
		policy := access.Denied()
		if !d.legacyAccessPresent() {
			p, err := access.FromSettings(after)
			if err == nil {
				policy = p
			}
		}
		d.accessSnapshot = &policy
		if policy.Closed() && d.log != nil {
			d.log.Error("access policy unavailable")
		}
	}
	d.applyBackgroundSettings(keys, after)
	result := rpc.SettingsBatchWriteResult{Settings: make([]rpc.SettingsWriteResult, 0, len(keys))}
	for _, name := range keys {
		key, _ := config.LookupSetting(name)
		old := key.Default
		if v, ok := stored[name]; ok {
			old = v
		}
		if d.log != nil {
			if key.Type == "ids" || key.Type == "paths" {
				added, removed := settingListDifference(old, after[name])
				d.log.Info("setting updated", "key", name, "source", d.settingsWriteSource(), "added", added, "removed", removed)
			} else {
				d.log.Info("setting updated", "key", name, "source", d.settingsWriteSource())
			}
		}
		applied := d.applyLiveSetting(key, after)
		if d.settingsPending == nil {
			d.settingsPending = make(map[string]bool)
		}
		d.settingsPending[name] = !applied
		item := rpc.SettingsWriteResult{SettingValue: d.settingValue(key, after, false), Previous: old, Applied: "restart_required"}
		if key.Secret {
			item.Previous = json.RawMessage(`"[REDACTED]"`)
		}
		if applied {
			item.Applied = "live"
		} else {
			item.RestartRequired = true
		}
		result.Settings = append(result.Settings, item)
	}
	return result, nil
}

func (d *daemon) settingsUnset(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SettingsUnsetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	return d.settingsWrite(ctx, p.Key, p.Passphrase, "unset", nil, nil)
}
func (d *daemon) settingsListChange(ctx context.Context, raw json.RawMessage, add bool) (any, error) {
	var p rpc.SettingsListChangeParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	op := "remove"
	if add {
		op = "add"
	}
	return d.settingsWrite(ctx, p.Key, p.Passphrase, op, nil, p.Values)
}
func (d *daemon) settingsWrite(ctx context.Context, name, phrase, op string, value json.RawMessage, items []json.RawMessage) (any, error) {
	key, err := settingsKey(name)
	if err != nil {
		return nil, err
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	if d.settingsSource == "offline" && d.legacyAccessPresent() {
		return nil, &LegacySettingsError{Source: d.opts.Paths.Access}
	}
	if key.Protected {
		if err := d.requireOwner(ctx, phrase); err != nil {
			return nil, err
		}
	}
	stored, err := d.settingsRead(ctx)
	if err != nil {
		return nil, err
	}
	old := key.Default
	if v, ok := stored[name]; ok {
		old = v
	}
	if op == "add" || op == "remove" {
		if key.Type != "ids" && key.Type != "paths" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "setting is not a list")
		}
		if len(items) == 0 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "list values are required")
		}
		value, err = changeSettingList(key, old, items, op == "add")
		if err != nil {
			return nil, err
		}
	}
	if op != "unset" {
		value, err = config.ParseSettingJSON(name, value)
		if err != nil {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid setting value")
		}
	}
	// Check the effective set as well as the database set checked by store.
	candidate := make(map[string]json.RawMessage, len(stored))
	for k, v := range stored {
		candidate[k] = v
	}
	if op == "unset" {
		delete(candidate, name)
	} else {
		candidate[name] = value
	}
	for k, override := range d.opts.SettingsOverrides {
		candidate[k] = override.Value
	}
	if err := config.ValidateSettings(candidate); err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid settings combination")
	}
	// A policy reader must never observe an old permissive snapshot after the DB commit.
	if key.Protected {
		d.accessRefreshMu.Lock()
		defer d.accessRefreshMu.Unlock()
		d.accessMu.Lock()
		defer d.accessMu.Unlock()
	}
	if d.settingsSource == "offline" && d.legacyAccessPresent() {
		return nil, &LegacySettingsError{Source: d.opts.Paths.Access}
	}
	if op == "unset" {
		err = d.store.DeleteSetting(ctx, name)
	} else {
		err = d.store.SetSettings(ctx, map[string]json.RawMessage{name: value})
	}
	if err != nil {
		var ce *config.Error
		if errors.As(err, &ce) {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid settings combination")
		}
		return nil, rpc.NewError(rpc.CodeInternal, "settings update failed")
	}
	stored, err = d.store.Settings(ctx)
	if err != nil {
		d.denyAfterSettingsReadFailure(key.Protected)
		return nil, rpc.NewError(rpc.CodeInternal, "settings unavailable")
	}
	if key.Protected {
		policy := access.Denied()
		if !d.legacyAccessPresent() {
			p, e := access.FromSettings(stored)
			if e == nil {
				policy = p
			}
		}
		d.accessSnapshot = &policy
		if policy.Closed() && d.log != nil {
			d.log.Error("access policy unavailable")
		}
	}
	if d.log != nil {
		if key.Type == "ids" || key.Type == "paths" {
			current := key.Default
			if v, ok := stored[name]; ok {
				current = v
			}
			added, removed := settingListDifference(old, current)
			d.log.Info("setting updated", "key", name, "source", d.settingsWriteSource(), "added", added, "removed", removed)
		} else {
			d.log.Info("setting updated", "key", name, "source", d.settingsWriteSource())
		}
	}
	d.applyBackgroundSettings([]string{name}, stored)
	applied := d.applyLiveSetting(key, stored)
	if d.settingsPending == nil {
		d.settingsPending = make(map[string]bool)
	}
	d.settingsPending[name] = !applied
	result := rpc.SettingsWriteResult{SettingValue: d.settingValue(key, stored, false), Previous: old, Applied: "restart_required"}
	if key.Secret {
		result.Previous = json.RawMessage(`"[REDACTED]"`)
	}
	if applied {
		result.Applied = "live"
	} else {
		result.RestartRequired = true
	}
	return result, nil
}
func settingListDifference(before, after json.RawMessage) (int, int) {
	var a, b []json.RawMessage
	_ = json.Unmarshal(before, &a)
	_ = json.Unmarshal(after, &b)
	left := make(map[string]bool, len(a))
	for _, v := range a {
		left[string(v)] = true
	}
	added := 0
	for _, v := range b {
		if left[string(v)] {
			delete(left, string(v))
		} else {
			added++
		}
	}
	return added, len(left)
}

func changeSettingList(key config.SettingKey, old json.RawMessage, items []json.RawMessage, add bool) (json.RawMessage, error) {
	var current []json.RawMessage
	if json.Unmarshal(old, &current) != nil {
		return nil, rpc.NewError(rpc.CodeInternal, "settings unavailable")
	}
	set := map[string]json.RawMessage{}
	for _, v := range current {
		set[string(v)] = v
	}
	for _, v := range items {
		one := append(append([]byte{'['}, v...), ']')
		canonical, err := config.ParseSettingJSON(key.Name, one)
		if err != nil {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid list value")
		}
		var values []json.RawMessage
		if json.Unmarshal(canonical, &values) != nil || len(values) != 1 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid list value")
		}
		if add {
			set[string(values[0])] = values[0]
		} else {
			delete(set, string(values[0]))
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []json.RawMessage{}
	for _, k := range keys {
		out = append(out, set[k])
	}
	raw, _ := json.Marshal(out)
	canonical, err := config.ParseSettingJSON(key.Name, raw)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid list value")
	}
	return bytes.Clone(canonical), nil
}
func (d *daemon) settingsTokenInit(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SettingsTokenInitParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	if d.ownerSecrets == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "owner secret unavailable")
	}
	if err := checkNewOwnerPhrase(p.Passphrase); err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid owner passphrase")
	}
	if err := d.ownerSecrets.Init(ctx, p.Passphrase); err != nil {
		if errors.Is(err, ErrOwnerPassphraseExists) {
			return nil, rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase already exists")
		}
		return nil, rpc.NewError(rpc.CodeInternal, "owner secret unavailable")
	}
	return rpc.SettingsTokenResult{Updated: true}, nil
}
func (d *daemon) settingsTokenChange(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SettingsTokenChangeParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	if d.ownerSecrets == nil {
		return nil, rpc.NewError(rpc.CodeInternal, "owner secret unavailable")
	}
	err := d.ownerSecrets.Change(ctx, p.OldPassphrase, p.NewPassphrase)
	if err != nil {
		switch {
		case errors.Is(err, ErrOwnerPassphraseRequired):
			return nil, rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase required")
		case errors.Is(err, ErrOwnerPassphraseWrong):
			return nil, rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase is wrong")
		case errors.Is(err, ErrOwnerPassphraseLocked):
			return nil, rpc.NewError(rpc.CodeWriteDisabled, "owner passphrase temporarily locked")
		default:
			if checkNewOwnerPhrase(p.NewPassphrase) != nil {
				return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid owner passphrase")
			}
			return nil, rpc.NewError(rpc.CodeInternal, "owner secret unavailable")
		}
	}
	return rpc.SettingsTokenResult{Updated: true}, nil
}
