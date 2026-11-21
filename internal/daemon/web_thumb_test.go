package daemon

import (
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type thumbnailTelegram struct {
	Telegram
	fetches int
}

func (f *thumbnailTelegram) WebThumbnail(_ context.Context, _ int64, _, _ int, out io.Writer) error {
	f.fetches++
	return jpeg.Encode(out, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
}

func TestWebThumbnailCacheAndAccess(t *testing.T) {
	d, tg, db, path := accessTestDaemon(t)
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "downloads")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := &thumbnailTelegram{Telegram: tg}
	d.opts.Telegram = fake
	d.opts.WebCacheRoot = root
	d.opts.WebCacheBytes = 128 << 10
	d.opts.WebCacheTTL = time.Hour
	d.thumbSlots = make(chan struct{}, 3)
	policy := func(block bool) {
		data := map[string]any{"downloads": []string{root}}
		if block {
			data["block"] = []int64{77}
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		writeAccess(t, path, string(encoded))
	}
	policy(false)
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 77, Type: "user"}}); err != nil {
		t.Fatal(err)
	}
	photo := "photo"
	if err := db.UpsertMessages(ctx, []store.Message{
		{ID: 1, ChatID: 77, MediaType: &photo, Raw: json.RawMessage(`{"_":"message"}`)},
		{ID: 2, ChatID: 77, MediaType: &photo, Raw: json.RawMessage(`{"_":"message","noforwards":true}`)},
	}); err != nil {
		t.Fatal(err)
	}

	load := func(id int) error {
		_, err := d.webThumb(ctx, params(t, rpc.WebThumbnailParams{Chat: "77", ID: id}))
		return err
	}
	if err := load(2); err == nil || fake.fetches != 0 {
		t.Fatalf("protected thumbnail requested: %v, fetches=%d", err, fake.fetches)
	}
	for range 2 {
		if err := load(1); err != nil {
			t.Fatal(err)
		}
	}
	if fake.fetches != 1 {
		t.Fatalf("cache miss: %d fetches", fake.fetches)
	}
	policy(true)
	if err := load(1); err == nil {
		t.Fatal("thumbnail served after access revoked")
	}
}
