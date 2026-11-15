package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"tg-cli/internal/rpc"
	"tg-cli/internal/sessionstr"
)

type sessionTelegram interface {
	SessionBytes(context.Context) ([]byte, error)
}

func (d *daemon) sessionExport(ctx context.Context, _ json.RawMessage) (any, error) {
	storage, ok := d.opts.Telegram.(sessionTelegram)
	if !ok {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
	}
	user, err := d.opts.Telegram.Self(ctx)
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
	client, _ := os.ReadFile(d.opts.Paths.Client)
	encoded, data, err := sessionstr.Encode(raw, strings.TrimSpace(string(client)), 0)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "session is unavailable")
	}
	return rpc.SessionExportResult{Session: encoded, Format: data.Format, DC: data.DC, UserID: data.UserID, KeyID: data.KeyID, Client: data.Client}, nil
}
