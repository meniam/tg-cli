package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (d *daemon) messageReaders(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.MessageGetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	if _, err = d.requireAllowedChat(ctx, ref); err != nil {
		return nil, err
	}
	chat, err := d.opts.Telegram.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	self, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return nil, err
	}
	if chat.Type != "user" && chat.Type != "group" && chat.Type != "supergroup" || self != nil && chat.ID == self.ID {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "readers require a group or private user chat")
	}
	result, err := d.opts.Telegram.MessageReaders(ctx, chatref.Ref{Kind: chatref.ID, ID: chat.ID}, p.ID)
	if err != nil {
		return nil, err
	}
	if result.ChatID != chat.ID || !d.policy().Allowed(result.ChatID) {
		return nil, rpc.ErrChatNotFound()
	}
	hide, _, blocked := d.policy().Filter()
	denied := map[int64]bool{}
	for _, id := range blocked {
		denied[id] = true
	}
	result.Readers = append([]rpc.MessageReader{}, result.Readers...)
	result.Readers = filterReaders(result.Readers, hide, denied)
	for i := range result.Readers {
		if person, e := d.store.GetPerson(ctx, result.Readers[i].UserID); e == nil {
			result.Readers[i].Name = strings.TrimSpace(valueText(person.FirstName) + " " + valueText(person.LastName))
			if result.Readers[i].Name == "" {
				result.Readers[i].Name = valueText(person.DisplayName)
			}
			result.Readers[i].Username = valueText(person.Username)
		}
	}
	return result, nil
}
func filterReaders(readers []rpc.MessageReader, hide bool, denied map[int64]bool) []rpc.MessageReader {
	out := make([]rpc.MessageReader, 0, len(readers))
	for _, r := range readers {
		if !hide && !denied[r.UserID] {
			out = append(out, r)
		}
	}
	return out
}

func (d *daemon) accountProfile(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct{}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	return d.opts.Telegram.AccountProfile(ctx)
}

func (d *daemon) accountEmojiStatuses(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.EmojiStatusesParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	return d.opts.Telegram.AccountEmojiStatuses(ctx, p.Recent)
}

func (d *daemon) approveJoinRequest(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.handleJoinRequest(ctx, raw, true)
}
func (d *daemon) declineJoinRequest(ctx context.Context, raw json.RawMessage) (any, error) {
	return d.handleJoinRequest(ctx, raw, false)
}
func (d *daemon) handleJoinRequest(ctx context.Context, raw json.RawMessage, approved bool) (any, error) {
	var p rpc.JoinRequestActionParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, chat, err := d.adminReference(ctx, p.Chat, false, d.opts.Telegram)
	if err != nil {
		return nil, err
	}
	if chat.Type != "channel" && chat.Type != "supergroup" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "join requests require a channel or supergroup")
	}
	user, uid, err := d.memberReference(ctx, p.User, false)
	if err != nil {
		return nil, err
	}
	result := rpc.JoinRequestActionResult{ChatID: chat.ID, UserID: uid, Approved: approved, DryRun: p.DryRun}
	if p.DryRun {
		rights, err := d.opts.Telegram.OwnAdminRights(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !rights.Creator && !rights.InviteUsers {
			return nil, rpc.NewError(rpc.CodeTelegram, "chat admin rights are required")
		}
		return result, nil
	}
	self, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return nil, err
	}
	if self == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	action := "decline"
	if approved {
		action = "approve"
	}
	record := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "account": self.ID, "tool": "tg", "phase": "intent", "action": action, "chat_id": chat.ID, "user_id": uid, "outcome": "pending"}
	if err := d.moderation(record); err != nil {
		return nil, fmt.Errorf("record moderation intent: %w", err)
	}
	policy := d.policy()
	var denied error
	if !policy.Allowed(chat.ID) {
		denied = rpc.ErrChatNotFound()
	} else if !policy.AdminAllowed(chat.ID) {
		denied = rpc.ErrAdminDisabled()
	} else {
		hide, _, block := policy.Filter()
		if hide {
			denied = rpc.ErrChatNotFound()
		} else {
			for _, id := range block {
				if id == uid {
					denied = rpc.ErrUnknownChatID()
					break
				}
			}
		}
	}
	if denied != nil {
		d.finishModeration(record, denied)
		return nil, denied
	}
	err = d.opts.Telegram.HandleJoinRequest(ctx, ref, user, approved)
	record["time"] = time.Now().UTC().Format(time.RFC3339)
	record["phase"] = "result"
	record["outcome"] = writeOutcome(err)
	if e := d.moderation(record); e != nil {
		d.log.Error("moderation result journal failed")
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (d *daemon) chatSet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatSetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	ref, chat, err := d.adminReference(ctx, p.Chat, false, d.opts.Telegram)
	if err != nil {
		return nil, err
	}
	changes := p.Changes()
	for _, c := range changes {
		if (c.Flag == "signatures" || c.Flag == "signature-profiles") && chat.Type != "channel" || (c.Flag == "antispam" || c.Flag == "forum" || c.Flag == "prehistory" || c.Flag == "join-to-send") && chat.Type != "supergroup" || c.Flag == "join-requests" && chat.Type != "channel" && chat.Type != "supergroup" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "chat type is incompatible with "+c.Flag)
		}
	}
	out := rpc.ChatSetResult{ChatID: chat.ID, Changes: []rpc.SettingChange{}, DryRun: p.DryRun}
	if p.DryRun {
		rights, e := d.opts.Telegram.OwnAdminRights(ctx, ref)
		if e != nil {
			return nil, e
		}
		if !rights.Creator && !rights.ChangeInfo {
			return nil, rpc.NewError(rpc.CodeTelegram, "chat admin rights are required")
		}
		out.Changes = changes
		return out, nil
	}
	self, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return nil, err
	}
	if self == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	record := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "account": self.ID, "tool": "tg", "phase": "intent", "action": "set", "chat_id": chat.ID, "flags": changes, "outcome": "pending"}
	if err := d.moderation(record); err != nil {
		return nil, fmt.Errorf("record moderation intent: %w", err)
	}
	var firstErr error
	for i := 0; i < len(changes); i++ {
		c := changes[i]
		combined := c.Flag == "signatures" && p.SignatureProfiles != nil
		if !d.policy().Allowed(chat.ID) {
			err = rpc.ErrChatNotFound()
		} else if !d.policy().AdminAllowed(chat.ID) {
			err = rpc.ErrAdminDisabled()
		} else {
			var changed bool
			changed, err = d.opts.Telegram.SetChannelSetting(ctx, ref, c.Flag, c.Value, p.Signatures, p.SignatureProfiles)
			c.Changed = changed
		}
		group := []rpc.SettingChange{c}
		if combined {
			i++
			second := changes[i]
			second.Changed = c.Changed
			group = append(group, second)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			item := rpc.ItemErrorFrom(0, err)
			for _, v := range group {
				out.Errors = append(out.Errors, rpc.SettingError{Flag: v.Flag, Code: item.Code, Message: item.Message})
			}
			var re *rpc.Error
			if errors.As(err, &re) && (re.Code == rpc.CodeFloodWait || re.Code == rpc.CodeNotAuthorized) {
				for _, pending := range changes[i+1:] {
					out.Errors = append(out.Errors, rpc.SettingError{Flag: pending.Flag, Code: item.Code, Message: "not attempted: " + item.Message})
				}
				break
			}
		} else {
			out.Changes = append(out.Changes, group...)
		}
	}
	record["time"] = time.Now().UTC().Format(time.RFC3339)
	record["phase"] = "result"
	record["outcome"] = writeOutcome(firstErr)
	if e := d.moderation(record); e != nil {
		d.log.Error("moderation result journal failed")
	}
	if firstErr != nil && len(out.Changes) == 0 {
		return nil, firstErr
	}
	out.Partial = len(out.Errors) > 0
	return out, nil
}

func (d *daemon) accountSet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.AccountSetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(time.Now()); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	if !d.policy().ProfileAllowed() {
		return nil, &rpc.Error{Code: rpc.CodeWriteDisabled, Message: d.accessMessage("profile changes are disabled in access.json"), Data: map[string]any{"list": "profile"}}
	}
	var channel *chatref.Ref
	if p.PersonalChannel != nil && *p.PersonalChannel != "none" {
		ref, chat, err := d.listChat(ctx, *p.PersonalChannel)
		if err != nil {
			return nil, err
		}
		if chat.Type != "channel" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "personal channel must be a channel")
		}
		ref = chatref.Ref{Kind: chatref.ID, ID: chat.ID}
		channel = &ref
	}
	changes := []rpc.SettingChange{}
	for _, f := range []struct {
		name  string
		value *string
	}{{"birthday", p.Birthday}, {"personal-channel", p.PersonalChannel}, {"emoji-status", p.EmojiStatus}} {
		if f.value != nil {
			changes = append(changes, rpc.SettingChange{Flag: f.name, Value: *f.value})
		}
	}
	out := rpc.AccountSetResult{Changes: []rpc.SettingChange{}, DryRun: p.DryRun}
	if p.DryRun {
		out.Changes = changes
		return out, nil
	}
	self, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return nil, err
	}
	if self == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	names := []string{}
	for _, c := range changes {
		names = append(names, c.Flag)
	}
	record := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "account": self.ID, "tool": "tg", "phase": "intent", "action": "account.set", "fields": names, "outcome": "pending"}
	if err := d.moderation(record); err != nil {
		return nil, fmt.Errorf("record moderation intent: %w", err)
	}
	var first error
	for i, c := range changes {
		if !d.policy().ProfileAllowed() {
			err = &rpc.Error{Code: rpc.CodeWriteDisabled, Message: d.accessMessage("profile changes are disabled in access.json"), Data: map[string]any{"list": "profile"}}
		} else if c.Flag == "personal-channel" && channel != nil && !d.policy().Allowed(channel.ID) {
			err = rpc.ErrChatNotFound()
		} else {
			target := (*chatref.Ref)(nil)
			if c.Flag == "personal-channel" {
				target = channel
			}
			c.Changed, err = d.opts.Telegram.SetAccountField(ctx, c.Flag, c.Value, p.EmojiStatusUntil, target)
		}
		if err == nil {
			out.Changes = append(out.Changes, c)
			continue
		}
		if first == nil {
			first = err
		}
		item := rpc.ItemErrorFrom(0, err)
		out.Errors = append(out.Errors, rpc.SettingError{Flag: c.Flag, Code: item.Code, Message: item.Message})
		var re *rpc.Error
		if errors.As(err, &re) && (re.Code == rpc.CodeFloodWait || re.Code == rpc.CodeNotAuthorized) {
			for _, next := range changes[i+1:] {
				out.Errors = append(out.Errors, rpc.SettingError{Flag: next.Flag, Code: item.Code, Message: "not attempted: " + item.Message})
			}
			break
		}
	}
	record["time"] = time.Now().UTC().Format(time.RFC3339)
	record["phase"] = "result"
	record["outcome"] = writeOutcome(first)
	if e := d.moderation(record); e != nil {
		d.log.Error("moderation result journal failed")
	}
	if first != nil && len(out.Changes) == 0 {
		return nil, first
	}
	out.Partial = len(out.Errors) > 0
	return out, nil
}

func (d *daemon) chatRequests(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.JoinRequestsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 20, 100)
	if err != nil {
		return nil, err
	}
	p.Limit = limit
	ref, chat, err := d.listChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	if chat.Type != "channel" && chat.Type != "supergroup" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "requests require a channel or supergroup")
	}
	ref = chatref.Ref{Kind: chatref.ID, ID: chat.ID}
	result, err := d.opts.Telegram.ChatRequests(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	policy := d.policy()
	if !policy.Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	hide, _, block := policy.Filter()
	denied := map[int64]bool{}
	for _, id := range block {
		denied[id] = true
	}
	visible := make([]rpc.JoinRequest, 0, len(result.Requests))
	members := []rpc.Member{}
	for _, r := range result.Requests {
		if hide || denied[r.UserID] {
			continue
		}
		visible = append(visible, r)
		members = append(members, rpc.Member{ID: r.UserID, Name: r.Name, Username: r.Username, IsBot: r.IsBot})
	}
	result.Requests = visible
	if err := d.opts.Telegram.CacheJoinRequestUsers(ctx, visible); err != nil {
		d.log.Error("could not cache request users")
	}
	d.saveMembers(ctx, members)
	return result, nil
}

func (d *daemon) filterPrivacy(r rpc.PrivacyRules) rpc.PrivacyRules {
	p := d.policy()
	filter := func(ids []int64) []int64 {
		out := make([]int64, 0, len(ids))
		for _, id := range ids {
			if p.Allowed(id) {
				out = append(out, id)
			} else {
				r.Hidden++
			}
		}
		return out
	}
	r.AllowUsers = filter(r.AllowUsers)
	r.DenyUsers = filter(r.DenyUsers)
	r.AllowChats = filter(r.AllowChats)
	r.DenyChats = filter(r.DenyChats)
	return r
}
func (d *daemon) accountPrivacyGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.PrivacyGetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if !rpc.ValidPrivacyKey(p.Key) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "unknown privacy key")
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	result, err := d.opts.Telegram.AccountPrivacyGet(ctx, p.Key)
	if err != nil {
		return nil, err
	}
	return d.filterPrivacy(result), nil
}
func (d *daemon) accountPrivacySet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.PrivacySetParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	if !d.policy().ProfileAllowed() {
		return nil, &rpc.Error{Code: rpc.CodeWriteDisabled, Message: d.accessMessage("profile changes are disabled in access.json"), Data: map[string]any{"list": "profile"}}
	}
	current, err := d.opts.Telegram.AccountPrivacyGet(ctx, p.Key)
	if err != nil {
		return nil, err
	}
	if d.filterPrivacy(current).Hidden > 0 && !p.ReplaceHidden {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "hidden rules would be replaced: add --replace-hidden")
	}
	users := func(values []string) ([]chatref.Ref, error) {
		out := []chatref.Ref{}
		for _, value := range values {
			ref, chat, err := d.resolvePerson(ctx, value, false, false)
			if err != nil {
				return nil, err
			}
			out = append(out, chatref.Ref{Kind: chatref.ID, ID: chat.ID})
			_ = ref
		}
		return out, nil
	}
	chats := func(values []string) ([]chatref.Ref, error) {
		out := []chatref.Ref{}
		for _, value := range values {
			_, chat, err := d.listChat(ctx, value)
			if err != nil {
				return nil, err
			}
			if chat.Type != "group" && chat.Type != "supergroup" && chat.Type != "channel" {
				return nil, rpc.NewError(rpc.CodeInvalidParams, "privacy chat must be a group or channel")
			}
			out = append(out, chatref.Ref{Kind: chatref.ID, ID: chat.ID})
		}
		return out, nil
	}
	au, err := users(p.AllowUsers)
	if err != nil {
		return nil, err
	}
	du, err := users(p.DenyUsers)
	if err != nil {
		return nil, err
	}
	ac, err := chats(p.AllowChats)
	if err != nil {
		return nil, err
	}
	dc, err := chats(p.DenyChats)
	if err != nil {
		return nil, err
	}
	for _, groups := range [][][]chatref.Ref{{au, du}, {ac, dc}} {
		seen := map[int64]bool{}
		for _, group := range groups {
			for _, ref := range group {
				if seen[ref.ID] {
					return nil, rpc.NewError(rpc.CodeInvalidParams, "duplicate or conflicting privacy exception")
				}
				seen[ref.ID] = true
			}
		}
	}
	if p.DryRun {
		return rpc.PrivacyRules{Key: p.Key, Default: p.Default, AllowUsers: idsOf(au), DenyUsers: idsOf(du), AllowChats: idsOf(ac), DenyChats: idsOf(dc), AllowPremium: p.AllowPremium, AllowBots: p.AllowBots, DenyBots: p.DenyBots, AllowCloseFriends: p.AllowCloseFriends, DryRun: true}, nil
	}
	self, err := d.opts.Telegram.Self(ctx)
	if err != nil {
		return nil, err
	}
	if self == nil {
		return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	record := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "account": self.ID, "tool": "tg", "phase": "intent", "action": "privacy.set", "key": p.Key, "outcome": "pending"}
	if err := d.moderation(record); err != nil {
		return nil, fmt.Errorf("record moderation intent: %w", err)
	}
	policy := d.policy()
	var denied error
	if !policy.ProfileAllowed() {
		denied = &rpc.Error{Code: rpc.CodeWriteDisabled, Message: d.accessMessage("profile changes are disabled in access.json"), Data: map[string]any{"list": "profile"}}
	}
	if denied == nil {
		for _, refs := range [][]chatref.Ref{au, du, ac, dc} {
			for _, ref := range refs {
				if !policy.Allowed(ref.ID) {
					denied = rpc.ErrChatNotFound()
					break
				}
			}
		}
	}
	if denied != nil {
		d.finishModeration(record, denied)
		return nil, denied
	}
	result, err := d.opts.Telegram.AccountPrivacySet(ctx, p, au, du, ac, dc)
	record["time"] = time.Now().UTC().Format(time.RFC3339)
	record["phase"] = "result"
	record["outcome"] = writeOutcome(err)
	if e := d.moderation(record); e != nil {
		d.log.Error("moderation result journal failed")
	}
	if err != nil {
		return nil, err
	}
	return d.filterPrivacy(result), nil
}
func idsOf(refs []chatref.Ref) []int64 {
	out := make([]int64, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.ID)
	}
	return out
}

func (d *daemon) finishModeration(record map[string]any, err error) {
	record["time"] = time.Now().UTC().Format(time.RFC3339)
	record["phase"] = "result"
	record["outcome"] = writeOutcome(err)
	if e := d.moderation(record); e != nil {
		d.log.Error("moderation result journal failed")
	}
}
