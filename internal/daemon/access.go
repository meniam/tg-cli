package daemon

import (
	"context"

	"tg-cli/internal/access"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func (d *daemon) policy() access.Policy {
	if d.opts.Access == nil {
		return access.Policy{}
	}
	policy, err := d.opts.Access.Policy()
	if err != nil {
		// The path and file content must not reach a request response or log.
		d.log.Error("access policy unavailable")
	}
	return policy
}

func chatFilter(policy access.Policy) store.ChatFilter {
	hide, allow, block := policy.Filter()
	return store.ChatFilter{Hide: hide, Allow: allow, Block: block}
}

// requireAllowedChat rejects locally known closed references before any
// Telegram call. Other references need one Resolve under an enabled policy;
// chat.resolve can reuse that result.
func (d *daemon) requireAllowedChat(ctx context.Context, ref chatref.Ref) (*rpc.Chat, error) {
	policy := d.policy()
	if policy.Enabled() {
		if ref.Kind == chatref.ID && !policy.Allowed(ref.ID) {
			return nil, rpc.ErrChatNotFound()
		}
		if ref.Kind == chatref.Invite && !policy.InviteAllowed() {
			return nil, rpc.ErrChatNotFound()
		}
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	if !policy.Enabled() {
		return nil, nil
	}
	switch ref.Kind {
	case chatref.ID:
		return nil, nil
	case chatref.Invite:
		return nil, nil
	default:
		chat, err := d.opts.Telegram.Resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !policy.Allowed(chat.ID) {
			return nil, rpc.ErrChatNotFound()
		}
		return &chat, nil
	}
}
