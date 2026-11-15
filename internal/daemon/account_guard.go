package daemon

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"tg-cli/internal/rpc"
)

type accountCache struct {
	mu   sync.RWMutex
	user *rpc.User
}

func (c *accountCache) set(user *rpc.User) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if user == nil {
		c.user = nil
		return
	}
	copyUser := *user
	c.user = &copyUser
}
func (c *accountCache) setIfEmpty(user *rpc.User) {
	if user == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.user == nil {
		copyUser := *user
		c.user = &copyUser
	}
}
func (c *accountCache) check(req rpc.Request) error {
	if req.ExpectUser == "" || !accountMethod(req.Method) {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.user == nil {
		return rpc.NewError(rpc.CodeNotAuthorized, "expected account cannot be verified: account is unknown")
	}
	expected := req.ExpectUser
	match := false
	if strings.HasPrefix(expected, "@") {
		match = c.user.Username != "" && strings.EqualFold(expected[1:], c.user.Username)
	} else {
		id, err := strconv.ParseInt(expected, 10, 64)
		match = err == nil && id > 0 && id == c.user.ID
	}
	if !match {
		return rpc.NewError(rpc.CodeNotAuthorized, fmt.Sprintf("expected account %s, actual account id=%d username=@%s", expected, c.user.ID, c.user.Username))
	}
	return nil
}
func accountMethod(method string) bool {
	return method == rpc.MethodSearch || method == rpc.MethodSubscribe || method == rpc.MethodUnsubscribe || strings.HasPrefix(method, "chat.") || strings.HasPrefix(method, "message.") || strings.HasPrefix(method, "sticker.") || strings.HasPrefix(method, "note.") || strings.HasPrefix(method, "history_sync.")
}
