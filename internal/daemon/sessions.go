package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"tg-cli/internal/rpc"
)

func sessionID(hash int64) string {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(hash))
	sum := sha256.Sum256(raw[:])
	return hex.EncodeToString(sum[:8])
}

func setSessionIDs(result *rpc.SessionsResult) {
	for i := range result.Sessions {
		result.Sessions[i].ID = sessionID(result.Sessions[i].Hash)
	}
}

func (d *daemon) terminateSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.TerminateParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidateTerminateParams(p); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	list, err := d.telegram(ctx).Sessions(ctx)
	if err != nil {
		return nil, err
	}
	var target *rpc.Session
	for i := range list.Sessions {
		if sessionID(list.Sessions[i].Hash) == p.ID {
			if target != nil {
				return nil, rpc.NewError(rpc.CodeTelegram, "session id is ambiguous")
			}
			target = &list.Sessions[i]
		}
	}
	if target == nil {
		return nil, rpc.NewError(rpc.CodeNotFound, "session not found")
	}
	if target.Current {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "cannot terminate the current session")
	}
	if !d.policy().SessionsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("session termination requires sessions: true in access.json"))
	}
	if p.DryRun {
		return rpc.TerminateResult{ID: p.ID, DryRun: true, Action: "terminate", DeviceModel: target.DeviceModel, Platform: target.Platform, AppName: target.AppName, DateActive: target.DateActive}, nil
	}
	self, err := d.telegram(ctx).Self(ctx)
	if err != nil {
		return nil, err
	}
	if self == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	record := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "account": self.ID, "tool": "tg", "phase": "intent", "action": "session_terminate", "id": p.ID, "device_model": target.DeviceModel, "app_name": target.AppName, "date_active": target.DateActive, "outcome": "pending"}
	if err := d.accountJournal(record); err != nil {
		return nil, fmt.Errorf("record session termination intent: %w", err)
	}
	if !d.policy().SessionsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("session termination requires sessions: true in access.json"))
	}
	err = d.telegram(ctx).TerminateSession(ctx, target.Hash)
	record["time"] = time.Now().UTC().Format(time.RFC3339)
	record["phase"] = "result"
	record["outcome"] = "success"
	if err != nil {
		record["outcome"] = "failed"
	}
	if e := d.accountJournal(record); e != nil {
		d.log.Error("account result journal failed")
	}
	if err != nil {
		return nil, err
	}
	if fresh, e := d.telegram(ctx).Sessions(ctx); e == nil {
		d.save(ctx, rpc.MethodAccountSessions, func(ctx context.Context) error { return d.store.ReplaceSessions(ctx, fresh) })
	} else {
		d.log.Warn("session refresh failed")
	}
	return rpc.TerminateResult{ID: p.ID, Terminated: true}, nil
}
