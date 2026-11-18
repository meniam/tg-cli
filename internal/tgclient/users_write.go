package tgclient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func mapUserError(err error) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		var rpcErr *rpc.Error
		if _, ok := tgerr.As(err); !ok && !errors.As(err, &rpcErr) {
			return rpc.NewError(rpc.CodeTelegram, "telegram request failed")
		}
	}
	if tgerr.Is(err, "CONTACT_NAME_EMPTY", "QUERY_TOO_SHORT", "SEARCH_QUERY_EMPTY") {
		return rpc.NewError(rpc.CodeInvalidParams, "Telegram rejected the input")
	}
	if tgerr.Is(err, "CONTACT_ID_INVALID") {
		return rpc.ErrChatNotFound()
	}
	return mapError(err)
}
func applyContactUpdates(ctx context.Context, mgr *peers.Manager, updates tg.UpdatesClass) error {
	switch u := updates.(type) {
	case *tg.Updates:
		return mgr.Apply(ctx, u.Users, u.Chats)
	case *tg.UpdatesCombined:
		return mgr.Apply(ctx, u.Users, u.Chats)
	default:
		return nil
	}
}
func (c *Client) AddContact(ctx context.Context, ref chatref.Ref, p rpc.ContactAddParams) (rpc.ContactAddResult, error) {
	if ref.Kind == chatref.Phone {
		return rpc.ContactAddResult{}, rpc.NewError(rpc.CodeInvalidParams, "adding a contact requires a username or user id")
	}
	u, err := c.resolveUser(ctx, ref)
	if err != nil {
		return rpc.ContactAddResult{}, err
	}
	return addContactAPI(ctx, c.tg.API(), c.mgr, u, p)
}
func addContactAPI(ctx context.Context, api *tg.Client, mgr *peers.Manager, u peers.User, p rpc.ContactAddParams) (rpc.ContactAddResult, error) {
	first, last := p.FirstName, p.LastName
	if first == "" {
		first = u.Raw().FirstName
	}
	if last == "" {
		last = u.Raw().LastName
	}
	if strings.TrimSpace(first) == "" {
		return rpc.ContactAddResult{}, rpc.NewError(rpc.CodeInvalidParams, "contact first name is required")
	}
	updates, err := api.ContactsAddContact(ctx, &tg.ContactsAddContactRequest{ID: u.InputUser(), FirstName: first, LastName: last, Phone: p.Phone})
	if err != nil {
		return rpc.ContactAddResult{}, mapUserError(err)
	}
	if err = applyContactUpdates(ctx, mgr, updates); err != nil {
		return rpc.ContactAddResult{}, mapError(err)
	}
	mutual := u.Raw().MutualContact
	var resultRaw json.RawMessage
	switch v := updates.(type) {
	case *tg.Updates:
		for _, raw := range v.Users {
			if x, ok := raw.(*tg.User); ok && x.ID == u.ID() {
				mutual = x.MutualContact
				resultRaw = methodRaw(rawAddContact, x)
			}
		}
	case *tg.UpdatesCombined:
		for _, raw := range v.Users {
			if x, ok := raw.(*tg.User); ok && x.ID == u.ID() {
				mutual = x.MutualContact
				resultRaw = methodRaw(rawAddContact, x)
			}
		}
	}
	return rpc.ContactAddResult{Raw: resultRaw, UserID: u.ID(), FirstName: first, LastName: last, IsContact: true, Mutual: mutual}, nil
}
func (c *Client) DeleteContact(ctx context.Context, ref chatref.Ref) (rpc.ContactDeleteResult, error) {
	u, err := c.resolveUser(ctx, ref)
	if err != nil {
		return rpc.ContactDeleteResult{}, err
	}
	return deleteContactAPI(ctx, c.tg.API(), c.mgr, u)
}
func deleteContactAPI(ctx context.Context, api *tg.Client, mgr *peers.Manager, u peers.User) (rpc.ContactDeleteResult, error) {
	updates, err := api.ContactsDeleteContacts(ctx, []tg.InputUserClass{u.InputUser()})
	if err != nil {
		return rpc.ContactDeleteResult{}, mapUserError(err)
	}
	if err = applyContactUpdates(ctx, mgr, updates); err != nil {
		return rpc.ContactDeleteResult{}, mapError(err)
	}
	return rpc.ContactDeleteResult{UserID: u.ID(), Deleted: true}, nil
}
func (c *Client) Block(ctx context.Context, ref chatref.Ref) (rpc.BlockResult, error) {
	u, err := c.resolveUser(ctx, ref)
	if err != nil {
		return rpc.BlockResult{}, err
	}
	return blockAPI(ctx, c.tg.API(), u, true)
}
func (c *Client) Unblock(ctx context.Context, ref chatref.Ref) (rpc.BlockResult, error) {
	u, err := c.resolveUser(ctx, ref)
	if err != nil {
		return rpc.BlockResult{}, err
	}
	return blockAPI(ctx, c.tg.API(), u, false)
}
func blockAPI(ctx context.Context, api *tg.Client, u peers.User, block bool) (rpc.BlockResult, error) {
	var ok bool
	var err error
	if block {
		ok, err = api.ContactsBlock(ctx, &tg.ContactsBlockRequest{ID: u.InputPeer()})
	} else {
		ok, err = api.ContactsUnblock(ctx, &tg.ContactsUnblockRequest{ID: u.InputPeer()})
	}
	if err != nil {
		return rpc.BlockResult{}, mapUserError(err)
	}
	if !ok {
		return rpc.BlockResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram rejected the block operation")
	}
	return rpc.BlockResult{UserID: u.ID(), Blocked: block}, nil
}
