package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"tg-cli/internal/rpc"
	"tg-cli/internal/sessionstr"
)

type sessionTelegram interface {
	SessionBytes(context.Context) ([]byte, error)
}

func (d *daemon) sessionExport(ctx context.Context, params json.RawMessage) (any, error) {
	var request rpc.SessionExportParams
	if json.Unmarshal(params, &request) != nil || !request.Yes || request.Path == "" || request.Path == "-" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "a file path and confirmation are required")
	}
	storage, ok := d.telegram(ctx).(sessionTelegram)
	if !ok {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
	}
	user, err := d.telegram(ctx).Self(ctx)
	if err != nil || user == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	raw, err := storage.SessionBytes(ctx)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
	}
	_, storedID, _, err := sessionstr.Inspect(raw)
	if err != nil || (storedID != 0 && storedID != user.ID) {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
	}
	if storedID == 0 {
		var file map[string]json.RawMessage
		var data map[string]json.RawMessage
		if json.Unmarshal(raw, &file) != nil || json.Unmarshal(file["Data"], &data) != nil || data == nil {
			return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
		}
		data["UserID"], _ = json.Marshal(user.ID)
		file["Data"], _ = json.Marshal(data)
		raw, err = json.Marshal(file)
		if err != nil {
			return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
		}
	}
	encoded, data, err := sessionstr.Encode(raw, d.opts.ClientPreset, 0)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
	}
	if err := ExportSessionFile(request.Path, encoded); err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "cannot create export file")
	}
	return rpc.SessionExportResult{Format: data.Format, DC: data.DC, UserID: data.UserID, KeyID: data.KeyID, Client: data.Client}, nil
}

// ExportSessionFile writes a session to a new owner-only file.
func ExportSessionFile(path, value string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, value+"\n")
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
