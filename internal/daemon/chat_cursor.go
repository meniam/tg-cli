package daemon

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sync"

	"tg-cli/internal/access"
	"tg-cli/internal/rpc"
)

// Each daemon owns a fresh key. Fixed-width offsets hide the kind of peer
// and how many hidden dialogs were scanned between visible rows.
type chatCursorCodec struct {
	once sync.Once
	aead cipher.AEAD
}

func (c *chatCursorCodec) cipher() cipher.AEAD {
	c.once.Do(func() {
		var key [32]byte
		rand.Read(key[:])
		block, _ := aes.NewCipher(key[:])
		c.aead, _ = cipher.NewGCMWithRandomNonce(block)
	})
	return c.aead
}

func (d *daemon) chatCursorScope(policy access.Policy, chatType string) []byte {
	closed, allow, block := policy.Filter()
	d.account.mu.RLock()
	defer d.account.mu.RUnlock()
	var id int64
	if d.account.user != nil {
		id = d.account.user.ID
	}
	return []byte(fmt.Sprintf("%d/%s/%t/%v/%v", id, chatType, closed, allow, block))
}

func (c *chatCursorCodec) seal(offset rpc.DialogOffset, scope []byte) string {
	data, _ := binary.Append(nil, binary.BigEndian, offset)
	return base64.RawURLEncoding.EncodeToString(c.cipher().Seal(nil, nil, data, scope))
}

func invalidChatCursor() *rpc.Error {
	return rpc.NewError(rpc.CodeInvalidParams, "cursor is not valid, start the list again")
}

func (c *chatCursorCodec) open(token string, scope []byte) (rpc.DialogOffset, error) {
	var offset rpc.DialogOffset
	if token == "" {
		return offset, nil
	}
	aead := c.cipher()
	if len(token) != base64.RawURLEncoding.EncodedLen(binary.Size(offset)+aead.Overhead()) {
		return offset, invalidChatCursor()
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(data) != binary.Size(offset)+aead.Overhead() {
		return offset, invalidChatCursor()
	}
	plain, err := aead.Open(nil, nil, data, scope)
	if err != nil {
		return offset, invalidChatCursor()
	}
	if _, err = binary.Decode(plain, binary.BigEndian, &offset); err != nil {
		return offset, invalidChatCursor()
	}
	return offset, nil
}
