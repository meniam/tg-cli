package chatref

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// MessageRef identifies one message, optionally within a forum topic.
type MessageRef struct {
	Chat  Ref
	ID    int
	Topic int
}

var errMessageLink = errors.New("invalid message link: expected https://t.me/channel/123")
var reservedMessagePaths = map[string]bool{
	"c": true, "joinchat": true, "addstickers": true, "share": true, "addtheme": true,
	"addemoji": true, "proxy": true, "socks": true, "iv": true, "s": true, "login": true,
	"boost": true, "giftcode": true, "invoice": true, "setlanguage": true,
}

// ParseMessageLink accepts the public and private t.me message link forms.
func ParseMessageLink(s string) (MessageRef, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "t.me/") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, "t.me") || u.User != nil {
		return MessageRef{}, errMessageLink
	}
	path := strings.TrimPrefix(u.EscapedPath(), "/")
	if strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return MessageRef{}, errMessageLink
	}
	var result MessageRef
	if strings.EqualFold(parts[0], "c") {
		if len(parts) != 3 && len(parts) != 4 {
			return MessageRef{}, errMessageLink
		}
		channel, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || channel <= 0 || channel > math.MaxInt64-channelIDBase {
			return MessageRef{}, errMessageLink
		}
		result.Chat = Ref{Kind: ID, ID: -channelIDBase - channel}
		parts = parts[2:]
	} else {
		if len(parts) != 2 && len(parts) != 3 {
			return MessageRef{}, errMessageLink
		}
		if reservedMessagePaths[strings.ToLower(parts[0])] || strings.HasPrefix(parts[0], "+") {
			return MessageRef{}, errMessageLink
		}
		result.Chat, err = parseUsername(parts[0])
		if err != nil {
			return MessageRef{}, errMessageLink
		}
		parts = parts[1:]
	}
	if len(parts) == 2 {
		result.Topic, err = positiveMessagePart(parts[0])
		if err != nil {
			return MessageRef{}, errMessageLink
		}
	}
	result.ID, err = positiveMessagePart(parts[len(parts)-1])
	if err != nil {
		return MessageRef{}, errMessageLink
	}
	return result, nil
}

func positiveMessagePart(s string) (int, error) {
	if s == "" || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		return 0, errMessageLink
	}
	value, err := strconv.Atoi(s)
	if err != nil || value <= 0 {
		return 0, errMessageLink
	}
	return value, nil
}

// FormatMessageLink builds a link for a channel or supergroup. A known public
// username takes precedence; otherwise the channel ID gets a private c link.
func FormatMessageLink(r MessageRef, username string) (string, error) {
	if r.ID <= 0 || r.Topic < 0 {
		return "", errMessageLink
	}
	if r.Chat.Kind == ID && !IsChannel(r.Chat.ID) {
		return "", errors.New("message links require a channel or supergroup")
	}
	if username == "" && r.Chat.Kind == Username {
		username = r.Chat.Username
	}
	var base string
	if username != "" {
		ref, err := parseUsername(username)
		if err != nil || reservedMessagePaths[ref.Username] {
			return "", errMessageLink
		}
		base = "https://t.me/" + ref.Username
	} else {
		channel, ok := ChannelID(r.Chat.ID)
		if r.Chat.Kind != ID || !ok {
			return "", errors.New("message links require a channel or supergroup")
		}
		base = fmt.Sprintf("https://t.me/c/%d", channel)
	}
	if r.Topic > 0 {
		base += fmt.Sprintf("/%d", r.Topic)
	}
	return fmt.Sprintf("%s/%d", base, r.ID), nil
}
