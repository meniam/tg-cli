package tgclient

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

// idempotentAPI applies IDs after builder processing, immediately before invocation.
func idempotentAPI(api *tg.Client, accountID, chatID int64, key string) (*tg.Client, error) {
	if key == "" {
		return api, nil
	}
	if accountID <= 0 {
		return nil, errors.New("account id is required for idempotent writes")
	}
	return tg.NewClient(&idempotentInvoker{next: api.Invoker(), accountID: accountID, chatID: chatID, key: key}), nil
}

type idempotentInvoker struct {
	next              tg.Invoker
	accountID, chatID int64
	key               string
	offset            uint64
}

func deterministicID(accountID, chatID int64, key string, index uint64) int64 {
	// Fixed-width integers and a length prefix make the tuple unambiguous.
	h := sha256.New()
	var b [8]byte
	for _, v := range []uint64{uint64(accountID), uint64(chatID), uint64(len(key))} {
		binary.BigEndian.PutUint64(b[:], v)
		_, _ = h.Write(b[:])
	}
	_, _ = h.Write([]byte(key))
	binary.BigEndian.PutUint64(b[:], index)
	_, _ = h.Write(b[:])
	sum := h.Sum(nil)
	id := int64(binary.BigEndian.Uint64(sum[:8]))
	if id == 0 {
		return 1
	}
	return id
}

func (i *idempotentInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	switch req := input.(type) {
	case *tg.MessagesSendMessageRequest:
		req.RandomID = deterministicID(i.accountID, i.chatID, i.key, 0)
	case *tg.MessagesSendMediaRequest:
		req.RandomID = deterministicID(i.accountID, i.chatID, i.key, 0)
	case *tg.MessagesSendMultiMediaRequest:
		for n := range req.MultiMedia {
			req.MultiMedia[n].RandomID = deterministicID(i.accountID, i.chatID, i.key, uint64(n))
		}
	case *tg.MessagesForwardMessagesRequest:
		req.RandomID = make([]int64, len(req.ID))
		for n := range req.ID {
			req.RandomID[n] = deterministicID(i.accountID, i.chatID, i.key, i.offset+uint64(n))
		}
	default:
		return i.next.Invoke(ctx, input, output)
	}
	err := i.next.Invoke(ctx, input, output)
	if err == nil || ctx.Err() != nil || !retryableWriteError(err) {
		return err
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return i.next.Invoke(ctx, input, output)
}

func retryableWriteError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var e net.Error
	return errors.As(err, &e)
}

func (c *Client) idempotencyAccount(ctx context.Context, key string) (int64, error) {
	if key == "" {
		return 0, nil
	}
	user, err := c.Self(ctx)
	if err != nil {
		return 0, err
	}
	if user == nil {
		return 0, fmt.Errorf("idempotent write requires an authorized account")
	}
	return user.ID, nil
}
