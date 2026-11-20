package tgclient

import (
	"context"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// SetTranscriptionSink installs the update receiver before the client runs.
func (c *Client) SetTranscriptionSink(sink func(rpc.Transcription)) {
	c.sinkMu.Lock()
	c.transcriptionSink = sink
	c.sinkMu.Unlock()
}

func (c *Client) transcribedAudio(u *tg.UpdateTranscribedAudio) {
	c.sinkMu.RLock()
	sink := c.transcriptionSink
	c.sinkMu.RUnlock()
	if sink != nil {
		sink(rpc.Transcription{Raw: methodRaw(rawTranscribed, u), ChatID: botPeerID(u.Peer), ID: u.MsgID, TranscriptionID: u.TranscriptionID, Text: u.Text, Pending: u.Pending})
	}
}

// Transcribe requests Telegram's transcription for a voice message.
func (c *Client) Transcribe(ctx context.Context, ref chatref.Ref, id int) (rpc.Transcription, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.Transcription{}, err
	}
	return transcribeResolved(ctx, c.tg.API(), p, id)
}

func transcribeResolved(ctx context.Context, api *tg.Client, p peers.Peer, id int) (rpc.Transcription, error) {
	v, err := api.MessagesTranscribeAudio(ctx, &tg.MessagesTranscribeAudioRequest{Peer: p.InputPeer(), MsgID: id})
	if err != nil {
		for _, e := range []struct{ code, reason string }{{"PREMIUM_ACCOUNT_REQUIRED", "premium_required"}, {"TRANSCRIPTION_FAILED", "transcription_failed"}} {
			if tgerr.Is(err, e.code) {
				return rpc.Transcription{}, &rpc.Error{Code: rpc.CodeTelegram, Message: "transcription unavailable", Data: map[string]any{"reason": e.reason}}
			}
		}
		return rpc.Transcription{}, mapError(err)
	}
	out := rpc.Transcription{Raw: methodRaw(rawTranscribe, v), ChatID: toChatOf(p).ID, ID: id, TranscriptionID: v.TranscriptionID, Text: v.Text, Pending: v.Pending}
	if remains, ok := v.GetTrialRemainsNum(); ok {
		out.TrialRemains = &remains
	}
	if reset, ok := v.GetTrialRemainsUntilDate(); ok {
		out.TrialResetsAt = formatDate(reset)
	}
	return out, nil
}
