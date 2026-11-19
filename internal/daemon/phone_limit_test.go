package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func TestPhoneLimiterSpacingAndWindow(t *testing.T) {
	var l phoneLimiter
	start := time.Unix(100000, 0)
	for i := 0; i < 20; i++ {
		if err := l.take(start.Add(time.Duration(i) * 3 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.take(start.Add(60 * time.Second)); writeCode(t, err) != rpc.CodeFloodWait {
		t.Fatalf("21st: %v", err)
	}
	if err := l.take(start.Add(24 * time.Hour)); err != nil {
		t.Fatalf("expired: %v", err)
	}
	if err := l.take(start.Add(24*time.Hour + time.Second)); writeCode(t, err) != rpc.CodeFloodWait {
		t.Fatalf("window: %v", err)
	}
}

// audit05FloodRetry returns the retry_after_seconds of a flood-wait refusal.
func audit05FloodRetry(t *testing.T, err error) int {
	t.Helper()
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFloodWait {
		t.Fatalf("want flood wait, got %v", err)
	}
	data, ok := rpcErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("flood wait data %#v", rpcErr.Data)
	}
	seconds, ok := data["retry_after_seconds"].(int)
	if !ok {
		t.Fatalf("retry_after_seconds %#v", data["retry_after_seconds"])
	}
	return seconds
}

// TestAudit05_PhoneLimiterSpacingAlone exercises the 3 second interval with far
// fewer than 20 calls, so the daily window cannot be the cause of a refusal.
func TestAudit05_PhoneLimiterSpacingAlone(t *testing.T) {
	var l phoneLimiter
	start := time.Unix(100000, 0)
	if err := l.take(start); err != nil {
		t.Fatal(err)
	}
	if got := audit05FloodRetry(t, l.take(start.Add(2*time.Second))); got != 1 {
		t.Fatalf("retry_after_seconds = %d, want 1", got)
	}
	if err := l.take(start.Add(3 * time.Second)); err != nil {
		t.Fatalf("after the interval: %v", err)
	}
}

// TestAudit05_PhoneLimiterWindowRetryAfter checks the window rule's retry hint:
// the 21st call must wait until the oldest call leaves the 24 hour window.
func TestAudit05_PhoneLimiterWindowRetryAfter(t *testing.T) {
	var l phoneLimiter
	start := time.Unix(100000, 0)
	for i := 0; i < 20; i++ {
		if err := l.take(start.Add(time.Duration(i) * 3 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	now := start.Add(2 * time.Hour)
	want := int((24*time.Hour - 2*time.Hour).Seconds())
	if got := audit05FloodRetry(t, l.take(now)); got != want {
		t.Fatalf("retry_after_seconds = %d, want %d", got, want)
	}
}

// TestAudit05_PhoneLimiterCompletedShiftsInterval: the interval is measured from
// the completion of the previous lookup, which is later than its start.
func TestAudit05_PhoneLimiterCompletedShiftsInterval(t *testing.T) {
	var l phoneLimiter
	start := time.Unix(100000, 0)
	if err := l.take(start); err != nil {
		t.Fatal(err)
	}
	l.completed(start.Add(2 * time.Second))
	if got := audit05FloodRetry(t, l.take(start.Add(3*time.Second))); got != 2 {
		t.Fatalf("retry_after_seconds = %d, want 2", got)
	}
	if err := l.take(start.Add(5 * time.Second)); err != nil {
		t.Fatalf("after the shifted interval: %v", err)
	}
}

type phoneFake struct {
	*usersFake
	missing bool
	calls   int
}

func (f *phoneFake) Resolve(ctx context.Context, ref chatref.Ref) (rpc.Chat, error) {
	if ref.Kind == chatref.Phone && f.missing {
		return rpc.Chat{}, rpc.ErrChatNotFound()
	}
	return f.fakeTelegram.Resolve(ctx, ref)
}
func (f *phoneFake) ResolvePhone(context.Context, string) (rpc.Chat, error) {
	f.calls++
	return rpc.Chat{ID: 77, Type: "user"}, nil
}
func TestPhoneProfileFallbackAndAccess(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	base.resolved = &rpc.Chat{ID: 77, Type: "user"}
	f := &phoneFake{usersFake: &usersFake{fakeTelegram: base}}
	d.opts.Telegram = f
	p := rpc.UserRefParams{User: "+15551234567"}
	if _, err := d.userProfile(context.Background(), params(t, p)); err != nil || f.calls != 0 {
		t.Fatalf("contact: %v calls=%d", err, f.calls)
	}
	f.missing = true
	if _, err := d.userProfile(context.Background(), params(t, p)); err != nil || f.calls != 1 {
		t.Fatalf("fallback: %v calls=%d", err, f.calls)
	}
	person, err := db.GetPerson(context.Background(), 77)
	if err != nil || person.IsContact == nil || *person.IsContact {
		t.Fatalf("person: %+v %v", person, err)
	}
	d.phoneLimit = phoneLimiter{}
	writeAccess(t, path, `{"block":[77]}`)
	if _, err := d.userProfile(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("blocked: %v", err)
	}
	f.missing = false
	if _, err := d.userProfile(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeNotFound || f.calls != 2 {
		t.Fatalf("blocked contact: %v calls=%d", err, f.calls)
	}
	f.missing = true
	if _, err := d.userBlock(context.Background(), params(t, rpc.BlockParams{User: p.User, DryRun: true})); writeCode(t, err) != rpc.CodeNotFound || f.calls != 2 {
		t.Fatalf("other command: %v calls=%d", err, f.calls)
	}
}

func TestPhoneProfileLimitStopsTelegram(t *testing.T) {
	d, base, _, _ := accessTestDaemon(t)
	f := &phoneFake{usersFake: &usersFake{fakeTelegram: base}, missing: true}
	d.opts.Telegram = f
	start := time.Now().Add(-2 * time.Hour)
	for i := 0; i < 20; i++ {
		if err := d.phoneLimit.take(start.Add(time.Duration(i) * 3 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := d.userProfile(context.Background(), params(t, rpc.UserRefParams{User: "+15551234567"}))
	if writeCode(t, err) != rpc.CodeFloodWait || f.calls != 0 {
		t.Fatalf("limit: %v calls=%d", err, f.calls)
	}
}
