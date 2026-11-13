package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type fakeTelegram struct {
	ready chan struct{}

	mu       sync.Mutex
	user     *rpc.User
	chats    []rpc.Chat
	need2FA  bool
	wantCode string
	wantPass string
	phones   []string
	resends  int

	qrScan    chan struct{} // the test closes it to "scan" the QR code
	qrRefresh chan struct{} // the test sends to refresh the QR token

	// What the read methods saw and what they return.
	lastRef      chatref.Ref
	lastQuery    rpc.HistoryQuery
	lastLimit    int
	lastType     string
	messages     []rpc.Message
	missing      bool // the read methods answer "not found"
	resolveCalls int
	resolved     *rpc.Chat
}

func newFake() *fakeTelegram {
	return &fakeTelegram{
		ready: make(chan struct{}), wantCode: "12345", wantPass: "hunter2",
		qrScan: make(chan struct{}), qrRefresh: make(chan struct{}),
	}
}

func (f *fakeTelegram) Run(ctx context.Context) error {
	close(f.ready)
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeTelegram) Ready() <-chan struct{} { return f.ready }

func (f *fakeTelegram) Self(context.Context) (*rpc.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.user == nil {
		return nil, nil
	}
	u := *f.user
	return &u, nil
}

func (f *fakeTelegram) Login(ctx context.Context, phone string, code func(context.Context, rpc.CodePrompt) (string, bool, error), password func(context.Context) (string, error)) error {
	f.mu.Lock()
	f.phones = append(f.phones, phone)
	f.mu.Unlock()
	prompt := rpc.CodePrompt{Delivery: rpc.DeliveryApp, NextDelivery: rpc.DeliverySMS, ResendAfterSeconds: 30}
	var c string
	for {
		answer, resend, err := code(ctx, prompt)
		if err != nil {
			return err
		}
		if !resend {
			c = answer
			break
		}
		f.mu.Lock()
		f.resends++
		f.mu.Unlock()
		prompt = rpc.CodePrompt{Delivery: rpc.DeliverySMS}
	}
	if c != f.wantCode {
		return rpc.NewError(rpc.CodeTelegram, "PHONE_CODE_INVALID")
	}
	if f.need2FA {
		p, err := password(ctx)
		if err != nil {
			return err
		}
		if p != f.wantPass {
			return rpc.NewError(rpc.CodeTelegram, "PASSWORD_HASH_INVALID")
		}
	}
	f.mu.Lock()
	f.user = &rpc.User{ID: 42, Username: "tester", FirstName: "Test"}
	f.mu.Unlock()
	return nil
}

func (f *fakeTelegram) LoginQR(ctx context.Context, show func(context.Context, rpc.QRToken) error, password func(context.Context) (string, error)) error {
	n := 1
	token := func() rpc.QRToken {
		return rpc.QRToken{URL: fmt.Sprintf("tg://login?token=T%d", n), ExpiresInSeconds: 30}
	}
	if err := show(ctx, token()); err != nil {
		return err
	}
	for scanned := false; !scanned; {
		select {
		case <-f.qrRefresh:
			n++
			if err := show(ctx, token()); err != nil {
				return err
			}
		case <-f.qrScan:
			scanned = true
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.need2FA {
		p, err := password(ctx)
		if err != nil {
			return err
		}
		if p != f.wantPass {
			return rpc.NewError(rpc.CodeTelegram, "PASSWORD_HASH_INVALID")
		}
	}
	f.mu.Lock()
	f.user = &rpc.User{ID: 42, Username: "tester", FirstName: "Test"}
	f.mu.Unlock()
	return nil
}

func (f *fakeTelegram) Chats(_ context.Context, limit int, chatType string) ([]rpc.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastType = chatType
	var out []rpc.Chat
	for _, c := range f.chats {
		if chatType == "" || c.Type == chatType {
			out = append(out, c)
		}
	}
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeTelegram) Resolve(_ context.Context, ref chatref.Ref) (rpc.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRef = ref
	f.resolveCalls++
	if f.missing {
		return rpc.Chat{}, rpc.NewError(rpc.CodeNotFound, "not found")
	}
	if f.resolved != nil {
		return *f.resolved, nil
	}
	return rpc.Chat{ID: 77, Type: "supergroup", Title: "Team", Username: "team"}, nil
}

func (f *fakeTelegram) ChatInfo(_ context.Context, ref chatref.Ref) (rpc.ChatInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRef = ref
	if f.missing {
		return rpc.ChatInfo{}, rpc.NewError(rpc.CodeNotFound, "not found")
	}
	return rpc.ChatInfo{ID: 77, Type: "supergroup", Title: "Team", About: "About", Members: 12}, nil
}

func (f *fakeTelegram) InviteInfo(_ context.Context, ref chatref.Ref) (rpc.InviteInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRef = ref
	return rpc.InviteInfo{Title: "Private team", Type: "group", Members: 5}, nil
}

func (f *fakeTelegram) History(_ context.Context, ref chatref.Ref, q rpc.HistoryQuery) (rpc.MessagesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRef, f.lastQuery = ref, q
	if f.missing {
		return rpc.MessagesResult{}, rpc.NewError(rpc.CodeNotFound, "not found")
	}
	res := rpc.MessagesResult{Messages: f.messages}
	if q.Limit < len(f.messages) {
		res.Messages = f.messages[:q.Limit]
		res.NextCursor = "CURSOR-AFTER"
	}
	return res, nil
}

func (f *fakeTelegram) Message(_ context.Context, ref chatref.Ref, id int) (rpc.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRef = ref
	for _, m := range f.messages {
		if m.ID == id {
			return m, nil
		}
	}
	return rpc.Message{}, rpc.NewError(rpc.CodeNotFound, "message not found")
}

func (f *fakeTelegram) Pinned(_ context.Context, ref chatref.Ref, limit int) (rpc.MessagesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRef, f.lastLimit = ref, limit
	return rpc.MessagesResult{}, nil // nil slice: the handler must return an empty list
}

// shortTempDir returns a short directory: Unix socket paths are length-limited
// and t.TempDir is too long on macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tgd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type running struct {
	paths  Paths
	client *rpc.Client
	cancel context.CancelFunc
	logs   *safeBuffer

	finished chan struct{} // closed when Run returned
	err      error         // Run result, valid after finished is closed
}

func start(t *testing.T, tg Telegram, home string) *running {
	t.Helper()
	paths, err := ResolvePaths(home, "test")
	if err != nil {
		t.Fatal(err)
	}
	logs := &safeBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan struct{})
	r := &running{paths: paths, cancel: cancel, logs: logs, finished: make(chan struct{})}
	go func() {
		defer close(r.finished)
		r.err = Run(ctx, Options{
			Profile:     "test",
			Paths:       paths,
			Telegram:    tg,
			Log:         slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			OnListening: func() { close(listening) },
		})
	}()
	select {
	case <-listening:
	case <-r.finished:
		cancel()
		t.Fatalf("daemon exited early: %v", r.err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("daemon did not start")
	}
	c, err := rpc.Dial(context.Background(), "unix", paths.Socket)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	r.client = c
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
		select {
		case <-r.finished:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	return r
}

func ctxTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func rpcCode(t *testing.T, err error) int {
	t.Helper()
	var e *rpc.Error
	if !errors.As(err, &e) {
		t.Fatalf("want *rpc.Error, got %v", err)
	}
	return e.Code
}

func TestResolvePaths(t *testing.T) {
	p, err := ResolvePaths("/h", "work")
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != "/h/work" || p.Socket != "/h/work/daemon.sock" || p.Lock != "/h/work/daemon.lock" || p.Session != "/h/work/session.json" || p.Client != "/h/work/client" || p.Device != "/h/work/device" || p.Database != "/h/work/store.db" {
		t.Errorf("unexpected paths %+v", p)
	}
	for _, bad := range []string{"", "Work", "../x", "a/b", "-a", strings.Repeat("a", 33)} {
		if _, err := ResolvePaths("/h", bad); err == nil {
			t.Errorf("profile %q must be rejected", bad)
		}
	}
	if _, err := ResolvePaths("/"+strings.Repeat("d", MaxSocketPath), "p"); err == nil {
		t.Error("a too long socket path must be rejected")
	}
}

func TestSocketPathLimitMatchesTheOS(t *testing.T) {
	// MaxSocketPath bytes must bind; one more must not (macOS limit is 104 with NUL).
	dir := shortTempDir(t)
	fit := filepath.Join(dir, strings.Repeat("a", MaxSocketPath-len(dir)-1))
	l, err := listenUnix(fit)
	if err != nil {
		t.Fatalf("a path of %d bytes must bind: %v", len(fit), err)
	}
	_ = l.Close()
	over := fit + strings.Repeat("a", 6)
	if l, err := listenUnix(over); err == nil {
		_ = l.Close()
		t.Logf("this OS accepts a socket path of %d bytes; the limit stays conservative", len(over))
	}
}

func TestPingStatusShutdown(t *testing.T) {
	fake := newFake()
	r := start(t, fake, shortTempDir(t))

	var pong rpc.PingResult
	if err := r.client.Call(ctxTimeout(t), rpc.MethodPing, nil, &pong); err != nil || !pong.Pong {
		t.Fatalf("ping: %v %+v", err, pong)
	}
	var st rpc.StatusResult
	if err := r.client.Call(ctxTimeout(t), rpc.MethodStatus, nil, &st); err != nil {
		t.Fatal(err)
	}
	if st.Profile != "test" || st.PID != os.Getpid() || !st.Connected || st.Authorized || st.ActiveRequests != 0 {
		t.Errorf("unexpected status %+v", st)
	}

	var sd rpc.ShutdownResult
	if err := r.client.Call(ctxTimeout(t), rpc.MethodShutdown, nil, &sd); err != nil || !sd.Stopping {
		t.Fatalf("shutdown: %v %+v", err, sd)
	}
	select {
	case <-r.finished:
		if r.err != nil {
			t.Errorf("clean stop returned %v", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after shutdown")
	}
	if _, err := os.Stat(r.paths.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket file must be removed, stat error: %v", err)
	}
}

func TestSecondDaemonOnSameProfileIsRefused(t *testing.T) {
	home := shortTempDir(t)
	r := start(t, newFake(), home)
	err := Run(context.Background(), Options{Profile: "test", Paths: r.paths, Telegram: newFake()})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
	// The refused attempt must not damage the running daemon.
	if err := r.client.Call(ctxTimeout(t), rpc.MethodPing, nil, nil); err != nil {
		t.Errorf("first daemon broken by the second attempt: %v", err)
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	home := shortTempDir(t)
	paths, err := ResolvePaths(home, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Socket, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := start(t, newFake(), home)
	if err := r.client.Call(ctxTimeout(t), rpc.MethodPing, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFilePermissions(t *testing.T) {
	r := start(t, newFake(), shortTempDir(t))
	for path, want := range map[string]os.FileMode{
		r.paths.Dir:    0o700,
		r.paths.Socket: 0o600,
		r.paths.Lock:   0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
}

func TestLoginWithPassword(t *testing.T) {
	fake := newFake()
	fake.need2FA = true
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	var res rpc.AuthResult
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.State != rpc.AuthCodeRequired || res.Delivery != rpc.DeliveryApp || res.NextDelivery != rpc.DeliverySMS || res.ResendAfterSeconds != 30 {
		t.Fatalf("after start: %+v", res)
	}
	var st rpc.StatusResult
	_ = r.client.Call(ctx, rpc.MethodStatus, nil, &st)
	if st.LoginState != rpc.AuthCodeRequired || st.Authorized {
		t.Errorf("status during login: %+v", st)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthCode, rpc.AuthCodeParams{Code: "12345"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.State != rpc.AuthPasswordRequired {
		t.Fatalf("after code: %+v", res)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthPassword, rpc.AuthPasswordParams{Password: "hunter2"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.State != rpc.AuthAuthorized || res.User == nil || res.User.ID != 42 {
		t.Fatalf("after password: %+v", res)
	}
	st = rpc.StatusResult{}
	_ = r.client.Call(ctx, rpc.MethodStatus, nil, &st)
	if !st.Authorized || st.User == nil || st.LoginState != "" {
		t.Errorf("status after login: %+v", st)
	}
}

func TestLoginWithoutPassword(t *testing.T) {
	r := start(t, newFake(), shortTempDir(t))
	ctx := ctxTimeout(t)
	var res rpc.AuthResult
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, &res); err != nil {
		t.Fatal(err)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthCode, rpc.AuthCodeParams{Code: "12345"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.State != rpc.AuthAuthorized {
		t.Errorf("an account without 2FA must be authorized after the code: %+v", res)
	}
}

func TestLoginStepsOutOfOrderAreRejected(t *testing.T) {
	fake := newFake()
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	err := r.client.Call(ctx, rpc.MethodAuthCode, rpc.AuthCodeParams{Code: "1"}, nil)
	if rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("code without start: %v", err)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{}, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Errorf("start without phone: %v", err)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, nil); err != nil {
		t.Fatal(err)
	}
	err = r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, nil)
	if rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("second start while in progress: %v", err)
	}
	err = r.client.Call(ctx, rpc.MethodAuthPassword, rpc.AuthPasswordParams{Password: "x"}, nil)
	if rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("password while waiting for the code: %v", err)
	}
	var cancel rpc.AuthCancelResult
	if err := r.client.Call(ctx, rpc.MethodAuthCancel, nil, &cancel); err != nil || !cancel.Canceled {
		t.Errorf("cancel: %v %+v", err, cancel)
	}
	// After a cancel a new login can start.
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, nil); err != nil {
		t.Errorf("start after cancel: %v", err)
	}
}

func TestWrongCodeEndsTheLoginAndAllowsRetry(t *testing.T) {
	r := start(t, newFake(), shortTempDir(t))
	ctx := ctxTimeout(t)
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, nil); err != nil {
		t.Fatal(err)
	}
	err := r.client.Call(ctx, rpc.MethodAuthCode, rpc.AuthCodeParams{Code: "00000"}, nil)
	if rpcCode(t, err) != rpc.CodeTelegram {
		t.Fatalf("wrong code: %v", err)
	}
	var st rpc.StatusResult
	_ = r.client.Call(ctx, rpc.MethodStatus, nil, &st)
	if st.LoginState != "" {
		t.Errorf("a failed login must be cleared, state %q", st.LoginState)
	}
	var res rpc.AuthResult
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, &res); err != nil || res.State != rpc.AuthCodeRequired {
		t.Errorf("retry: %v %+v", err, res)
	}
}

func TestStartWhenAlreadyLoggedIn(t *testing.T) {
	fake := newFake()
	fake.user = &rpc.User{ID: 1}
	r := start(t, fake, shortTempDir(t))
	err := r.client.Call(ctxTimeout(t), rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, nil)
	if rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("start when logged in: %v", err)
	}
}

func TestChatList(t *testing.T) {
	fake := newFake()
	fake.chats = []rpc.Chat{{ID: 1, Type: "user", Title: "A"}, {ID: -2, Type: "group", Title: "B"}, {ID: -1000000000003, Type: "channel", Title: "C"}}
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	err := r.client.Call(ctx, rpc.MethodChatList, nil, nil)
	if rpcCode(t, err) != rpc.CodeNotAuthorized {
		t.Fatalf("chat.list without login: %v", err)
	}

	fake.mu.Lock()
	fake.user = &rpc.User{ID: 1}
	fake.mu.Unlock()
	var res rpc.ChatListResult
	if err := r.client.Call(ctx, rpc.MethodChatList, nil, &res); err != nil || len(res.Chats) != 3 {
		t.Fatalf("chat.list: %v %+v", err, res)
	}
	if err := r.client.Call(ctx, rpc.MethodChatList, rpc.ChatListParams{Limit: 2}, &res); err != nil || len(res.Chats) != 2 {
		t.Fatalf("chat.list limit 2: %v %+v", err, res)
	}
	for _, limit := range []int{-1, maxChatLimit + 1} {
		err := r.client.Call(ctx, rpc.MethodChatList, rpc.ChatListParams{Limit: limit}, nil)
		if rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
}

func TestSecretsAreNeverLogged(t *testing.T) {
	fake := newFake()
	fake.need2FA = true
	fake.wantCode = "SECRETCODE"
	fake.wantPass = "SECRETPASSWORD"
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)
	_ = r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+SECRETPHONE"}, nil)
	_ = r.client.Call(ctx, rpc.MethodAuthCode, rpc.AuthCodeParams{Code: "SECRETCODE"}, nil)
	_ = r.client.Call(ctx, rpc.MethodAuthPassword, rpc.AuthPasswordParams{Password: "SECRETPASSWORD"}, nil)

	_ = r.client.Close()
	r.cancel()
	<-r.finished
	for _, secret := range []string{"SECRETCODE", "SECRETPASSWORD", "SECRETPHONE"} {
		if strings.Contains(r.logs.String(), secret) {
			t.Errorf("log contains %s:\n%s", secret, r.logs.String())
		}
	}
}

func TestResendCode(t *testing.T) {
	fake := newFake()
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	var res rpc.AuthResult
	// Resend before the login started is refused.
	if err := r.client.Call(ctx, rpc.MethodAuthResend, nil, nil); rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("resend without a login: %v", err)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+10000000000"}, &res); err != nil {
		t.Fatal(err)
	}
	var resent rpc.AuthResult // a fresh value: omitted fields must not carry over
	if err := r.client.Call(ctx, rpc.MethodAuthResend, nil, &resent); err != nil {
		t.Fatal(err)
	}
	if resent.State != rpc.AuthCodeRequired || resent.Delivery != rpc.DeliverySMS || resent.NextDelivery != "" {
		t.Fatalf("after resend: %+v", resent)
	}
	fake.mu.Lock()
	resends := fake.resends
	fake.mu.Unlock()
	if resends != 1 {
		t.Errorf("Telegram was asked to resend %d times, want 1", resends)
	}
	// The login goes on with the code from the second delivery.
	if err := r.client.Call(ctx, rpc.MethodAuthCode, rpc.AuthCodeParams{Code: "12345"}, &res); err != nil || res.State != rpc.AuthAuthorized {
		t.Errorf("code after resend: %v %+v", err, res)
	}
	// A resend while a password or nothing is awaited is refused.
	if err := r.client.Call(ctx, rpc.MethodAuthResend, nil, nil); rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("resend after the login ended: %v", err)
	}
}

// waitAsync calls auth.wait in the background and returns its result.
func waitAsync(t *testing.T, r *running) <-chan rpc.AuthResult {
	t.Helper()
	out := make(chan rpc.AuthResult, 1)
	go func() {
		var res rpc.AuthResult
		if err := r.client.Call(context.Background(), rpc.MethodAuthWait, nil, &res); err != nil {
			t.Errorf("auth.wait: %v", err)
		}
		out <- res
	}()
	return out
}

func receive(t *testing.T, ch <-chan rpc.AuthResult) rpc.AuthResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
		return rpc.AuthResult{}
	}
}

func TestQRLogin(t *testing.T) {
	fake := newFake()
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	var res rpc.AuthResult
	if err := r.client.Call(ctx, rpc.MethodAuthStartQR, nil, &res); err != nil {
		t.Fatal(err)
	}
	if res.State != rpc.AuthQRScan || res.QRURL != "tg://login?token=T1" || res.ExpiresInSeconds != 30 {
		t.Fatalf("after start_qr: %+v", res)
	}
	var st rpc.StatusResult
	_ = r.client.Call(ctx, rpc.MethodStatus, nil, &st)
	if st.LoginState != rpc.AuthQRScan {
		t.Errorf("status during the QR login: %+v", st)
	}

	// The token refreshes while the user has not scanned yet.
	next := waitAsync(t, r)
	fake.qrRefresh <- struct{}{}
	if res := receive(t, next); res.State != rpc.AuthQRScan || res.QRURL != "tg://login?token=T2" {
		t.Fatalf("after a refresh: %+v", res)
	}
	// The user scans it.
	next = waitAsync(t, r)
	close(fake.qrScan)
	if res := receive(t, next); res.State != rpc.AuthAuthorized || res.User == nil || res.User.ID != 42 {
		t.Fatalf("after the scan: %+v", res)
	}
}

func TestQRLoginWithPassword(t *testing.T) {
	fake := newFake()
	fake.need2FA = true
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	if err := r.client.Call(ctx, rpc.MethodAuthStartQR, nil, nil); err != nil {
		t.Fatal(err)
	}
	next := waitAsync(t, r)
	close(fake.qrScan)
	if res := receive(t, next); res.State != rpc.AuthPasswordRequired {
		t.Fatalf("an account with 2FA must ask for the password: %+v", res)
	}
	var res rpc.AuthResult
	if err := r.client.Call(ctx, rpc.MethodAuthPassword, rpc.AuthPasswordParams{Password: "hunter2"}, &res); err != nil || res.State != rpc.AuthAuthorized {
		t.Errorf("password after the scan: %v %+v", err, res)
	}
}

func TestQRScanBeforeWait(t *testing.T) {
	fake := newFake()
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	if err := r.client.Call(ctx, rpc.MethodAuthStartQR, nil, nil); err != nil {
		t.Fatal(err)
	}
	close(fake.qrScan) // the login ends before the client asks again
	deadline := time.Now().Add(5 * time.Second)
	for {
		var st rpc.StatusResult
		_ = r.client.Call(ctx, rpc.MethodStatus, nil, &st)
		if st.Authorized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the login did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var res rpc.AuthResult
	if err := r.client.Call(ctx, rpc.MethodAuthWait, nil, &res); err != nil || res.State != rpc.AuthAuthorized {
		t.Fatalf("the final event must not be lost: %v %+v", err, res)
	}
	// It is delivered once.
	if err := r.client.Call(ctx, rpc.MethodAuthWait, nil, nil); rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("a second wait after the end: %v", err)
	}
}

func TestQRWaitWithoutLoginAndCancel(t *testing.T) {
	fake := newFake()
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)
	if err := r.client.Call(ctx, rpc.MethodAuthWait, nil, nil); rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("wait without a login: %v", err)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthStartQR, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthStartQR, nil, nil); rpcCode(t, err) != rpc.CodeLoginState {
		t.Errorf("a second login while one runs: %v", err)
	}
	var c rpc.AuthCancelResult
	if err := r.client.Call(ctx, rpc.MethodAuthCancel, nil, &c); err != nil || !c.Canceled {
		t.Fatalf("cancel: %v %+v", err, c)
	}
	if err := r.client.Call(ctx, rpc.MethodAuthStartQR, nil, nil); err != nil {
		t.Errorf("start after cancel: %v", err)
	}
}

func loggedIn(fake *fakeTelegram) {
	fake.mu.Lock()
	fake.user = &rpc.User{ID: 1}
	fake.mu.Unlock()
}

func TestReadMethodsNeedLogin(t *testing.T) {
	r := start(t, newFake(), shortTempDir(t))
	ctx := ctxTimeout(t)
	for method, params := range map[string]any{
		rpc.MethodChatResolve:    rpc.ChatRefParams{Chat: "@telegram"},
		rpc.MethodChatInfo:       rpc.ChatRefParams{Chat: "@telegram"},
		rpc.MethodChatInviteInfo: rpc.ChatRefParams{Chat: "t.me/+AbCdEfGh1234"},
		rpc.MethodMessageHistory: rpc.HistoryParams{Chat: "@telegram"},
		rpc.MethodMessageGet:     rpc.MessageGetParams{Chat: "@telegram", ID: 1},
		rpc.MethodMessagePinned:  rpc.PinnedParams{Chat: "@telegram"},
	} {
		if err := r.client.Call(ctx, method, params, nil); rpcCode(t, err) != rpc.CodeNotAuthorized {
			t.Errorf("%s without a login: %v", method, err)
		}
	}
}

func TestChatReferencesAreParsedBeforeAnyRequest(t *testing.T) {
	fake := newFake()
	loggedIn(fake)
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	// A reference of every kind reaches Telegram in parsed form.
	for in, want := range map[string]chatref.Ref{
		"@Telegram":      {Kind: chatref.Username, Username: "telegram"},
		"-1001234567890": {Kind: chatref.ID, ID: -1001234567890},
		"+15551234567":   {Kind: chatref.Phone, Phone: "15551234567"},
	} {
		var chat rpc.Chat
		if err := r.client.Call(ctx, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: in}, &chat); err != nil || chat.ID != 77 {
			t.Fatalf("resolve %q: %v %+v", in, err, chat)
		}
		fake.mu.Lock()
		got := fake.lastRef
		fake.mu.Unlock()
		if got != want {
			t.Errorf("%q reached Telegram as %+v, want %+v", in, got, want)
		}
	}
	// A bad reference is refused without calling Telegram.
	fake.mu.Lock()
	fake.lastRef = chatref.Ref{}
	fake.mu.Unlock()
	for _, bad := range []string{"", "@a", "has space"} {
		err := r.client.Call(ctx, rpc.MethodChatResolve, rpc.ChatRefParams{Chat: bad}, nil)
		if rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("reference %q: %v", bad, err)
		}
	}
	fake.mu.Lock()
	touched := fake.lastRef != chatref.Ref{}
	fake.mu.Unlock()
	if touched {
		t.Error("an invalid reference must not reach Telegram")
	}
	// An invite link is for invite_info only, and invite_info takes nothing else.
	for _, method := range []string{rpc.MethodChatResolve, rpc.MethodChatInfo} {
		err := r.client.Call(ctx, method, rpc.ChatRefParams{Chat: "t.me/+AbCdEfGh1234"}, nil)
		if rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("%s with an invite link: %v", method, err)
		}
	}
	if err := r.client.Call(ctx, rpc.MethodChatInviteInfo, rpc.ChatRefParams{Chat: "@telegram"}, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Errorf("invite_info with a username: %v", err)
	}
	var invite rpc.InviteInfo
	if err := r.client.Call(ctx, rpc.MethodChatInviteInfo, rpc.ChatRefParams{Chat: "t.me/+AbCdEfGh1234"}, &invite); err != nil || invite.Title != "Private team" {
		t.Errorf("invite_info: %v %+v", err, invite)
	}
}

func TestHistoryParameters(t *testing.T) {
	fake := newFake()
	loggedIn(fake)
	fake.messages = []rpc.Message{{ID: 3, Text: "c"}, {ID: 2, Text: "b"}, {ID: 1, Text: "a"}}
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)
	call := func(p rpc.HistoryParams) (rpc.MessagesResult, error) {
		var res rpc.MessagesResult
		err := r.client.Call(ctx, rpc.MethodMessageHistory, p, &res)
		return res, err
	}
	seen := func() rpc.HistoryQuery {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.lastQuery
	}

	// Defaults: 20 messages, the size cut of the protocol is passed down.
	res, err := call(rpc.HistoryParams{Chat: "@telegram"})
	if err != nil || len(res.Messages) != 3 || res.NextCursor != "" {
		t.Fatalf("default call: %v %+v", err, res)
	}
	if q := seen(); q.Limit != 20 || q.MaxBytes != 768<<10 {
		t.Errorf("defaults reached Telegram as %+v", q)
	}
	// A smaller limit gives a cursor and passes every parameter.
	res, err = call(rpc.HistoryParams{Chat: "@telegram", Limit: 2, Since: "2026-01-01T00:00:00Z", Until: "2026-02-01T10:00:00+03:00", Topic: 5, Cursor: "C"})
	if err != nil || len(res.Messages) != 2 || res.NextCursor != "CURSOR-AFTER" {
		t.Fatalf("limit 2: %v %+v", err, res)
	}
	q := seen()
	if q.Limit != 2 || q.Topic != 5 || q.Cursor != "C" ||
		!q.Since.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) ||
		!q.Until.Equal(time.Date(2026, 2, 1, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("parameters reached Telegram as %+v", q)
	}

	for name, p := range map[string]rpc.HistoryParams{
		"limit below range":   {Chat: "@telegram", Limit: -1},
		"limit above range":   {Chat: "@telegram", Limit: 101},
		"since is not a time": {Chat: "@telegram", Since: "yesterday"},
		"until is not a time": {Chat: "@telegram", Until: "2026-01-01"},
		"since after until":   {Chat: "@telegram", Since: "2026-02-01T00:00:00Z", Until: "2026-01-01T00:00:00Z"},
		"negative topic":      {Chat: "@telegram", Topic: -3},
		"invite link":         {Chat: "t.me/+AbCdEfGh1234"},
	} {
		if _, err := call(p); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("%s: %v", name, err)
		}
	}

	fake.mu.Lock()
	fake.missing = true
	fake.mu.Unlock()
	if _, err := call(rpc.HistoryParams{Chat: "@telegram"}); rpcCode(t, err) != rpc.CodeNotFound {
		t.Errorf("a missing chat: %v", err)
	}
}

func TestMessageGetAndPinned(t *testing.T) {
	fake := newFake()
	loggedIn(fake)
	fake.messages = []rpc.Message{{ID: 7, Text: "seven"}}
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	var msg rpc.Message
	if err := r.client.Call(ctx, rpc.MethodMessageGet, rpc.MessageGetParams{Chat: "@telegram", ID: 7}, &msg); err != nil || msg.Text != "seven" {
		t.Fatalf("get: %v %+v", err, msg)
	}
	if err := r.client.Call(ctx, rpc.MethodMessageGet, rpc.MessageGetParams{Chat: "@telegram", ID: 8}, nil); rpcCode(t, err) != rpc.CodeNotFound {
		t.Errorf("a missing message: %v", err)
	}
	for _, id := range []int{0, -1} {
		if err := r.client.Call(ctx, rpc.MethodMessageGet, rpc.MessageGetParams{Chat: "@telegram", ID: id}, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("id %d: %v", id, err)
		}
	}

	var res struct {
		Messages []rpc.Message `json:"messages"`
	}
	if err := r.client.Call(ctx, rpc.MethodMessagePinned, rpc.PinnedParams{Chat: "@telegram"}, &res); err != nil || res.Messages == nil || len(res.Messages) != 0 {
		t.Errorf("pinned must return an empty list, not null: %v %+v", err, res)
	}
	fake.mu.Lock()
	limit := fake.lastLimit
	fake.mu.Unlock()
	if limit != 20 {
		t.Errorf("the default pinned limit: %d", limit)
	}
	for _, limit := range []int{-1, 101} {
		if err := r.client.Call(ctx, rpc.MethodMessagePinned, rpc.PinnedParams{Chat: "@telegram", Limit: limit}, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("pinned limit %d: %v", limit, err)
		}
	}
}

func TestChatListTypeFilter(t *testing.T) {
	fake := newFake()
	loggedIn(fake)
	fake.chats = []rpc.Chat{{ID: 1, Type: "user"}, {ID: 2, Type: "bot"}, {ID: -3, Type: "group"}, {ID: 4, Type: "bot"}}
	r := start(t, fake, shortTempDir(t))
	ctx := ctxTimeout(t)

	var res rpc.ChatListResult
	if err := r.client.Call(ctx, rpc.MethodChatList, rpc.ChatListParams{Type: "bot"}, &res); err != nil || len(res.Chats) != 2 {
		t.Fatalf("bots: %v %+v", err, res)
	}
	if err := r.client.Call(ctx, rpc.MethodChatList, rpc.ChatListParams{Type: "robot"}, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Errorf("an unknown type: %v", err)
	}
}
