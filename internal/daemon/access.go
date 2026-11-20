package daemon

import (
	"context"
	"os"

	"tg-cli/internal/access"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// refreshAccess replaces the snapshot even on failure, so a stale permissive
// policy cannot survive a failed database read or a pending legacy file.
func (d *daemon) refreshAccess(ctx context.Context) {
	policy := access.Denied()
	ok := false
	if d.store != nil {
		// Until the legacy file has been migrated, it may contain restrictions
		// absent from the database. Never silently open those chats.
		_, statErr := os.Stat(d.opts.Paths.Access)
		if d.opts.Paths.Access == "" || os.IsNotExist(statErr) {
			values, err := d.store.Settings(ctx)
			if err == nil {
				policy, err = access.FromSettings(values)
				ok = err == nil
			}
		}
	}
	d.accessMu.Lock()
	d.accessSnapshot = &policy
	d.accessMu.Unlock()
	if !ok && d.log != nil {
		d.log.Error("access policy unavailable")
	}
}

func (d *daemon) policy() access.Policy {
	d.accessMu.RLock()
	if d.accessSnapshot != nil {
		policy := *d.accessSnapshot
		d.accessMu.RUnlock()
		return policy
	}
	d.accessMu.RUnlock()
	// Test fixtures may supply the legacy loader directly. Run always creates
	// a database snapshot before serving requests.
	if d.opts.Access != nil {
		policy, err := d.opts.Access.Policy()
		if err != nil && d.log != nil {
			d.log.Error("access policy unavailable")
		}
		return policy
	}
	return access.Denied()
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
			// The answer for an id the session does not know: a closed id must match it.
			return nil, rpc.ErrUnknownChatID()
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
		chat, err := d.telegram(ctx).Resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !policy.Allowed(chat.ID) {
			return nil, rpc.ErrChatNotFound()
		}
		return &chat, nil
	}
}
