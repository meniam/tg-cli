package chatref

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseChatlist extracts the slug from a Telegram chat folder invite link.
func ParseChatlist(s string) (string, error) {
	path, ok := stripLinkPrefix(strings.TrimSpace(s))
	if !ok {
		return "", fmt.Errorf("expected a t.me/addlist/<slug> link")
	}
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "addlist") || !inviteRe.MatchString(parts[1]) {
		return "", fmt.Errorf("invalid chat folder invite link")
	}
	if _, err := url.PathUnescape(parts[1]); err != nil {
		return "", fmt.Errorf("invalid chat folder invite link")
	}
	return parts[1], nil
}
