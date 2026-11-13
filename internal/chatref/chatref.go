// Package chatref parses the ways a user can point at a chat: a username, a
// numeric id in the Bot API form, a phone number from the contacts, a t.me
// link or an invite link.
//
// It is plain logic with no dependencies, so the daemon and the CLI share it.
package chatref

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Kind tells which way of pointing at a chat a Ref uses.
type Kind int

const (
	// Username is @name, name, t.me/name or tg://resolve?domain=name.
	Username Kind = iota + 1
	// ID is a numeric id in the Bot API form: users are positive, basic groups
	// are negative, channels and supergroups start from -1000000000000.
	ID
	// Phone is a phone number of a contact, with a leading plus.
	Phone
	// Invite is a private invite link, t.me/+hash or t.me/joinchat/hash.
	Invite
)

// channelIDBase is the offset of channel ids in the Bot API form.
const channelIDBase = 1_000_000_000_000

// Ref is a parsed chat reference. Exactly the fields of its Kind are set.
type Ref struct {
	Kind     Kind
	Username string // lowercase, without @
	ID       int64  // Bot API form
	Phone    string // digits only, without the plus
	Invite   string // the invite hash
}

// String returns a form that is safe to log: phone numbers and invite hashes
// are not repeated.
func (r Ref) String() string {
	switch r.Kind {
	case Username:
		return "@" + r.Username
	case ID:
		return strconv.FormatInt(r.ID, 10)
	case Phone:
		return "a phone number"
	case Invite:
		return "an invite link"
	}
	return "an unknown reference"
}

var (
	usernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`)
	phoneRe    = regexp.MustCompile(`^\+[0-9][0-9 ()\-]{5,24}$`)
	inviteRe   = regexp.MustCompile(`^[A-Za-z0-9_\-]{8,64}$`)
)

// Parse understands the following forms:
//
//	@name  name  t.me/name  https://t.me/name  tg://resolve?domain=name
//	12345  -12345  -1001234567890
//	+15551234567  +1 (555) 123-4567
//	t.me/+HASH  https://t.me/joinchat/HASH
func Parse(input string) (Ref, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return Ref{}, fmt.Errorf("the chat reference is empty")
	}

	if strings.HasPrefix(s, "tg:") {
		return parseDeepLink(s)
	}
	if rest, ok := stripLinkPrefix(s); ok {
		return parseLinkPath(rest)
	}

	switch {
	case strings.HasPrefix(s, "+"):
		return parsePhone(s)
	case s[0] == '-' || (s[0] >= '0' && s[0] <= '9'):
		return parseID(s)
	}
	return parseUsername(strings.TrimPrefix(s, "@"))
}

// stripLinkPrefix removes the scheme and host of a t.me link.
func stripLinkPrefix(s string) (string, bool) {
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/", "https://telegram.me/", "telegram.me/"} {
		if strings.HasPrefix(strings.ToLower(s), prefix) {
			return s[len(prefix):], true
		}
	}
	return "", false
}

func parseLinkPath(path string) (Ref, error) {
	path = strings.SplitN(path, "?", 2)[0]
	path = strings.TrimRight(path, "/")
	switch {
	case strings.HasPrefix(path, "+"):
		return parseInvite(path[1:])
	case strings.EqualFold(path, "joinchat"):
		return Ref{}, fmt.Errorf("the invite link has no valid hash")
	case strings.HasPrefix(strings.ToLower(path), "joinchat/"):
		return parseInvite(path[len("joinchat/"):])
	}
	// t.me/name/123 points at a message of the chat: the chat is what counts.
	return parseUsername(strings.SplitN(path, "/", 2)[0])
}

func parseDeepLink(s string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, fmt.Errorf("the link is not valid")
	}
	// tg://resolve?domain=x has a host, tg:resolve?domain=x has an opaque part.
	if u.Host != "resolve" && u.Opaque != "resolve" {
		return Ref{}, fmt.Errorf("only tg://resolve links are supported")
	}
	domain := u.Query().Get("domain")
	if domain == "" {
		return Ref{}, fmt.Errorf("the link has no domain")
	}
	return parseUsername(domain)
}

func parseUsername(name string) (Ref, error) {
	if !usernameRe.MatchString(name) {
		return Ref{}, fmt.Errorf("%q is not a username: 5 to 32 letters, digits and underscores, starting with a letter", name)
	}
	return Ref{Kind: Username, Username: strings.ToLower(name)}, nil
}

func parseInvite(hash string) (Ref, error) {
	hash = strings.SplitN(hash, "/", 2)[0]
	if !inviteRe.MatchString(hash) {
		return Ref{}, fmt.Errorf("the invite link has no valid hash")
	}
	return Ref{Kind: Invite, Invite: hash}, nil
}

func parsePhone(s string) (Ref, error) {
	if !phoneRe.MatchString(s) {
		return Ref{}, fmt.Errorf("the phone number must be in international format, like +15551234567")
	}
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if n := digits.Len(); n < 7 || n > 15 {
		return Ref{}, fmt.Errorf("the phone number must have 7 to 15 digits")
	}
	return Ref{Kind: Phone, Phone: digits.String()}, nil
}

func parseID(s string) (Ref, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id == 0 {
		return Ref{}, fmt.Errorf("%q is not a chat id", s)
	}
	// Ids of channels fall into the range below -1000000000000; the ids between
	// that and zero belong to basic groups. A value like -1000000000000 itself
	// would be channel 0, which does not exist.
	if id <= -channelIDBase && id > -channelIDBase-1 {
		return Ref{}, fmt.Errorf("%q is not a chat id", s)
	}
	return Ref{Kind: ID, ID: id}, nil
}

// IsChannel reports whether an id in the Bot API form belongs to a channel or
// a supergroup.
func IsChannel(id int64) bool { return id < -channelIDBase }

// IsGroup reports whether an id in the Bot API form belongs to a basic group.
func IsGroup(id int64) bool { return id < 0 && id > -channelIDBase }

// ChannelID returns the raw Telegram channel id of a Bot API id, and false for
// ids that are not channels.
func ChannelID(id int64) (int64, bool) {
	if !IsChannel(id) {
		return 0, false
	}
	return -id - channelIDBase, true
}

// GroupID returns the raw Telegram id of a basic group, and false otherwise.
func GroupID(id int64) (int64, bool) {
	if !IsGroup(id) {
		return 0, false
	}
	return -id, true
}
