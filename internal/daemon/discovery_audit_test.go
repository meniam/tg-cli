package daemon

import (
	"context"
	"errors"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// TestAudit04_DiscoveryRejectsInvalidParams covers the validation of the discovery
// handlers: every case is an invalid_params answer and no Telegram call.
func TestAudit04_DiscoveryRejectsInvalidParams(t *testing.T) {
	d, tg, _, _ := discoveryDaemon(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"search without a query", func() error {
			_, err := d.messageSearch(ctx, params(t, rpc.SearchMessagesParams{Chat: "42", Query: "  "}))
			return err
		}},
		{"search with an unknown media filter", func() error {
			_, err := d.messageSearch(ctx, params(t, rpc.SearchMessagesParams{Chat: "42", Query: "a", Media: "sticker"}))
			return err
		}},
		{"search over the limit", func() error {
			_, err := d.messageSearch(ctx, params(t, rpc.SearchMessagesParams{Chat: "42", Query: "a", Limit: 101}))
			return err
		}},
		{"public search without a query", func() error {
			_, err := d.messageSearchPublic(ctx, params(t, rpc.PublicSearchParams{Query: " "}))
			return err
		}},
		{"context without an id", func() error {
			_, err := d.messageContext(ctx, []byte(`{"chat":"42","id":0}`))
			return err
		}},
		{"context window before too large", func() error {
			_, err := d.messageContext(ctx, []byte(`{"chat":"42","id":9,"before":51}`))
			return err
		}},
		{"context window after negative", func() error {
			_, err := d.messageContext(ctx, []byte(`{"chat":"42","id":9,"after":-1}`))
			return err
		}},
		{"comments without an id", func() error {
			_, err := d.messageComments(ctx, params(t, rpc.CommentsParams{Chat: "42", ID: 0}))
			return err
		}},
		{"link without an id", func() error {
			_, err := d.messageLink(ctx, params(t, rpc.LinkParams{Chat: "42", ID: 0}))
			return err
		}},
		{"link with a negative topic", func() error {
			_, err := d.messageLink(ctx, params(t, rpc.LinkParams{Chat: "42", ID: 9, Topic: -1}))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := rpcCode(t, tc.call()); code != rpc.CodeInvalidParams {
				t.Errorf("code = %d, want %d", code, rpc.CodeInvalidParams)
			}
		})
	}
	if len(tg.calls) != 0 {
		t.Errorf("an invalid request reached Telegram: %+v", tg.calls)
	}
}

// TestAudit04_DiscoveryPassesTelegramErrors checks that the error of Telegram
// reaches the caller of each of the six handlers unchanged.
func TestAudit04_DiscoveryPassesTelegramErrors(t *testing.T) {
	d, tg, _, path := discoveryDaemon(t)
	ctx := context.Background()
	writeAccess(t, path, `{"write":[42]}`)
	tg.err = rpc.NewError(rpc.CodeTelegram, "PEER_FLOOD")
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"search", func() error {
			_, err := d.messageSearch(ctx, params(t, rpc.SearchMessagesParams{Chat: "42", Query: "a"}))
			return err
		}},
		{"public search", func() error {
			_, err := d.messageSearchPublic(ctx, params(t, rpc.PublicSearchParams{Query: "a"}))
			return err
		}},
		{"context", func() error {
			_, err := d.messageContext(ctx, params(t, rpc.ContextParams{Chat: "42", ID: 9}))
			return err
		}},
		{"comments", func() error {
			_, err := d.messageComments(ctx, params(t, rpc.CommentsParams{Chat: "42", ID: 9}))
			return err
		}},
		{"link", func() error {
			_, err := d.messageLink(ctx, params(t, rpc.LinkParams{Chat: "42", ID: 9}))
			return err
		}},
		{"mark read", func() error {
			_, err := d.chatMarkRead(ctx, params(t, rpc.MarkReadParams{Chat: "42"}))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { sameRPCError(t, tc.call(), tg.err) })
	}
}

// TestAudit04_DiscoveryPublicHitsMustBeConsistent covers the public search filter:
// a hit without a chat id and a hit whose message belongs to another chat are
// dropped and not saved, even though the access lists would show their chat.
func TestAudit04_DiscoveryPublicHitsMustBeConsistent(t *testing.T) {
	d, tg, db, _ := discoveryDaemon(t)
	ctx := context.Background()
	tg.public = rpc.PublicSearchResult{Hits: []rpc.PublicHit{
		{ChatID: 42, Message: discoveryMessage(77, 31, "belongs to another chat")},
		{ChatID: 0, Message: discoveryMessage(0, 32, "no chat")},
		{ChatID: 42, Message: discoveryMessage(42, 33, "consistent")},
	}}
	got, err := d.messageSearchPublic(ctx, params(t, rpc.PublicSearchParams{Query: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	hits := got.(rpc.PublicSearchResult).Hits
	if len(hits) != 1 || hits[0].Message.ID != 33 {
		t.Fatalf("hits = %+v, want only message 33", hits)
	}
	for _, id := range []int{31, 32} {
		for _, chat := range []int64{0, 42, 77} {
			if _, err := db.GetMessage(ctx, chat, id); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("message %d of chat %d was saved: %v", id, chat, err)
			}
		}
	}
	if _, err := db.GetMessage(ctx, 42, 33); err != nil {
		t.Errorf("the consistent hit was not saved: %v", err)
	}
}
