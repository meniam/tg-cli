package tgclient

import (
	"context"

	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) MessageButtons(ctx context.Context, ref chatref.Ref, id int) (rpc.ButtonsResult, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ButtonsResult{}, err
	}
	elems, err := query.Messages(c.tg.API()).GetMessagesWithEntities(ctx, p.InputPeer(), id)
	if err != nil {
		if isNotFound(err) {
			return rpc.ButtonsResult{}, messageNotFound()
		}
		return rpc.ButtonsResult{}, mapError(err)
	}
	for _, e := range elems {
		if m, ok := e.Msg.(*tg.Message); ok && m.ID == id {
			return buttonsOf(int64(p.TDLibPeerID()), id, m.ReplyMarkup), nil
		}
		if m, ok := e.Msg.(*tg.MessageService); ok && m.ID == id {
			return buttonsOf(int64(p.TDLibPeerID()), id, nil), nil
		}
	}
	return rpc.ButtonsResult{}, messageNotFound()
}
func buttonsOf(chatID int64, id int, markup tg.ReplyMarkupClass) rpc.ButtonsResult {
	result := rpc.ButtonsResult{ChatID: chatID, MessageID: id, Keyboard: "none", Buttons: []rpc.Button{}}
	switch m := markup.(type) {
	case *tg.ReplyInlineMarkup:
		result.Keyboard = "inline"
		for row, r := range m.Rows {
			for col, b := range r.Buttons {
				v := rpc.Button{Index: len(result.Buttons), Row: row, Column: col, Text: b.Text}
				switch typ := b.Type.(type) {
				case *tg.InlineButtonTypeCallback:
					v.Type = "callback"
					v.Pressable = !typ.RequiresPassword
					if typ.RequiresPassword {
						v.Type = "callback_password"
					}
				case *tg.InlineButtonTypeURL:
					v.Type = "url"
					v.URL = typ.URL
				case *tg.InlineButtonTypeURLAuth:
					v.Type = "url_auth"
				case *tg.InlineButtonTypeWebView:
					v.Type = "web_view"
				case *tg.InlineButtonTypeSwitchInline:
					v.Type = "switch_inline"
				case *tg.InlineButtonTypeUserProfile:
					v.Type = "user_profile"
					v.UserID = typ.UserID
				case *tg.InlineButtonTypeCopy:
					v.Type = "copy"
					v.CopyText = typ.CopyText
				case *tg.InlineButtonTypeGame:
					v.Type = "game"
				case *tg.InlineButtonTypeBuy:
					v.Type = "buy"
				default:
					v.Type = "disabled"
				}
				result.Buttons = append(result.Buttons, v)
			}
		}
	case *tg.ReplyKeyboardMarkup:
		result.Keyboard = "reply"
		for row, r := range m.Rows {
			for col, b := range r.Buttons {
				v := rpc.Button{Index: len(result.Buttons), Row: row, Column: col, Text: b.Text}
				switch b.Type.(type) {
				case *tg.ButtonTypeDefault:
					v.Type = "text"
				case *tg.ButtonTypeRequestPhone:
					v.Type = "request_phone"
				case *tg.ButtonTypeRequestGeoLocation:
					v.Type = "request_location"
				case *tg.ButtonTypeRequestPoll:
					v.Type = "request_poll"
				case *tg.ButtonTypeRequestPeer:
					v.Type = "request_peer"
				case *tg.ButtonTypeSimpleWebView:
					v.Type = "web_view_simple"
				default:
					v.Type = "disabled"
				}
				result.Buttons = append(result.Buttons, v)
			}
		}
	}
	return result
}
