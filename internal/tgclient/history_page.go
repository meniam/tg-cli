package tgclient

import (
	"context"

	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type historyPageContextKey struct{}

func historyPageFromContext(ctx context.Context) bool {
	marked, _ := ctx.Value(historyPageContextKey{}).(bool)
	return marked
}

// HistoryPage fetches exactly one raw page of up to 100 older messages.
func (c *Client) HistoryPage(ctx context.Context, ref chatref.Ref, offsetID int) (rpc.HistoryPage, error) {
	if offsetID < 0 {
		return rpc.HistoryPage{}, rpc.NewError(rpc.CodeInvalidParams, "offset id must not be negative")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.HistoryPage{}, err
	}
	return historyPageAPI(ctx, c.tg.API(), resolved.InputPeer(), toChatOf(resolved), offsetID)
}

func historyPageAPI(ctx context.Context, api *tg.Client, input tg.InputPeerClass, chat rpc.Chat, offsetID int) (rpc.HistoryPage, error) {
	ctx = context.WithValue(ctx, historyPageContextKey{}, true)
	response, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer: input, OffsetID: offsetID, AddOffset: 0, Limit: telegramPageSize,
	})
	if err != nil {
		return rpc.HistoryPage{}, mapError(err)
	}
	modified, err := modifiedMessages(response)
	if err != nil {
		return rpc.HistoryPage{}, err
	}
	raw := modified.GetMessages()
	result := rpc.HistoryPage{Empty: len(raw) == 0}
	for i, message := range raw {
		if i == 0 || message.GetID() < result.RawNextOffsetID {
			result.RawNextOffsetID = message.GetID()
		}
	}
	result.Messages = convertMessages(modified, chat.ID, &rpc.Sender{ID: chat.ID, Name: chat.Title, Username: chat.Username})
	for i, message := range result.Messages {
		if i == 0 || message.ID < result.OldestID {
			result.OldestID = message.ID
		}
	}
	return result, nil
}
