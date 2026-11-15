package tgclient

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"sync"
)

var joinCursorOnce sync.Once
var joinCursorKey [32]byte
var joinCursorKeyErr error

type joinCursor struct {
	Chat   int64  `json:"chat"`
	User   int64  `json:"user"`
	Hash   int64  `json:"hash"`
	Date   int    `json:"date"`
	Search string `json:"search"`
}

func joinCursorAEAD() (cipher.AEAD, error) {
	joinCursorOnce.Do(func() { _, joinCursorKeyErr = io.ReadFull(rand.Reader, joinCursorKey[:]) })
	if joinCursorKeyErr != nil {
		return nil, joinCursorKeyErr
	}
	block, err := aes.NewCipher(joinCursorKey[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func sealJoinCursor(c joinCursor) (string, error) {
	a, err := joinCursorAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(a.Seal(nonce, nonce, data, nil)), nil
}
func openJoinCursor(s string) (joinCursor, error) {
	var c joinCursor
	if len(s) > 2048 {
		return c, invalidCursor()
	}
	a, err := joinCursorAEAD()
	if err != nil {
		return c, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < a.NonceSize() {
		return c, invalidCursor()
	}
	plain, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], nil)
	if err != nil || json.Unmarshal(plain, &c) != nil || c.User <= 0 || c.Date <= 0 {
		return joinCursor{}, invalidCursor()
	}
	return c, nil
}
