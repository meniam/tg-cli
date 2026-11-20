package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// messageExportLocal reads one database page without requiring a Telegram login.
func (d *daemon) messageExportLocal(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.exportLocalPage(ctx, raw, maxPageBytes)
}

func (d *daemon) exportLocalPage(ctx context.Context, raw json.RawMessage, maxBytes int) (rpc.ExportPage, error) {
	var p rpc.ExportParams
	if err := decodeParams(raw, &p); err != nil {
		return rpc.ExportPage{}, err
	}
	if p.After < 0 {
		return rpc.ExportPage{}, rpc.NewError(rpc.CodeInvalidParams, "after must not be negative")
	}
	since, err := parseTime("since", p.Since)
	if err != nil {
		return rpc.ExportPage{}, err
	}
	until, err := parseTime("until", p.Until)
	if err != nil {
		return rpc.ExportPage{}, err
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return rpc.ExportPage{}, rpc.NewError(rpc.CodeInvalidParams, "since must not be later than until")
	}
	chatID, kind, err := d.localChatID(ctx, p.Chat)
	if err != nil {
		return rpc.ExportPage{}, err
	}
	known, err := d.store.ChatKnown(ctx, chatID)
	if err != nil {
		return rpc.ExportPage{}, err
	}
	if !known {
		if kind == chatref.ID {
			return rpc.ExportPage{}, rpc.ErrUnknownChatID()
		}
		return rpc.ExportPage{}, rpc.ErrChatNotFound()
	}
	stored, err := d.store.ExportMessages(ctx, store.ExportQuery{
		ChatID: chatID, After: p.After, Since: since, Until: until,
		IncludeDeleted: p.IncludeDeleted, Limit: store.MaxExportPageSize,
	})
	if err != nil {
		return rpc.ExportPage{}, err
	}
	transcriptions, err := d.storedTranscriptions(ctx, chatID, stored.Messages)
	if err != nil {
		return rpc.ExportPage{}, err
	}
	result := rpc.ExportPage{Messages: make([]rpc.ExportMessage, 0, len(stored.Messages))}
	// The size is that of {"messages":[]} plus encoded items and commas. The
	// cursor is added for every non-final candidate. maxPageBytes leaves room
	// for the JSON-RPC envelope and request ID outside this result.
	size := len(`{"messages":[]}`)
	for i, message := range stored.Messages {
		item := rpc.ExportMessage{Message: rpcStoredMessage(message), Deleted: message.DeletedAt != nil, Transcription: transcriptions[message.ID]}
		encoded, err := json.Marshal(item)
		if err != nil {
			return rpc.ExportPage{}, err
		}
		itemSize := len(encoded)
		if len(result.Messages) > 0 {
			itemSize++ // comma between messages
		}
		more := i < len(stored.Messages)-1 || !stored.Last
		cursorSize := 0
		if more {
			cursorSize = len(`,"next_after":`) + len(strconv.Itoa(message.ID))
		}
		if size+itemSize+cursorSize > maxBytes {
			if len(result.Messages) == 0 {
				return rpc.ExportPage{}, rpc.NewError(rpc.CodeInternal, fmt.Sprintf("message %d does not fit into a response page", message.ID))
			}
			result.NextAfter = result.Messages[len(result.Messages)-1].ID
			break
		}
		result.Messages = append(result.Messages, item)
		size += itemSize
		if more {
			result.NextAfter = message.ID
		} else {
			result.NextAfter = 0
		}
	}
	return result, nil
}
