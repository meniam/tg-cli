package tgclient

import (
	"context"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) resolveUser(ctx context.Context, ref chatref.Ref) (peers.User, error) {
	if ref.Kind == chatref.ID && ref.ID <= 0 || ref.Kind == chatref.Invite {
		return peers.User{}, rpc.NewError(rpc.CodeInvalidParams, "a user reference is required")
	}
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return peers.User{}, err
	}
	u, ok := p.(peers.User)
	if !ok {
		return peers.User{}, rpc.NewError(rpc.CodeInvalidParams, "a user reference is required")
	}
	return u, nil
}
func userStatus(s tg.UserStatusClass) (string, string) {
	switch v := s.(type) {
	case *tg.UserStatusOnline:
		return "online", ""
	case *tg.UserStatusOffline:
		return "offline", time.Unix(int64(v.WasOnline), 0).UTC().Format(time.RFC3339)
	case *tg.UserStatusRecently:
		return "recently", ""
	case *tg.UserStatusLastWeek:
		return "last_week", ""
	case *tg.UserStatusLastMonth:
		return "last_month", ""
	default:
		return "hidden", ""
	}
}
func contactOf(u *tg.User) rpc.Contact {
	mutual := u.MutualContact
	return rpc.Contact{ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username, Phone: u.Phone, Mutual: &mutual, Deleted: u.Deleted, IsBot: u.Bot}
}
func profileOf(u *tg.User, full tg.UserFull) rpc.UserProfile {
	kind := "user"
	if u.Bot {
		kind = "bot"
	}
	status, last := userStatus(u.Status)
	names := []string{}
	for _, n := range u.Usernames {
		if n.Active && n.Username != u.Username {
			names = append(names, n.Username)
		}
	}
	return rpc.UserProfile{ID: u.ID, Type: kind, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username, Usernames: names, Phone: u.Phone, Bio: full.About, IsContact: u.Contact, MutualContact: u.MutualContact, Blocked: full.Blocked, Verified: u.Verified, Premium: u.Premium, Scam: u.Scam, Fake: u.Fake, Deleted: u.Deleted, Status: status, LastSeen: last, CommonChatsCount: full.CommonChatsCount}
}
func (c *Client) UserProfile(ctx context.Context, ref chatref.Ref) (rpc.UserProfile, error) {
	u, err := c.resolveUser(ctx, ref)
	if err != nil {
		return rpc.UserProfile{}, err
	}
	return userProfileAPI(ctx, c.tg.API(), c.mgr, u)
}
func userProfileAPI(ctx context.Context, api *tg.Client, mgr *peers.Manager, user peers.User) (rpc.UserProfile, error) {
	r, err := api.UsersGetFullUser(ctx, user.InputUser())
	if err != nil {
		return rpc.UserProfile{}, mapUserError(err)
	}
	if err = mgr.Apply(ctx, r.Users, r.Chats); err != nil {
		return rpc.UserProfile{}, mapError(err)
	}
	for _, raw := range r.Users {
		if u, ok := raw.(*tg.User); ok && u.ID == user.ID() {
			return profileOf(u, r.FullUser), nil
		}
	}
	return rpc.UserProfile{}, rpc.NewError(rpc.CodeTelegram, "Telegram returned no user")
}
func (c *Client) Contacts(ctx context.Context) ([]rpc.Contact, error) {
	m, err := c.manager(ctx)
	if err != nil {
		return nil, err
	}
	return contactsAPI(ctx, c.tg.API(), m)
}
func contactsAPI(ctx context.Context, api *tg.Client, mgr *peers.Manager) ([]rpc.Contact, error) {
	r, err := api.ContactsGetContacts(ctx, 0)
	if err != nil {
		return nil, mapUserError(err)
	}
	response, ok := r.(*tg.ContactsContacts)
	if !ok {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned an incomplete contact list")
	}
	if err = mgr.Apply(ctx, response.Users, nil); err != nil {
		return nil, mapError(err)
	}
	users := map[int64]*tg.User{}
	for _, raw := range response.Users {
		if u, ok := raw.(*tg.User); ok {
			users[u.ID] = u
		}
	}
	result := make([]rpc.Contact, 0, len(response.Contacts))
	for _, v := range response.Contacts {
		u, ok := users[v.UserID]
		if !ok {
			return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned a contact without a user")
		}
		item := contactOf(u)
		mutual := v.Mutual
		item.Mutual = &mutual
		result = append(result, item)
	}
	return result, nil
}
func (c *Client) SearchContacts(ctx context.Context, query string, limit int) ([]rpc.ContactHit, error) {
	if err := rpc.ValidateContactSearchParams(rpc.ContactSearchParams{Query: query, Limit: limit}); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 20
	}
	m, err := c.manager(ctx)
	if err != nil {
		return nil, err
	}
	return searchContactsAPI(ctx, c.tg.API(), m, query, limit)
}
func searchContactsAPI(ctx context.Context, api *tg.Client, mgr *peers.Manager, query string, limit int) ([]rpc.ContactHit, error) {
	r, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: query, Limit: limit})
	if err != nil {
		return nil, mapUserError(err)
	}
	if err = mgr.Apply(ctx, r.Users, r.Chats); err != nil {
		return nil, mapError(err)
	}
	users := map[int64]*tg.User{}
	for _, raw := range r.Users {
		if u, ok := raw.(*tg.User); ok {
			users[u.ID] = u
		}
	}
	result := []rpc.ContactHit{}
	seen := map[int64]bool{}
	for _, group := range []struct {
		peers  []tg.PeerClass
		source string
	}{{r.MyResults, "contacts"}, {r.Results, "global"}} {
		for _, raw := range group.peers {
			p, ok := raw.(*tg.PeerUser)
			if !ok || seen[p.UserID] {
				continue
			}
			u, ok := users[p.UserID]
			if !ok {
				continue
			}
			seen[p.UserID] = true
			result = append(result, rpc.ContactHit{Contact: contactOf(u), Source: group.source})
			if len(result) == limit {
				return result, nil
			}
		}
	}
	return result, nil
}
