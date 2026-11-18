package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type ChatAdmin interface {
	Join(context.Context, chatref.Ref) (rpc.ChatJoinResult, error)
	Leave(context.Context, chatref.Ref) (rpc.ChatLeaveResult, error)
	CreateChat(context.Context, rpc.ChatCreateParams) (rpc.ChatCreateResult, error)
	EditChat(context.Context, chatref.Ref, rpc.ChatEditParams) (rpc.ChatEditResult, error)
	Kick(context.Context, chatref.Ref, chatref.Ref) (rpc.ChatAdminResult, error)
	Ban(context.Context, chatref.Ref, chatref.Ref, string) (rpc.ChatAdminResult, error)
	Unban(context.Context, chatref.Ref, chatref.Ref) (rpc.ChatAdminResult, error)
	Restrict(context.Context, chatref.Ref, chatref.Ref, []string, bool, string) (rpc.ChatAdminResult, error)
	SetDefaultRights(context.Context, chatref.Ref, []string, bool) (rpc.ChatAdminResult, error)
	SetSlowMode(context.Context, chatref.Ref, int) (rpc.ChatAdminResult, error)
	OwnAdminRights(context.Context, chatref.Ref) (rpc.ChatOwnAdminRights, error)
	AdminInviteChat(context.Context, chatref.Ref) (rpc.Chat, bool, error)
}

func (d *daemon) registerChatAdmin() {
	for _, item := range []struct{ name, action string }{{rpc.MethodChatJoin, "join"}, {rpc.MethodChatLeave, "leave"}, {rpc.MethodChatCreate, "create"}, {rpc.MethodChatEdit, "edit"}, {rpc.MethodChatKick, "kick"}, {rpc.MethodChatBan, "ban"}, {rpc.MethodChatUnban, "unban"}, {rpc.MethodChatRestrict, "restrict"}, {rpc.MethodChatPermissions, "permissions"}, {rpc.MethodChatSlowMode, "slow-mode"}} {
		action := item.action
		d.handle(item.name, func(ctx context.Context, raw json.RawMessage) (any, error) { return d.chatAdmin(ctx, action, raw) })
	}
}

type adminParams struct {
	Chat    string   `json:"chat"`
	User    string   `json:"user"`
	Title   *string  `json:"title"`
	About   *string  `json:"about"`
	Type    string   `json:"type"`
	Deny    []string `json:"deny"`
	Clear   bool     `json:"clear"`
	Until   string   `json:"until"`
	Seconds int      `json:"seconds"`
	DryRun  bool     `json:"dry_run"`
}
type adminDryRun struct {
	DryRun      bool     `json:"dry_run"`
	Action      string   `json:"action"`
	ChatID      int64    `json:"chat_id,omitempty"`
	UserID      int64    `json:"user_id,omitempty"`
	Count       int      `json:"count"`
	TitleLength *int     `json:"title_length,omitempty"`
	AboutLength *int     `json:"about_length,omitempty"`
	Denied      []string `json:"denied,omitempty"`
	Until       string   `json:"until,omitempty"`
	Seconds     *int     `json:"seconds,omitempty"`
	Type        string   `json:"type,omitempty"`
}

func (d *daemon) adminReference(ctx context.Context, value string, write bool, api ChatAdmin) (chatref.Ref, rpc.Chat, error) {
	ref, err := parseChat(value)
	if err != nil {
		return ref, rpc.Chat{}, err
	}
	if err = d.requireAccount(ctx); err != nil {
		return ref, rpc.Chat{}, err
	}
	policy := d.policy()
	if ref.Kind == chatref.ID && !policy.Allowed(ref.ID) {
		return ref, rpc.Chat{}, rpc.ErrUnknownChatID()
	}
	var chat rpc.Chat
	if ref.Kind == chatref.Invite {
		if !write {
			return ref, chat, rpc.NewError(rpc.CodeInvalidParams, "invite link is only supported for join")
		}
		if policy.Closed() {
			return ref, chat, rpc.ErrChatNotFound()
		}
		var known bool
		chat, known, err = api.AdminInviteChat(ctx, ref)
		if err != nil {
			return ref, chat, err
		}
		if !known {
			return ref, chat, &rpc.Error{Code: rpc.CodeWriteDisabled, Message: "owner must join this chat first", Data: map[string]any{"list": "write"}}
		}
	} else {
		if ref.Kind == chatref.ID && ((write && !policy.WriteAllowed(ref.ID)) || (!write && !policy.AdminAllowed(ref.ID))) {
			if write {
				return ref, chat, rpc.ErrWriteDisabled()
			}
			return ref, chat, rpc.ErrAdminDisabled()
		}
		chat, err = d.telegram(ctx).Resolve(ctx, ref)
		if err != nil {
			return ref, chat, err
		}
	}
	policy = d.policy()
	if !policy.Allowed(chat.ID) {
		return ref, chat, rpc.ErrChatNotFound()
	}
	if write && !policy.WriteAllowed(chat.ID) {
		return ref, chat, rpc.ErrWriteDisabled()
	}
	if !write && !policy.AdminAllowed(chat.ID) {
		return ref, chat, rpc.ErrAdminDisabled()
	}
	if ref.Kind != chatref.Invite {
		ref = chatref.Ref{Kind: chatref.ID, ID: chat.ID}
	}
	return ref, chat, nil
}
func (d *daemon) memberReference(ctx context.Context, value string, rejectSelf bool) (chatref.Ref, int64, error) {
	ref, err := parseChatNoInvite(value)
	if err != nil {
		return ref, 0, err
	}
	if ref.Kind != chatref.Username && (ref.Kind != chatref.ID || ref.ID <= 0) {
		return ref, 0, rpc.NewError(rpc.CodeInvalidParams, "user must be a username or positive id")
	}
	blocked := func(id int64) bool {
		hide, _, ids := d.policy().Filter()
		if hide {
			return true
		}
		for _, v := range ids {
			if v == id {
				return true
			}
		}
		return false
	}
	if ref.Kind == chatref.ID && blocked(ref.ID) {
		return ref, 0, rpc.ErrUnknownChatID()
	}
	user, err := d.telegram(ctx).Resolve(ctx, ref)
	if err != nil {
		return ref, 0, err
	}
	if blocked(user.ID) {
		return ref, 0, rpc.ErrChatNotFound()
	}
	if user.ID <= 0 || (user.Type != "user" && user.Type != "bot") {
		return ref, 0, rpc.NewError(rpc.CodeInvalidParams, "target must be a user")
	}
	self, err := d.telegram(ctx).Self(ctx)
	if err != nil {
		return ref, 0, err
	}
	if self == nil {
		return ref, 0, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	if rejectSelf && self.ID == user.ID {
		return ref, 0, rpc.NewError(rpc.CodeInvalidParams, "use chat leave")
	}
	return chatref.Ref{Kind: chatref.ID, ID: user.ID}, user.ID, nil
}
func (d *daemon) moderation(record map[string]any) (err error) {
	d.deletionsMu.Lock()
	defer d.deletionsMu.Unlock()
	if d.opts.Paths.Dir == "" {
		return fmt.Errorf("moderation journal path is empty")
	}
	f, err := os.OpenFile(filepath.Join(d.opts.Paths.Dir, "moderation.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if err = json.NewEncoder(f).Encode(record); err != nil {
		return err
	}
	return f.Sync()
}
func adminTypeOK(action, kind string) bool {
	switch action {
	case "restrict", "slow-mode":
		return kind == "supergroup"
	case "permissions":
		return kind == "group" || kind == "supergroup"
	case "ban", "unban":
		return kind == "supergroup" || kind == "channel"
	default:
		return kind == "group" || kind == "supergroup" || kind == "channel"
	}
}
func (d *daemon) chatAdmin(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	var p adminParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	api := d.telegram(ctx)
	if action == "create" {
		if err := d.requireAccount(ctx); err != nil {
			return nil, err
		}
		if !d.policy().CreateAllowed() {
			return nil, &rpc.Error{Code: rpc.CodeWriteDisabled, Message: d.accessMessage("creating chats is disabled in access.json"), Data: map[string]any{"list": "create"}}
		}
		if p.Title == nil {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "title is required")
		}
		if err := rpc.ValidateChatTitle(*p.Title); err != nil {
			return nil, err
		}
		if p.Type != "channel" && p.Type != "supergroup" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "type must be supergroup or channel")
		}
		about := ""
		if p.About != nil {
			about = *p.About
		}
		if err := rpc.ValidateChatAbout(about); err != nil {
			return nil, err
		}
		if p.DryRun {
			n := utf8.RuneCountInString(*p.Title)
			a := utf8.RuneCountInString(about)
			return adminDryRun{DryRun: true, Action: action, Count: 1, TitleLength: &n, AboutLength: &a, Type: p.Type}, nil
		}
		result, err := api.CreateChat(ctx, rpc.ChatCreateParams{Title: *p.Title, About: about, Type: p.Type})
		if err != nil {
			return nil, err
		}
		policy := d.policy()
		_, allow, _ := policy.Filter()
		if len(allow) != 0 || !policy.Allowed(result.ID) {
			result.Title = ""
			result.Closed = true
		} else {
			d.saveChats(ctx, rpc.MethodChatCreate, []rpc.Chat{{Raw: result.Raw, ID: result.ID, Type: result.Type, Title: result.Title}}, false)
		}
		return result, nil
	}
	ref, chat, err := d.adminReference(ctx, p.Chat, action == "join" || action == "leave", api)
	if err != nil {
		return nil, err
	}
	if action != "join" && ref.Kind == chatref.Invite {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invite link is only supported for join")
	}
	if action == "join" && ref.Kind != chatref.Invite && chat.Type == "group" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "joining a basic group requires an invite link")
	}
	if !adminTypeOK(action, chat.Type) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "chat type is incompatible with action")
	}
	var userref chatref.Ref
	var uid int64
	if action == "kick" || action == "ban" || action == "unban" || action == "restrict" {
		userref, uid, err = d.memberReference(ctx, p.User, action != "unban")
		if err != nil {
			return nil, err
		}
	}
	if action == "edit" {
		if p.Title == nil && p.About == nil {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "title or about is required")
		}
		if p.Title != nil {
			if err = rpc.ValidateChatTitle(*p.Title); err != nil {
				return nil, err
			}
		}
		if p.About != nil {
			if err = rpc.ValidateChatAbout(*p.About); err != nil {
				return nil, err
			}
		}
	}
	if action == "ban" || action == "restrict" {
		if err = rpc.ValidateUntil(p.Until, time.Now()); err != nil {
			return nil, err
		}
	}
	if action == "restrict" || action == "permissions" {
		if err = rpc.ValidateRights(p.Deny, p.Clear); err != nil {
			return nil, err
		}
	}
	if action == "slow-mode" {
		if err = rpc.ValidateSlowModeSeconds(p.Seconds); err != nil {
			return nil, err
		}
	}
	if p.DryRun {
		if action != "join" && action != "leave" {
			rights, e := api.OwnAdminRights(ctx, ref)
			if e != nil {
				return nil, e
			}
			allowed := rights.Creator || (action == "edit" && rights.ChangeInfo) || (action != "edit" && rights.BanUsers)
			if !allowed {
				return nil, rpc.NewError(rpc.CodeTelegram, "chat admin rights are required")
			}
		}
		r := adminDryRun{DryRun: true, Action: action, Count: 1, ChatID: chat.ID, UserID: uid, Denied: p.Deny, Until: p.Until}
		if p.Title != nil {
			n := utf8.RuneCountInString(*p.Title)
			r.TitleLength = &n
		}
		if p.About != nil {
			n := utf8.RuneCountInString(*p.About)
			r.AboutLength = &n
		}
		if action == "slow-mode" {
			r.Seconds = &p.Seconds
		}
		return r, nil
	}
	journal := action == "leave" || action == "kick" || action == "ban" || action == "unban" || action == "restrict" || action == "permissions"
	var record map[string]any
	if journal {
		self, e := d.telegram(ctx).Self(ctx)
		if e != nil {
			return nil, e
		}
		if self == nil {
			return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
		}
		record = map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "account": self.ID, "tool": "tg", "phase": "intent", "action": action, "chat_id": chat.ID, "outcome": "pending"}
		if uid != 0 {
			record["user_id"] = uid
		}
		if p.Until != "" {
			record["until"] = p.Until
		}
		if p.Deny != nil {
			record["denied"] = p.Deny
		}
		if e = d.moderation(record); e != nil {
			return nil, fmt.Errorf("record moderation intent: %w", e)
		}
	}
	var result any
	switch action {
	case "join":
		var r rpc.ChatJoinResult
		r, err = api.Join(ctx, ref)
		chat.Raw = r.Raw
		if chat.ID == 0 && r.ChatID != 0 {
			chat.ID, chat.Type = r.ChatID, r.Type
		}
		if r.ChatID == 0 {
			r.ChatID = chat.ID
			r.Type = chat.Type
		}
		result = r
	case "leave":
		result, err = api.Leave(ctx, ref)
	case "edit":
		result, err = api.EditChat(ctx, ref, rpc.ChatEditParams{Chat: p.Chat, Title: p.Title, About: p.About})
	case "kick":
		result, err = api.Kick(ctx, ref, userref)
	case "ban":
		result, err = api.Ban(ctx, ref, userref, p.Until)
	case "unban":
		result, err = api.Unban(ctx, ref, userref)
	case "restrict":
		result, err = api.Restrict(ctx, ref, userref, p.Deny, p.Clear, p.Until)
	case "permissions":
		result, err = api.SetDefaultRights(ctx, ref, p.Deny, p.Clear)
	case "slow-mode":
		result, err = api.SetSlowMode(ctx, ref, p.Seconds)
	}
	if journal {
		record["time"] = time.Now().UTC().Format(time.RFC3339)
		record["phase"] = "result"
		record["outcome"] = writeOutcome(err)
		if e := d.moderation(record); e != nil {
			d.log.Error("moderation result journal failed")
		}
	}
	if err != nil {
		return nil, err
	}
	if (action == "join" || action == "edit") && d.policy().Allowed(chat.ID) {
		if action == "edit" && p.Title != nil {
			chat.Title = *p.Title
		}
		d.saveChats(ctx, "chat."+action, []rpc.Chat{chat}, false)
	}
	return result, nil
}
