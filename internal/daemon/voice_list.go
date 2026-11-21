package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

const (
	voiceListDefaultLimit = 50
	voiceListMaxLimit     = 200
)

// voiceList lists the jobs of the voice queue, newest first. It reads the queue
// and the chat title in the database and never calls Telegram, and it carries no
// message text and no transcription. A chat that access.json closes is not
// listed, and asking for it by name answers an empty page like a chat that does
// not exist.
func (d *daemon) voiceList(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.VoiceListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.State != "" && !store.ValidVoiceState(p.State) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "state must be queued, running, done, failed, or skipped")
	}
	if p.Kind != "" && !store.ValidVoiceKind(p.Kind) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "kind must be voice, video_note, or video")
	}
	limit, err := limitOrDefault(p.Limit, voiceListDefaultLimit, voiceListMaxLimit)
	if err != nil {
		return nil, err
	}
	after, err := decodeVoiceCursor(p.Cursor)
	if err != nil {
		return nil, err
	}
	policy := d.policy()
	chatID, visible, err := d.searchChatScope(ctx, policy, p.Chat)
	if err != nil {
		return nil, err
	}
	res := rpc.VoiceListResult{Jobs: []rpc.VoiceJob{}}
	if !visible {
		return res, nil
	}
	entries, more, err := d.store.ListVoiceJobs(ctx, store.VoiceListQuery{State: p.State, Kind: p.Kind, ChatID: chatID, ChatFilter: chatFilter(policy), Limit: limit, After: after})
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		job := rpc.VoiceJob{
			ChatID: e.ChatID, ChatTitle: e.ChatTitle, MessageID: e.MessageID, Kind: e.Kind, Source: e.Source, State: e.State,
			Reason: e.Reason, Attempts: e.Attempts, Bytes: e.Bytes, EnqueuedAt: e.EnqueuedAt,
		}
		if e.FinishedAt != 0 {
			finished := e.FinishedAt
			job.FinishedAt = &finished
		}
		res.Jobs = append(res.Jobs, job)
	}
	if more {
		res.NextCursor = encodeVoiceCursor(entries[len(entries)-1].Position)
	}
	return res, nil
}

// encodeVoiceCursor writes the position of the last job of a page. The cursor is
// opaque to the caller: it holds two numbers and nothing else.
func encodeVoiceCursor(pos store.VoiceListPosition) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", pos.EnqueuedAt, pos.RowID)))
}

// decodeVoiceCursor reads a cursor of encodeVoiceCursor. An empty cursor means
// the first page; anything else that does not decode to two non-negative numbers
// is invalid params.
func decodeVoiceCursor(cursor string) (*store.VoiceListPosition, error) {
	if cursor == "" {
		return nil, nil
	}
	invalid := rpc.NewError(rpc.CodeInvalidParams, "invalid cursor")
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, invalid
	}
	at, row, ok := strings.Cut(string(raw), ":")
	if !ok {
		return nil, invalid
	}
	enqueued, err := strconv.ParseInt(at, 10, 64)
	if err != nil || enqueued < 0 {
		return nil, invalid
	}
	rowID, err := strconv.ParseInt(row, 10, 64)
	if err != nil || rowID < 0 {
		return nil, invalid
	}
	return &store.VoiceListPosition{EnqueuedAt: enqueued, RowID: rowID}, nil
}

// storedTranscriptions returns the finished transcriptions of the given stored
// messages of one chat, keyed by message id, in the form the local read commands
// print beside the message. It reads the database only; the caller has checked
// that access.json leaves the chat open.
func (d *daemon) storedTranscriptions(ctx context.Context, chatID int64, messages []store.Message) (map[int]*rpc.MessageTranscription, error) {
	ids := make([]int, 0, len(messages))
	for _, m := range messages {
		if m.MediaType != nil && transcribedMediaType(*m.MediaType) {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	found, err := d.store.FinishedTranscriptions(ctx, chatID, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int]*rpc.MessageTranscription, len(found))
	for id, t := range found {
		out[id] = &rpc.MessageTranscription{Text: t.Text, Source: t.Source, Engine: t.Engine}
	}
	return out, nil
}

// transcribedMediaType is a media type that can have a transcription.
func transcribedMediaType(mediaType string) bool {
	return store.ValidVoiceKind(mediaType)
}
