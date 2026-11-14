package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) Typing(ctx context.Context, ref chatref.Ref, p rpc.TypingParams) (rpc.TypingResult, error) {
	if err := rpc.ValidateTypingParams(p); err != nil {
		return rpc.TypingResult{}, err
	}
	if p.DryRun {
		return rpc.TypingResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.TypingResult{}, err
	}
	return setTyping(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p)
}

func setTyping(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, id int64, p rpc.TypingParams) (rpc.TypingResult, error) {
	if err := rpc.ValidateTypingParams(p); err != nil {
		return rpc.TypingResult{}, err
	}
	var action tg.SendMessageActionClass
	switch p.Action {
	case "typing":
		action = &tg.SendMessageTypingAction{}
	case "upload_photo":
		action = &tg.SendMessageUploadPhotoAction{}
	case "upload_video":
		action = &tg.SendMessageUploadVideoAction{}
	case "upload_document":
		action = &tg.SendMessageUploadDocumentAction{}
	case "record_voice":
		action = &tg.SendMessageRecordAudioAction{}
	case "upload_voice":
		action = &tg.SendMessageUploadAudioAction{}
	case "record_video":
		action = &tg.SendMessageRecordVideoAction{}
	case "choose_sticker":
		action = &tg.SendMessageChooseStickerAction{}
	case "cancel":
		action = &tg.SendMessageCancelAction{}
	}
	req := &tg.MessagesSetTypingRequest{Peer: peer, Action: action}
	if p.Topic > 0 {
		req.SetTopMsgID(p.Topic)
	}
	ok, err := api.MessagesSetTyping(ctx, req)
	if err != nil {
		return rpc.TypingResult{}, mapActionError(err)
	}
	if !ok {
		return rpc.TypingResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram rejected typing action")
	}
	return rpc.TypingResult{ChatID: id, Action: p.Action}, nil
}
