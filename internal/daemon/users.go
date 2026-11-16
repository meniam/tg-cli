package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/throttle"
)

type usersTelegram interface {
	UserProfile(context.Context, chatref.Ref) (rpc.UserProfile, error)
	Contacts(context.Context) ([]rpc.Contact, error)
	SearchContacts(context.Context, string, int) ([]rpc.ContactHit, error)
	AddContact(context.Context, chatref.Ref, rpc.ContactAddParams) (rpc.ContactAddResult, error)
	DeleteContact(context.Context, chatref.Ref) (rpc.ContactDeleteResult, error)
	Block(context.Context, chatref.Ref) (rpc.BlockResult, error)
	Unblock(context.Context, chatref.Ref) (rpc.BlockResult, error)
}
type phoneResolver interface {
	ResolvePhone(context.Context, string) (rpc.Chat, error)
}

func (d *daemon) registerUsers() {
	for _, entry := range []struct {
		method  string
		handler rpc.Handler
	}{
		{rpc.MethodUserProfile, d.userProfile}, {rpc.MethodUserBlock, d.userBlock}, {rpc.MethodUserUnblock, d.userUnblock},
		{rpc.MethodContactList, d.contactList}, {rpc.MethodContactAdd, d.contactAdd},
		{rpc.MethodContactDelete, d.contactDelete}, {rpc.MethodContactSearch, d.contactSearch},
	} {
		handler := entry.handler
		d.handle(entry.method, func(ctx context.Context, raw json.RawMessage) (any, error) {
			result, err := handler(ctx, raw)
			if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result, err
			}
			var rpcErr *rpc.Error
			if errors.As(err, &rpcErr) {
				if rpcErr.Code == rpc.CodeTelegram {
					return nil, rpc.NewError(rpc.CodeTelegram, "telegram request failed")
				}
				return nil, err
			}
			return nil, rpc.NewError(rpc.CodeInternal, "user operation failed")
		})
	}
}

// resolvePerson fixes the target ID before checking write access and making a write.
func (d *daemon) resolvePerson(ctx context.Context, value string, allowPhone, write bool) (chatref.Ref, rpc.Chat, error) {
	if err := rpc.ValidateUserRef(value, allowPhone); err != nil {
		return chatref.Ref{}, rpc.Chat{}, err
	}
	ref, _ := chatref.Parse(value)
	if ref.Kind == chatref.ID && !d.policy().Allowed(ref.ID) {
		return ref, rpc.Chat{}, rpc.ErrUnknownChatID()
	}
	if ref.Kind == chatref.ID && write && !d.policy().WriteAllowed(ref.ID) {
		return ref, rpc.Chat{}, rpc.ErrWriteDisabled()
	}
	if err := d.requireAccount(ctx); err != nil {
		return ref, rpc.Chat{}, err
	}
	chat, err := d.telegram(ctx).Resolve(ctx, ref)
	if err != nil {
		return ref, rpc.Chat{}, err
	}
	if !d.policy().Allowed(chat.ID) {
		if ref.Kind == chatref.ID {
			return ref, rpc.Chat{}, rpc.ErrUnknownChatID()
		}
		return ref, rpc.Chat{}, rpc.ErrChatNotFound()
	}
	if chat.ID <= 0 || (chat.Type != "user" && chat.Type != "bot") {
		return ref, rpc.Chat{}, rpc.NewError(rpc.CodeInvalidParams, "a user or bot is required")
	}
	if write && !d.policy().WriteAllowed(chat.ID) {
		return ref, rpc.Chat{}, rpc.ErrWriteDisabled()
	}
	return chatref.Ref{Kind: chatref.ID, ID: chat.ID}, chat, nil
}
func (d *daemon) checkPersonWrite(id int64) error {
	p := d.policy()
	if !p.Allowed(id) {
		return rpc.ErrUnknownChatID()
	}
	if !p.WriteAllowed(id) {
		return rpc.ErrWriteDisabled()
	}
	return nil
}
func (d *daemon) personFromContact(ctx context.Context, c rpc.Contact) store.Person {
	raw := map[string]json.RawMessage{}
	if old, err := d.store.GetPerson(ctx, c.ID); err == nil && len(old.Raw) > 0 {
		_ = json.Unmarshal(old.Raw, &raw)
		if raw == nil {
			raw = map[string]json.RawMessage{}
		}
	}
	raw["deleted"], _ = json.Marshal(c.Deleted)
	encoded, _ := json.Marshal(raw)
	bot := c.IsBot
	return store.Person{ID: c.ID, FirstName: optionalText(c.FirstName), LastName: optionalText(c.LastName), Username: optionalText(c.Username), Phone: optionalText(c.Phone), IsBot: &bot, Raw: encoded}
}
func contactFromPerson(p store.Person) rpc.Contact {
	c := rpc.Contact{ID: p.ID, FirstName: valueText(p.FirstName), LastName: valueText(p.LastName), Username: valueText(p.Username), Phone: valueText(p.Phone)}
	if p.IsBot != nil {
		c.IsBot = *p.IsBot
	}
	var raw struct {
		Deleted bool `json:"deleted"`
	}
	_ = json.Unmarshal(p.Raw, &raw)
	c.Deleted = raw.Deleted
	return c
}
func valueText(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (d *daemon) saveContactPeople(ctx context.Context, people []rpc.Contact) error {
	list := make([]store.Person, 0, len(people))
	policy := d.policy()
	for _, c := range people {
		if policy.Allowed(c.ID) {
			list = append(list, d.personFromContact(ctx, c))
		}
	}
	return d.store.UpsertPeople(ctx, list)
}
func (d *daemon) userProfile(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.UserRefParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, chat, err := d.resolveProfilePerson(ctx, p.User)
	if err != nil {
		return nil, err
	}
	v, err := d.telegram(ctx).UserProfile(ctx, ref)
	if err != nil {
		return nil, err
	}
	if v.ID != chat.ID {
		return nil, rpc.ErrChatNotFound()
	}
	if !d.policy().Allowed(v.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	if err := d.contactsMu.Acquire(ctx); err != nil {
		return nil, err
	}
	defer d.contactsMu.Release()
	if !d.policy().Allowed(v.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	c := rpc.Contact{ID: v.ID, FirstName: v.FirstName, LastName: v.LastName, Username: v.Username, Phone: v.Phone, Deleted: v.Deleted, IsBot: v.Type == "bot"}
	person := d.personFromContact(ctx, c)
	person.Bio = optionalText(v.Bio)
	person.IsContact = &v.IsContact
	if err = d.store.UpsertPeople(ctx, []store.Person{person}); err != nil {
		return nil, err
	}
	return v, nil
}

func (d *daemon) resolveProfilePerson(ctx context.Context, value string) (chatref.Ref, rpc.Chat, error) {
	ref, err := chatref.Parse(value)
	if err != nil || ref.Kind != chatref.Phone {
		return d.resolvePerson(ctx, value, true, false)
	}
	if err := rpc.ValidateUserRef(value, true); err != nil {
		return ref, rpc.Chat{}, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return ref, rpc.Chat{}, err
	}
	chat, err := d.telegram(ctx).Resolve(ctx, ref)
	var re *rpc.Error
	if errors.As(err, &re) && re.Code == rpc.CodeNotFound {
		client, ok := d.telegram(ctx).(phoneResolver)
		if !ok {
			return ref, chat, err
		}
		d.phoneResolveMu.Lock()
		defer d.phoneResolveMu.Unlock()
		if err = ctx.Err(); err != nil {
			return ref, chat, err
		}
		if err = d.phoneLimit.take(time.Now()); err != nil {
			return ref, chat, err
		}
		chat, err = client.ResolvePhone(ctx, ref.Phone)
		d.phoneLimit.completed(time.Now())
	}
	if err != nil {
		return ref, chat, err
	}
	if !d.policy().Allowed(chat.ID) || chat.ID <= 0 || (chat.Type != "user" && chat.Type != "bot") {
		return ref, rpc.Chat{}, rpc.ErrChatNotFound()
	}
	return chatref.Ref{Kind: chatref.ID, ID: chat.ID}, chat, nil
}
func (d *daemon) userBlock(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.changeBlock(ctx, raw, true)
}
func (d *daemon) userUnblock(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.changeBlock(ctx, raw, false)
}
func (d *daemon) changeBlock(ctx context.Context, raw json.RawMessage, block bool) (any, error) {
	var p rpc.BlockParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, chat, err := d.resolvePerson(ctx, p.User, true, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.BlockResult{UserID: chat.ID, Blocked: block, DryRun: true}, nil
	}
	if err = d.checkPersonWrite(chat.ID); err != nil {
		return nil, err
	}
	if block {
		return d.telegram(ctx).Block(ctx, ref)
	}
	return d.telegram(ctx).Unblock(ctx, ref)
}
func (d *daemon) syncContacts(ctx context.Context) ([]rpc.Contact, error) {
	items, err := d.telegram(ctx).Contacts(ctx)
	if err != nil {
		return nil, err
	}
	filter := chatFilter(d.policy())
	people := make([]store.Person, 0, len(items))
	for _, c := range items {
		if d.policy().Allowed(c.ID) {
			people = append(people, d.personFromContact(ctx, c))
		}
	}
	if err = d.store.ReplaceContacts(ctx, people, filter); err != nil {
		return nil, err
	}
	return items, nil
}
func (d *daemon) syncContactsAfterLogin(ctx context.Context, authorized <-chan struct{}) {
	select {
	case <-ctx.Done():
		return
	case <-d.telegram(ctx).Ready():
	}
	user, err := d.telegram(ctx).Self(ctx)
	if err != nil {
		d.log.Warn("contact sync authorization check failed")
		return
	}
	if user == nil {
		select {
		case <-ctx.Done():
			return
		case <-authorized:
		}
	}
	if err := d.contactsMu.Acquire(ctx); err != nil {
		return
	}
	defer d.contactsMu.Release()
	if _, err = d.syncContacts(throttle.WithBackground(ctx)); err != nil && !errors.Is(err, context.Canceled) {
		d.log.Warn("contact sync failed")
	}
}
func (d *daemon) contactList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ContactListParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidateContactListParams(p); err != nil {
		return nil, err
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	if err := d.contactsMu.Acquire(ctx); err != nil {
		return nil, err
	}
	defer d.contactsMu.Release()
	var remote map[int64]rpc.Contact
	if !p.Local {
		if err := d.requireAccount(ctx); err != nil {
			return nil, err
		}
		items, err := d.syncContacts(ctx)
		if err != nil {
			return nil, err
		}
		remote = map[int64]rpc.Contact{}
		for _, c := range items {
			remote[c.ID] = c
		}
	}
	persons, more, err := d.store.ListContacts(ctx, p.Limit, p.Offset, chatFilter(d.policy()))
	if err != nil {
		return nil, err
	}
	result := rpc.ContactListResult{Contacts: []rpc.Contact{}}
	for _, person := range persons {
		c := contactFromPerson(person)
		if r, ok := remote[c.ID]; ok {
			c.Mutual = r.Mutual
		}
		result.Contacts = append(result.Contacts, c)
	}
	if more {
		result.NextOffset = p.Offset + len(persons)
	}
	return result, nil
}
func (d *daemon) contactAdd(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ContactAddParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, chat, err := d.resolvePerson(ctx, p.User, false, true)
	if err != nil {
		return nil, err
	}
	if err := d.contactsMu.Acquire(ctx); err != nil {
		return nil, err
	}
	defer d.contactsMu.Release()
	if p.FirstName == "" || p.LastName == "" {
		profile, e := d.telegram(ctx).UserProfile(ctx, ref)
		if e != nil {
			return nil, e
		}
		if p.FirstName == "" {
			p.FirstName = profile.FirstName
		}
		if p.LastName == "" {
			p.LastName = profile.LastName
		}
	}
	if strings.TrimSpace(p.FirstName) == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "contact first name is required")
	}
	if err = d.checkPersonWrite(chat.ID); err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.ContactAddResult{UserID: chat.ID, FirstName: p.FirstName, LastName: p.LastName, DryRun: true}, nil
	}
	res, err := d.telegram(ctx).AddContact(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	c := rpc.Contact{ID: chat.ID, FirstName: res.FirstName, LastName: res.LastName, Username: chat.Username, Phone: p.Phone, IsBot: chat.Type == "bot"}
	if previous, err := d.store.GetPerson(ctx, chat.ID); err == nil {
		c.Deleted = contactFromPerson(previous).Deleted
	}
	person := d.personFromContact(ctx, c)
	yes := true
	person.IsContact = &yes
	if err = d.store.UpsertPeople(ctx, []store.Person{person}); err != nil {
		return nil, err
	}
	return res, nil
}
func (d *daemon) contactDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ContactDeleteParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if !p.Yes && !p.DryRun {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "--yes is required")
	}
	ref, chat, err := d.resolvePerson(ctx, p.User, true, true)
	if err != nil {
		return nil, err
	}
	if err := d.contactsMu.Acquire(ctx); err != nil {
		return nil, err
	}
	defer d.contactsMu.Release()
	if err = d.checkPersonWrite(chat.ID); err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.ContactDeleteResult{UserID: chat.ID, DryRun: true}, nil
	}
	res, err := d.telegram(ctx).DeleteContact(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err = d.store.SetContact(ctx, chat.ID, false); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return res, nil
}
func (d *daemon) contactSearch(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ContactSearchParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidateContactSearchParams(p); err != nil {
		return nil, err
	}
	if p.Limit == 0 {
		p.Limit = 20
	}
	result := rpc.ContactSearchResult{Contacts: []rpc.ContactHit{}}
	if err := d.contactsMu.Acquire(ctx); err != nil {
		return nil, err
	}
	defer d.contactsMu.Release()
	if p.Local {
		items, err := d.store.SearchContacts(ctx, p.Query, p.Limit, chatFilter(d.policy()))
		if err != nil {
			if errors.Is(err, store.ErrEmptyQuery) {
				return nil, rpc.NewError(rpc.CodeInvalidParams, "search query is empty")
			}
			return nil, err
		}
		for _, item := range items {
			result.Contacts = append(result.Contacts, rpc.ContactHit{Contact: contactFromPerson(item), Source: "contacts"})
		}
		return result, nil
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	items, err := d.telegram(ctx).SearchContacts(ctx, p.Query, p.Limit)
	if err != nil {
		return nil, err
	}
	people := []rpc.Contact{}
	for _, item := range items {
		if d.policy().Allowed(item.ID) {
			result.Contacts = append(result.Contacts, item)
			people = append(people, item.Contact)
		}
	}
	if err = d.saveContactPeople(ctx, people); err != nil {
		return nil, err
	}
	return result, nil
}
