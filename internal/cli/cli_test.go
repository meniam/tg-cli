package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

type fakeTelegram struct {
	noChatAdmin
	ready chan struct{}
	once  sync.Once

	mu       sync.Mutex
	user     *rpc.User
	chats    []rpc.Chat
	need2FA  bool
	wantCode string
	wantPass string

	messages      []rpc.Message // newest first
	missing       bool          // the read methods answer "not found"
	resolveCalls  int
	peopleFixture bool

	audit01Calls map[string]int // calls of the chat and message read methods, by name
	audit01Refs  map[string]chatref.Ref
}

// audit01Remember keeps the last reference a read method was asked about.
func (f *fakeTelegram) audit01Remember(method string, ref chatref.Ref) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.audit01Refs == nil {
		f.audit01Refs = make(map[string]chatref.Ref)
	}
	f.audit01Refs[method] = ref
}

func (f *fakeTelegram) audit01LastRef(method string) (chatref.Ref, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := f.audit01Refs[method]
	return ref, ok
}

// audit01Record counts a call of a read method so a test can prove that a command
// answered without touching Telegram.
func (f *fakeTelegram) audit01Record(method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.audit01Calls == nil {
		f.audit01Calls = make(map[string]int)
	}
	f.audit01Calls[method]++
}

// audit01CallSnapshot returns a copy of the call counters.
func (f *fakeTelegram) audit01CallSnapshot() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.audit01Calls))
	for k, v := range f.audit01Calls {
		out[k] = v
	}
	return out
}

func (f *fakeTelegram) Run(ctx context.Context) error {
	// A test may start the daemon twice with the same fake: connect only once.
	f.once.Do(func() { close(f.ready) })
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeTelegram) Ready() <-chan struct{}                       { return f.ready }
func (f *fakeTelegram) SetEventSink(func(rpc.Event))                 {}
func (f *fakeTelegram) SetTranscriptionSink(func(rpc.Transcription)) {}
func (f *fakeTelegram) ChatStats(_ context.Context, _ chatref.Ref, _ string) (rpc.ChatStats, error) {
	f.audit01Record("ChatStats")
	return rpc.ChatStats{ChatID: 77, Kind: "megagroup"}, nil
}
func (f *fakeTelegram) ChatBoosts(_ context.Context, _ chatref.Ref) (rpc.ChatBoosts, error) {
	f.audit01Record("ChatBoosts")
	return rpc.ChatBoosts{ChatID: 77}, nil
}
func (f *fakeTelegram) Transcribe(_ context.Context, _ chatref.Ref, id int) (rpc.Transcription, error) {
	f.audit01Record("Transcribe")
	return rpc.Transcription{ChatID: 77, ID: id, TranscriptionID: 1, Text: "fixture"}, nil
}
func (f *fakeTelegram) Translate(_ context.Context, _ chatref.Ref, p rpc.TranslateParams) (rpc.TranslateResult, error) {
	f.audit01Record("Translate")
	items := make([]rpc.TranslationItem, 0, len(p.IDs))
	for _, id := range p.IDs {
		items = append(items, rpc.TranslationItem{ID: id, Text: "fixture"})
	}
	return rpc.TranslateResult{ChatID: 77, ToLang: p.ToLang, Items: items}, nil
}
func (f *fakeTelegram) FloodWaitRemaining() time.Duration   { return 0 }
func (f *fakeTelegram) CheckTextLocal(rpc.SendParams) error { return nil }
func (f *fakeTelegram) CheckText(_ context.Context, p rpc.SendParams) (rpc.TextCheck, error) {
	f.audit01Record("CheckText")
	return rpc.TextCheck{Mode: "plain", PlainLength: len([]rune(p.Text))}, nil
}

func (f *fakeTelegram) Send(context.Context, chatref.Ref, rpc.SendParams) (rpc.SendResult, error) {
	f.audit01Record("Send")
	return rpc.SendResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) SendMedia(context.Context, chatref.Ref, rpc.SendMediaParams, []rpc.MediaFile) (rpc.SendMediaResult, error) {
	f.audit01Record("SendMedia")
	return rpc.SendMediaResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) Edit(context.Context, chatref.Ref, rpc.EditParams) (rpc.SendResult, error) {
	f.audit01Record("Edit")
	return rpc.SendResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) Delete(context.Context, chatref.Ref, rpc.DeleteParams) (rpc.DeleteResult, error) {
	f.audit01Record("Delete")
	return rpc.DeleteResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) Forward(context.Context, chatref.Ref, chatref.Ref, rpc.ForwardParams) (rpc.ForwardResult, error) {
	f.audit01Record("Forward")
	return rpc.ForwardResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) React(context.Context, chatref.Ref, rpc.ReactParams) (rpc.ReactResult, error) {
	f.audit01Record("React")
	return rpc.ReactResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) Pin(context.Context, chatref.Ref, rpc.PinParams) (rpc.PinResult, error) {
	f.audit01Record("Pin")
	return rpc.PinResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) Unpin(context.Context, chatref.Ref, rpc.UnpinParams) (rpc.PinResult, error) {
	f.audit01Record("Unpin")
	return rpc.PinResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) SendPoll(context.Context, chatref.Ref, rpc.PollParams) (rpc.SendResult, error) {
	f.audit01Record("SendPoll")
	return rpc.SendResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) SearchMessages(context.Context, chatref.Ref, rpc.SearchMessagesParams) (rpc.MessagesResult, error) {
	f.audit01Record("SearchMessages")
	return rpc.MessagesResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) SearchPublic(context.Context, rpc.PublicSearchParams) (rpc.PublicSearchResult, error) {
	f.audit01Record("SearchPublic")
	return rpc.PublicSearchResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) MessageContext(context.Context, chatref.Ref, rpc.ContextParams) (rpc.MessagesResult, error) {
	f.audit01Record("MessageContext")
	return rpc.MessagesResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) Comments(context.Context, chatref.Ref, rpc.CommentsParams) (rpc.MessagesResult, error) {
	f.audit01Record("Comments")
	return rpc.MessagesResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) MessageLink(context.Context, chatref.Ref, rpc.LinkParams) (rpc.LinkResult, error) {
	f.audit01Record("MessageLink")
	return rpc.LinkResult{}, errors.New("not implemented")
}
func (f *fakeTelegram) DraftGet(context.Context, chatref.Ref) (rpc.Draft, error) {
	f.audit01Record("DraftGet")
	return rpc.Draft{}, nil
}
func (f *fakeTelegram) DraftList(context.Context) (rpc.DraftList, error) {
	f.audit01Record("DraftList")
	return rpc.DraftList{}, nil
}
func (f *fakeTelegram) DraftSet(context.Context, chatref.Ref, rpc.DraftParams) (rpc.DraftWriteResult, error) {
	f.audit01Record("DraftSet")
	return rpc.DraftWriteResult{}, nil
}
func (f *fakeTelegram) ScheduledList(context.Context, chatref.Ref) (rpc.ScheduledResult, error) {
	f.audit01Record("ScheduledList")
	return rpc.ScheduledResult{}, nil
}
func (f *fakeTelegram) ScheduledDelete(context.Context, chatref.Ref, []int) (rpc.ScheduledResult, error) {
	f.audit01Record("ScheduledDelete")
	return rpc.ScheduledResult{}, nil
}
func (f *fakeTelegram) ScheduledSendNow(context.Context, chatref.Ref, []int) (rpc.ScheduledResult, error) {
	f.audit01Record("ScheduledSendNow")
	return rpc.ScheduledResult{}, nil
}
func (f *fakeTelegram) Typing(context.Context, chatref.Ref, rpc.TypingParams) (rpc.TypingResult, error) {
	f.audit01Record("Typing")
	return rpc.TypingResult{}, nil
}

func (f *fakeTelegram) MarkRead(context.Context, chatref.Ref) (rpc.MarkReadResult, error) {
	f.audit01Record("MarkRead")
	return rpc.MarkReadResult{}, errors.New("not implemented")
}

func (f *fakeTelegram) Self(context.Context) (*rpc.User, error) {
	f.audit01Record("Self")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.user == nil {
		return nil, nil
	}
	u := *f.user
	return &u, nil
}

func (f *fakeTelegram) Login(ctx context.Context, _ string, code func(context.Context, rpc.CodePrompt) (string, bool, error), password func(context.Context) (string, error)) error {
	f.audit01Record("Login")
	prompt := rpc.CodePrompt{Delivery: rpc.DeliverySMS, NextDelivery: rpc.DeliveryCall, ResendAfterSeconds: 45}
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
		prompt = rpc.CodePrompt{Delivery: rpc.DeliveryCall}
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
	f.user = &rpc.User{ID: 42, Username: "tester"}
	f.mu.Unlock()
	return nil
}

func (f *fakeTelegram) LoginQR(ctx context.Context, show func(context.Context, rpc.QRToken) error, password func(context.Context) (string, error)) error {
	f.audit01Record("LoginQR")
	if err := show(ctx, rpc.QRToken{URL: "tg://login?token=FIRST", ExpiresInSeconds: 30}); err != nil {
		return err
	}
	select { // the "user" scans after the token refreshed once
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := show(ctx, rpc.QRToken{URL: "tg://login?token=SECOND", ExpiresInSeconds: 30}); err != nil {
		return err
	}
	select {
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
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
	f.user = &rpc.User{ID: 42, Username: "tester"}
	f.mu.Unlock()
	return nil
}

func (f *fakeTelegram) Chats(_ context.Context, limit int, chatType string) ([]rpc.Chat, error) {
	f.audit01Record("Chats")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rpc.Chat
	for _, c := range f.chats {
		if chatType == "" || c.Type == chatType {
			out = append(out, c)
		}
	}
	return out[:min(limit, len(out))], nil
}

func (f *fakeTelegram) Members(context.Context, chatref.Ref, rpc.MembersParams) (rpc.MembersResult, error) {
	f.audit01Record("Members")
	return rpc.MembersResult{Members: []rpc.Member{{ID: 5, Name: "Ann", Username: "ann", Role: "admin"}}, NextOffset: 1}, nil
}

func (f *fakeTelegram) Topics(context.Context, chatref.Ref, rpc.TopicsParams) (rpc.TopicsResult, error) {
	f.audit01Record("Topics")
	return rpc.TopicsResult{Topics: []rpc.Topic{{ID: 7, Title: "Foreign topic", TopMessage: 30, Created: "2026-11-14T10:00:00Z"}}, NextCursor: "TOPIC-NEXT"}, nil
}

func (f *fakeTelegram) CommonChats(context.Context, chatref.Ref, rpc.CommonChatsParams) (rpc.CommonChatsResult, error) {
	f.audit01Record("CommonChats")
	return rpc.CommonChatsResult{Chats: []rpc.CommonChat{{ID: -77, Type: "group", Title: "Shared"}}, NextCursor: "COMMON-NEXT"}, nil
}

func (f *fakeTelegram) Resolve(_ context.Context, ref chatref.Ref) (rpc.Chat, error) {
	f.audit01Record("Resolve")
	f.audit01Remember("Resolve", ref)
	f.mu.Lock()
	f.resolveCalls++
	f.mu.Unlock()
	if f.missing {
		return rpc.Chat{}, rpc.NewError(rpc.CodeNotFound, "not found: no such chat")
	}
	if ref.Kind == chatref.Username && ref.Username == "fixturebot" || ref.Kind == chatref.ID && ref.ID == 6 {
		return rpc.Chat{ID: 6, Type: "bot", Username: "fixturebot"}, nil
	}
	if ref.Kind == chatref.Username && ref.Username == "aliceuser" || ref.Kind == chatref.ID && ref.ID == 5 {
		return rpc.Chat{ID: 5, Type: "user", Title: "Ann", Username: "aliceuser"}, nil
	}
	return rpc.Chat{ID: 77, Type: "supergroup", Title: "Team", Username: "team"}, nil
}

func (f *fakeTelegram) ChatInfo(_ context.Context, ref chatref.Ref) (rpc.ChatInfo, error) {
	f.audit01Record("ChatInfo")
	f.audit01Remember("ChatInfo", ref)
	if f.missing {
		return rpc.ChatInfo{}, rpc.NewError(rpc.CodeNotFound, "not found: no such chat")
	}
	return rpc.ChatInfo{ID: 77, Type: "supergroup", Title: "Team", About: "About the team", Members: 12,
		CreatedAt: 1700000000, Scam: false, Fake: false, Restricted: true,
		RestrictionReason: []rpc.RestrictionReason{{Platform: "ios", Reason: "porno", Text: "Not available"}},
	}, nil
}

func (f *fakeTelegram) InviteInfo(_ context.Context, ref chatref.Ref) (rpc.InviteInfo, error) {
	f.audit01Record("InviteInfo")
	return rpc.InviteInfo{Title: "Private team", Type: "group", Members: 5}, nil
}

func (f *fakeTelegram) History(_ context.Context, ref chatref.Ref, q rpc.HistoryQuery) (rpc.MessagesResult, error) {
	f.audit01Record("History")
	if f.missing {
		return rpc.MessagesResult{}, rpc.NewError(rpc.CodeNotFound, "not found: no such chat")
	}
	res := rpc.MessagesResult{Messages: f.messages}
	if q.Limit < len(f.messages) {
		res.Messages = f.messages[:q.Limit]
		res.NextCursor = "CURSOR-AFTER"
	}
	return res, nil
}

func (f *fakeTelegram) HistoryPage(_ context.Context, _ chatref.Ref, _ int) (rpc.HistoryPage, error) {
	f.audit01Record("HistoryPage")
	return rpc.HistoryPage{}, nil
}

func (f *fakeTelegram) Message(_ context.Context, ref chatref.Ref, id int) (rpc.Message, error) {
	f.audit01Record("Message")
	for _, m := range f.messages {
		if m.ID == id {
			return m, nil
		}
	}
	return rpc.Message{}, rpc.NewError(rpc.CodeNotFound, "not found: no such message")
}

func (f *fakeTelegram) Pinned(_ context.Context, ref chatref.Ref, limit int) (rpc.MessagesResult, error) {
	f.audit01Record("Pinned")
	return rpc.MessagesResult{Messages: f.messages[:min(limit, len(f.messages))]}, nil
}

type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
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

type testEnv struct {
	t     *testing.T
	home  string
	env   map[string]string
	tg    *fakeTelegram
	stdin io.Reader

	mu         sync.Mutex
	lines      []string              // answers for ReadLine
	secrets    []string              // answers for ReadSecret
	cfg        daemon.TelegramConfig // what NewTelegram received last
	fetchErr   error                 // returned by FetchPresets when set
	fetchCalls int
	// launch, when set, replaces the background launcher of a start.
	launch func(args []string, logPath string) (int, func() error, error)
}

// presetFixture mimics the opentele file with made-up values.
const presetFixture = `
class API(BaseObject):
    class TelegramDesktop(APIData):
        api_id = 11111
        api_hash = "0123456789abcdef0123456789abcdef"

    class TelegramIOS(APIData):
        api_id = 22222
        api_hash = "11111111111111111111111111111111"
`

const (
	fixtureDesktopHash = "0123456789abcdef0123456789abcdef"
	fixtureIOSHash     = "11111111111111111111111111111111"
)

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	// Unix socket paths are length-limited and t.TempDir is too long on macOS.
	home, err := os.MkdirTemp("/tmp", "tgc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	return &testEnv{
		t:    t,
		home: home,
		env:  map[string]string{"TG_CLI_HOME": home, "TG_API_ID": "12345", "TG_API_HASH": "APIHASHVALUE"},
		tg: &fakeTelegram{
			ready:    make(chan struct{}),
			wantCode: "CODE777",
			wantPass: "PASS888",
			chats: []rpc.Chat{
				{ID: 1, Type: "user", Title: "Ann"},
				{ID: -2, Type: "group", Title: "Family"},
				{ID: -1000000000003, Type: "channel", Title: "News"},
			},
		},
	}
}

func (e *testEnv) deps(stdout, stderr *safeBuffer) Deps {
	pop := func(queue *[]string) (string, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if len(*queue) == 0 {
			return "", errors.New("no scripted answer left")
		}
		v := (*queue)[0]
		*queue = (*queue)[1:]
		return v, nil
	}
	return Deps{
		ConfigPath: filepath.Join(e.home, ".config", "tg-cli", "config.toml"),
		Stdout:     stdout,
		Stderr:     stderr,
		Stdin:      e.stdin,
		Getenv:     func(k string) string { return e.env[k] },
		ReadLine:   func(string) (string, error) { return pop(&e.lines) },
		ReadSecret: func(string) (string, error) { return pop(&e.secrets) },
		NewTelegram: func(cfg daemon.TelegramConfig) (daemon.Telegram, error) {
			e.mu.Lock()
			e.cfg = cfg
			e.mu.Unlock()
			return e.tg, nil
		},
		LaunchDaemon: e.launch,
		FetchPresets: func(context.Context) ([]byte, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.fetchCalls++
			if e.fetchErr != nil {
				return nil, e.fetchErr
			}
			return []byte(presetFixture), nil
		},
		CallTimeout: 5 * time.Second,
	}
}

type result struct {
	code           int
	stdout, stderr string
}

// dbRoot is the database root of the environment: the db directory next to the
// configuration file, as the CLI builds it from Deps.ConfigPath.
func (e *testEnv) dbRoot() string {
	return filepath.Join(filepath.Dir(filepath.Join(e.home, ".config", "tg-cli", "config.toml")), "db")
}

func (e *testEnv) run(args ...string) result {
	e.t.Helper()
	var stdout, stderr safeBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code := Execute(ctx, args, e.deps(&stdout, &stderr))
	return result{code, stdout.String(), stderr.String()}
}

// daemonRun is a `daemon start` running in the background.
type daemonRun struct {
	finished chan struct{} // closed when the command returned
	res      result        // valid after finished is closed
}

// startDaemon runs `daemon start` in the background and waits for the socket.
// The returned run keeps its stdout and stderr in res once it has finished;
// stderr of a running daemon is available through the stderr field.
func (e *testEnv) startDaemon(extraArgs ...string) *daemonRun {
	e.t.Helper()
	d := &daemonRun{finished: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr safeBuffer
	go func() {
		defer close(d.finished)
		// --foreground: the tests drive the daemon in this process, with a fake
		// Telegram client. The background start is tested on its own.
		code := Execute(ctx, append([]string{"daemon", "start", "--foreground"}, extraArgs...), e.deps(&stdout, &stderr))
		d.res = result{code, stdout.String(), stderr.String()}
	}()
	e.t.Cleanup(func() {
		cancel()
		select {
		case <-d.finished:
		case <-time.After(5 * time.Second):
			e.t.Error("daemon did not stop")
		}
	})

	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		e.t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := rpc.Dial(context.Background(), "unix", paths.Socket); err == nil {
			_ = c.Close()
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("daemon socket did not appear")
	return nil
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parse(t *testing.T, r result) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %q (%v)", r.stdout, err)
	}
	if strings.Count(strings.TrimSpace(r.stdout), "\n") != 0 {
		t.Errorf("stdout must be a single line, got %q", r.stdout)
	}
	return env
}

func wantFailure(t *testing.T, r result, exit int) {
	t.Helper()
	env := parse(t, r)
	if r.code != exit || env.OK || env.Error == nil || env.Error.Code != exitcode.Name(exit) {
		t.Errorf("want exit %d (%s), got exit %d with %q", exit, exitcode.Name(exit), r.code, r.stdout)
	}
}

func TestCommandsWithoutDaemon(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"chat", "list"}, {"daemon", "status"}, {"daemon", "stop"}, {"auth", "login", "--phone", "+1"}} {
		wantFailure(t, e.run(args...), exitcode.DaemonNotRunning)
	}
}

func TestUsageErrors(t *testing.T) {
	e := newEnv(t)
	wantFailure(t, e.run("nope"), exitcode.Usage)
	wantFailure(t, e.run("chat", "list", "--bogus"), exitcode.Usage)
	wantFailure(t, e.run("chat", "list", "--profile", "Bad Name"), exitcode.Usage)

	delete(e.env, "TG_API_ID")
	r := e.run("daemon", "start", "--foreground")
	wantFailure(t, r, exitcode.Usage)
	if strings.Contains(r.stdout+r.stderr, "APIHASHVALUE") {
		t.Error("api hash must never be printed")
	}
}

func TestLoginAndChatListEndToEnd(t *testing.T) {
	e := newEnv(t)
	e.tg.need2FA = true
	d := e.startDaemon()

	// Before the login the account is not available.
	wantFailure(t, e.run("chat", "list"), exitcode.NotAuthorized)

	e.lines = []string{"+15550001111", "CODE777"} // phone from the prompt, then the code
	e.secrets = []string{"PASS888"}
	login := e.run("auth", "login")
	env := parse(t, login)
	if login.code != exitcode.OK || !env.OK {
		t.Fatalf("login failed: exit %d, %q, stderr %q", login.code, login.stdout, login.stderr)
	}
	var res rpc.AuthResult
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("decode %q: %v", env.Data, err)
	}
	if res.State != rpc.AuthAuthorized || res.User == nil || res.User.ID != 42 {
		t.Errorf("unexpected login result %+v", res)
	}
	for _, secret := range []string{"CODE777", "PASS888", "+15550001111", "APIHASHVALUE"} {
		if strings.Contains(login.stdout, secret) || strings.Contains(login.stderr, secret) {
			t.Errorf("login output leaks %q", secret)
		}
	}

	list := e.run("chat", "list", "--limit", "2")
	env = parse(t, list)
	var chats rpc.ChatListResult
	if err := json.Unmarshal(env.Data, &chats); err != nil {
		t.Fatalf("decode %q: %v", env.Data, err)
	}
	if list.code != exitcode.OK || !env.OK || len(chats.Chats) != 2 || chats.Chats[1].Title != "Family" {
		t.Errorf("chat list: exit %d, %q", list.code, list.stdout)
	}

	status := e.run("daemon", "status")
	env = parse(t, status)
	var st rpc.StatusResult
	if err := json.Unmarshal(env.Data, &st); err != nil {
		t.Fatalf("decode %q: %v", env.Data, err)
	}
	if status.code != exitcode.OK || !st.Authorized || !st.Connected || st.Profile != "default" {
		t.Errorf("status: exit %d, %q", status.code, status.stdout)
	}

	stop := e.run("daemon", "stop")
	if stop.code != exitcode.OK || !parse(t, stop).OK {
		t.Errorf("stop: exit %d, %q", stop.code, stop.stdout)
	}
	select {
	case <-d.finished:
		if r := d.res; r.code != exitcode.OK || !strings.Contains(r.stdout, `"stopped":true`) {
			t.Errorf("daemon start result after stop: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

func TestLoginWithPhoneFlag(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	e.lines = []string{"CODE777"} // only the code: the phone comes from --phone
	r := e.run("auth", "login", "--phone", "+15550002222")
	if r.code != exitcode.OK || !parse(t, r).OK {
		t.Errorf("login: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestWrongCodeIsATelegramError(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	e.lines = []string{"BADCODE"}
	r := e.run("auth", "login", "--phone", "+15550003333")
	wantFailure(t, r, exitcode.Telegram)
	if !strings.Contains(r.stdout, "PHONE_CODE_INVALID") {
		t.Errorf("the Telegram reason must reach the agent: %q", r.stdout)
	}
	if strings.Contains(r.stdout+r.stderr, "BADCODE") {
		t.Error("the entered code must not be echoed")
	}
}

func TestAbandonedLoginIsCanceled(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	e.lines = nil // no answer: reading the code fails
	wantFailure(t, e.run("auth", "login", "--phone", "+15550004444"), exitcode.Usage)

	// The daemon must not stay stuck in a half-done login.
	e.lines = []string{"CODE777"}
	if r := e.run("auth", "login", "--phone", "+15550004444"); r.code != exitcode.OK {
		t.Errorf("a new login after an abandoned one failed: %q", r.stdout)
	}
}

func TestSecondDaemonOnTheSameProfile(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	r := e.run("daemon", "start", "--foreground")
	wantFailure(t, r, exitcode.Failure)
	if !strings.Contains(r.stdout, "already running") {
		t.Errorf("unexpected message %q", r.stdout)
	}
}

func TestProfilesAreIsolated(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	// The daemon runs for "default" only; another profile has no daemon.
	wantFailure(t, e.run("chat", "list", "--profile", "work"), exitcode.DaemonNotRunning)
}

// The CLI may depend on storage through daemon, but never import it directly.
// Telegram packages remain banned across the full dependency graph.
func TestCLIImportBoundary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not found")
	}
	cmd := exec.Command("go", "list", "-deps", "tg-cli/internal/cli")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, banned := range []string{"tg-cli/internal/tgclient", "github.com/gotd/td"} {
			if strings.HasPrefix(dep, banned) {
				t.Errorf("internal/cli depends on %s", dep)
			}
		}
	}
	direct := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, "tg-cli/internal/cli")
	imports, err := direct.Output()
	if err != nil {
		t.Fatalf("go list direct imports: %v", err)
	}
	for _, path := range strings.Fields(string(imports)) {
		if path == "tg-cli/internal/store" {
			t.Error("internal/cli imports internal/store directly")
		}
	}
}

func (e *testEnv) stopDaemon(d *daemonRun) {
	e.t.Helper()
	if r := e.run("daemon", "stop"); r.code != exitcode.OK {
		e.t.Fatalf("daemon stop: %q", r.stdout)
	}
	select {
	case <-d.finished:
	case <-time.After(5 * time.Second):
		e.t.Fatal("daemon did not stop")
	}
}

func (e *testEnv) savedProfile(name string) config.Profile {
	e.t.Helper()
	cfg, err := config.Load(filepath.Join(e.home, ".config", "tg-cli", "config.toml"))
	if err != nil {
		e.t.Fatal(err)
	}
	return cfg.Profiles[name]
}

func (e *testEnv) usePreset() {
	delete(e.env, "TG_API_ID")
	delete(e.env, "TG_API_HASH")
}

func TestStartWithClientPresetAndRemembersIt(t *testing.T) {
	e := newEnv(t)
	e.usePreset()

	d := e.startDaemon("--client", "desktop")
	e.mu.Lock()
	cfg, calls := e.cfg, e.fetchCalls
	e.mu.Unlock()
	if cfg.AppID != 11111 || cfg.AppHash != fixtureDesktopHash || calls != 1 {
		t.Fatalf("the preset credentials did not reach the Telegram client: %+v, %d fetches", cfg, calls)
	}
	e.stopDaemon(d)
	if out := d.res.stdout + d.res.stderr; strings.Contains(out, fixtureDesktopHash) {
		t.Errorf("the application hash must never be printed: %q", out)
	} else if !strings.Contains(d.res.stderr, "official client preset") {
		t.Errorf("the user must be told about the preset: %q", d.res.stderr)
	}

	// The choice is remembered: the next start needs no flag.
	if got := e.savedProfile("default").Client; got != "desktop" {
		t.Fatalf("saved preset %q", got)
	}
	info, err := os.Stat(filepath.Join(e.home, ".config", "tg-cli", "config.toml"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions: %v, %v", info, err)
	}
	d = e.startDaemon()
	e.mu.Lock()
	cfg = e.cfg
	e.mu.Unlock()
	if cfg.AppID != 11111 {
		t.Errorf("the saved preset was not used: %+v", cfg)
	}
	e.stopDaemon(d)
}

func TestClientFlagOverridesTheSavedPreset(t *testing.T) {
	e := newEnv(t)
	e.usePreset()
	e.stopDaemon(e.startDaemon("--client", "desktop"))
	d := e.startDaemon("--client", "ios")
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()
	if cfg.AppID != 22222 || cfg.AppHash != fixtureIOSHash {
		t.Errorf("--client must win over the saved preset: %+v", cfg)
	}
	e.stopDaemon(d)
	if got := e.savedProfile("default").Client; got != "ios" {
		t.Errorf("saved preset %q", got)
	}
}

func TestClientAndOwnApplicationConflict(t *testing.T) {
	e := newEnv(t) // TG_API_ID and TG_API_HASH are set
	r := e.run("daemon", "start", "--foreground", "--client", "desktop")
	wantFailure(t, r, exitcode.Usage)
	if !strings.Contains(r.stdout, "not both") {
		t.Errorf("unexpected message %q", r.stdout)
	}
	if e.fetchCalls != 0 {
		t.Error("nothing must be downloaded on a conflict")
	}
}

func TestUnknownPresetIsRejectedAndNotSaved(t *testing.T) {
	e := newEnv(t)
	e.usePreset()
	r := e.run("daemon", "start", "--foreground", "--client", "nope")
	wantFailure(t, r, exitcode.Usage)
	if !strings.Contains(r.stdout, "desktop, ios") {
		t.Errorf("the available presets must be listed: %q", r.stdout)
	}
	if strings.Contains(r.stdout+r.stderr, fixtureDesktopHash) {
		t.Error("an error must not contain hashes")
	}
	if got := e.savedProfile("default").Client; got != "" {
		t.Errorf("unknown preset saved: %q", got)
	}
}

func TestInvalidPresetNameIsRejectedBeforeDownload(t *testing.T) {
	e := newEnv(t)
	e.usePreset()
	wantFailure(t, e.run("daemon", "start", "--foreground", "--client", "../etc"), exitcode.Usage)
	if e.fetchCalls != 0 {
		t.Error("an invalid name must not trigger a download")
	}
}

func TestPresetDownloadFailure(t *testing.T) {
	e := newEnv(t)
	e.usePreset()
	e.fetchErr = errors.New("network is down")
	r := e.run("daemon", "start", "--foreground", "--client", "desktop")
	wantFailure(t, r, exitcode.Failure)
	if !strings.Contains(r.stdout, "network is down") {
		t.Errorf("the reason must be reported: %q", r.stdout)
	}
	if got := e.savedProfile("default").Client; got != "" {
		t.Errorf("preset saved after failure: %q", got)
	}
}

func TestNoCredentialsExplainsBothWays(t *testing.T) {
	e := newEnv(t)
	e.usePreset()
	r := e.run("daemon", "start", "--foreground")
	wantFailure(t, r, exitcode.Usage)
	for _, want := range []string{"TG_API_ID", "--client"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the message must mention %s: %q", want, r.stdout)
		}
	}
}

func TestLoginTellsWhereTheCodeWasSent(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	e.lines = []string{"CODE777"}
	r := e.run("auth", "login", "--phone", "+15550005555")
	if r.code != exitcode.OK {
		t.Fatalf("login: %q", r.stdout)
	}
	// The fake Telegram reports delivery by SMS.
	if !strings.Contains(r.stderr, "sent by SMS") {
		t.Errorf("the user must be told where the code went, stderr: %q", r.stderr)
	}
}

func TestInterruptEndsLoginWaitingForInput(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	block := make(chan struct{})
	defer close(block)

	var stdout, stderr safeBuffer
	deps := e.deps(&stdout, &stderr)
	entered := make(chan struct{}, 1)
	deps.ReadLine = func(string) (string, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block
		return "", errors.New("unreachable")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Execute(ctx, []string{"auth", "login", "--phone", "+15550006666"}, deps) }()
	select {
	case <-entered: // the login is now waiting for the code
	case <-time.After(10 * time.Second):
		t.Fatal("the login never asked for the code")
	}
	cancel()

	select {
	case code := <-done:
		if code != exitcode.Failure || !strings.Contains(stdout.String(), "interrupted") {
			t.Errorf("exit %d, %q", code, stdout.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an interrupt must end a login that waits for input")
	}
	// The abandoned login must not block the next one.
	e.lines = []string{"CODE777"}
	if r := e.run("auth", "login", "--phone", "+15550006666"); r.code != exitcode.OK {
		t.Errorf("login after an interrupt: %q", r.stdout)
	}
}

func TestAuthCancel(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	// Start a login over RPC and walk away without canceling, like a killed CLI.
	c, err := rpc.Dial(context.Background(), "unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+15550007777"}, nil); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	// The daemon still waits for the code, so another login is refused.
	e.lines = []string{"CODE777"}
	refused := e.run("auth", "login", "--phone", "+15550007777")
	wantFailure(t, refused, exitcode.Failure)
	if !strings.Contains(refused.stdout, "a login is already in progress") {
		t.Fatalf("a login must be refused because another one waits: %q", refused.stdout)
	}
	r := e.run("auth", "cancel")
	if r.code != exitcode.OK || !strings.Contains(r.stdout, `"canceled":true`) {
		t.Errorf("auth cancel: exit %d, %q", r.code, r.stdout)
	}
	if again := e.run("auth", "cancel"); !strings.Contains(again.stdout, `"canceled":false`) {
		t.Errorf("a second cancel must report nothing to cancel: %q", again.stdout)
	}
	e.lines = []string{"CODE777"}
	if r := e.run("auth", "login", "--phone", "+15550007777"); r.code != exitcode.OK {
		t.Errorf("login after cancel: %q", r.stdout)
	}
}

func TestResendIsOfferedAndWorks(t *testing.T) {
	e := newEnv(t)
	e.startDaemon()
	e.lines = []string{"resend", "CODE777"} // ask for another delivery, then enter the code
	r := e.run("auth", "login", "--phone", "+15550008888")
	if r.code != exitcode.OK || !parse(t, r).OK {
		t.Fatalf("login: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"Requesting the login code", "sent by SMS", "type resend to get it by a phone call", "after 45 seconds", "Asking Telegram to send the code again", "delivered by a phone call", "Signing in", "Logged in as"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr must contain %q, got %q", want, r.stderr)
		}
	}
	if strings.Contains(r.stdout+r.stderr, "CODE777") {
		t.Error("the code must not be echoed")
	}
}

func TestAuthStatus(t *testing.T) {
	e := newEnv(t)
	wantFailure(t, e.run("auth", "status"), exitcode.DaemonNotRunning)

	e.startDaemon()
	read := func() authStatus {
		r := e.run("auth", "status")
		env := parse(t, r)
		if r.code != exitcode.OK || !env.OK {
			t.Fatalf("auth status: exit %d, %q", r.code, r.stdout)
		}
		var st authStatus
		if err := json.Unmarshal(env.Data, &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := read(); st.Authorized || !st.Connected || st.User != nil || st.Profile != "default" {
		t.Errorf("before the login: %+v", st)
	}
	e.lines = []string{"CODE777"}
	if r := e.run("auth", "login", "--phone", "+15550009999"); r.code != exitcode.OK {
		t.Fatalf("login: %q", r.stdout)
	}
	if st := read(); !st.Authorized || st.User == nil || st.User.ID != 42 {
		t.Errorf("after the login: %+v", st)
	}
}

func TestAuthResendFromAnotherTerminal(t *testing.T) {
	e := newEnv(t)
	wantFailure(t, e.run("auth", "resend"), exitcode.DaemonNotRunning)
	e.startDaemon()
	// No login is waiting yet.
	wantFailure(t, e.run("auth", "resend"), exitcode.Failure)

	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	c, err := rpc.Dial(context.Background(), "unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(context.Background(), rpc.MethodAuthStart, rpc.AuthStartParams{Phone: "+15550001010"}, nil); err != nil {
		t.Fatal(err)
	}
	r := e.run("auth", "resend")
	env := parse(t, r)
	var res rpc.AuthResult
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("decode %q: %v", env.Data, err)
	}
	if r.code != exitcode.OK || res.Delivery != rpc.DeliveryCall {
		t.Errorf("auth resend: exit %d, %q", r.code, r.stdout)
	}
	if !strings.Contains(r.stderr, "phone call") {
		t.Errorf("the user must be told the new way: %q", r.stderr)
	}
}

func TestLoginByQR(t *testing.T) {
	e := newEnv(t)
	e.tg.need2FA = true
	e.startDaemon()
	e.secrets = []string{"PASS888"}
	r := e.run("auth", "login", "--qr")
	env := parse(t, r)
	var res rpc.AuthResult
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("decode %q: %v", env.Data, err)
	}
	if r.code != exitcode.OK || !env.OK || res.State != rpc.AuthAuthorized || res.User == nil {
		t.Fatalf("login --qr: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"Link Desktop Device", "tg://login?token=FIRST", "refreshed", "tg://login?token=SECOND", "two-factor", "Logged in as"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr must contain %q, got:\n%s", want, r.stderr)
		}
	}
	if !strings.Contains(r.stderr, "\x1b[") {
		t.Error("the QR code must use colors by default")
	}
	// A QR login needs neither a phone nor a code, and nothing secret is printed.
	if strings.Contains(r.stdout+r.stderr, "PASS888") {
		t.Error("the password must not be echoed")
	}
}

func TestLoginByQRWithoutColor(t *testing.T) {
	e := newEnv(t)
	e.env["NO_COLOR"] = "1"
	e.startDaemon()
	r := e.run("auth", "login", "--qr")
	if r.code != exitcode.OK {
		t.Fatalf("login --qr: %q", r.stdout)
	}
	if strings.Contains(r.stderr, "\x1b[") {
		t.Error("NO_COLOR must switch the colors off")
	}
}

func TestDeviceNameIsPassedAndRemembered(t *testing.T) {
	e := newEnv(t)
	cfg := func() daemon.TelegramConfig {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.cfg
	}

	// Nothing configured: the client gets an empty name and uses its default.
	d := e.startDaemon()
	if cfg().DeviceModel != "" {
		t.Errorf("without --device the name must stay empty, got %q", cfg().DeviceModel)
	}
	e.stopDaemon(d)

	d = e.startDaemon("--device", "  MacBook Pro M1 BRN ")
	if cfg().DeviceModel != "MacBook Pro M1 BRN" {
		t.Errorf("the device name from --device: %q", cfg().DeviceModel)
	}
	e.stopDaemon(d)
	if got := e.savedProfile("default").Device; got != "MacBook Pro M1 BRN" {
		t.Fatalf("saved device %q", got)
	}

	// The next start needs no flag.
	d = e.startDaemon()
	if cfg().DeviceModel != "MacBook Pro M1 BRN" {
		t.Errorf("the saved device name was not used: %q", cfg().DeviceModel)
	}
	e.stopDaemon(d)
}

func TestInvalidDeviceNames(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"   ", strings.Repeat("x", 65), "a\nb", "a\x1b[0m"} {
		wantFailure(t, e.run("daemon", "start", "--foreground", "--device", name), exitcode.Usage)
	}
	if got := e.savedProfile("default").Device; got != "" {
		t.Errorf("invalid device saved: %q", got)
	}
}

// loggedInEnv starts a daemon with a logged-in account and a few messages.
func loggedInEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	e.tg.user = &rpc.User{ID: 1, Username: "me"}
	e.tg.messages = []rpc.Message{
		{ID: 30, ChatID: 77, Date: "2026-01-03T10:00:00Z", Text: "third", Sender: &rpc.Sender{ID: 5, Name: "Ann"}},
		{ID: 20, ChatID: 77, Date: "2026-01-02T10:00:00Z", Text: "second", MediaType: "photo"},
		{ID: 10, ChatID: 77, Date: "2026-01-01T10:00:00Z", Text: "first"},
	}
	e.startDaemon()
	return e
}

func TestChatCommands(t *testing.T) {
	e := loggedInEnv(t)

	var chat rpc.Chat
	r := e.run("chat", "resolve", "@TeamChat")
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &chat) != nil || chat.ID != 77 || chat.Type != "supergroup" {
		t.Errorf("chat resolve: exit %d, %q", r.code, r.stdout)
	}
	if got, ok := e.tg.audit01LastRef("Resolve"); !ok || got != (chatref.Ref{Kind: chatref.Username, Username: "teamchat"}) {
		t.Errorf("chat resolve sent %+v to Telegram (called: %v)", got, ok)
	}
	var info rpc.ChatInfo
	r = e.run("chat", "info", "--", "-1001234567890")
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &info) != nil || info.About != "About the team" || info.Members != 12 {
		t.Errorf("chat info: exit %d, %q", r.code, r.stdout)
	}
	if got, ok := e.tg.audit01LastRef("ChatInfo"); !ok || got != (chatref.Ref{Kind: chatref.ID, ID: -1001234567890}) {
		t.Errorf("chat info sent %+v to Telegram (called: %v)", got, ok)
	}
	var invite rpc.InviteInfo
	r = e.run("chat", "invite-info", "https://t.me/+AbCdEfGh1234")
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &invite) != nil || invite.Title != "Private team" {
		t.Errorf("chat invite-info: exit %d, %q", r.code, r.stdout)
	}

	// Mistakes in the reference are usage errors, a missing chat is not_found.
	wantFailure(t, e.run("chat", "resolve", "not a chat"), exitcode.Usage)
	wantFailure(t, e.run("chat", "resolve", "t.me/+AbCdEfGh1234"), exitcode.Usage)
	wantFailure(t, e.run("chat", "invite-info", "@teamchat"), exitcode.Usage)
	e.tg.missing = true
	r = e.run("chat", "resolve", "@nosuchchat")
	wantFailure(t, r, exitcode.NotFound)
}

func TestChatListTypeAndJSONL(t *testing.T) {
	e := loggedInEnv(t)
	e.tg.chats = []rpc.Chat{{ID: 1, Type: "user", Title: "A"}, {ID: 2, Type: "bot", Title: "B"}, {ID: 3, Type: "bot", Title: "C"}}

	r := e.run("chat", "list", "--type", "bot")
	var res rpc.ChatListResult
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &res) != nil || len(res.Chats) != 2 {
		t.Errorf("chat list --type bot: exit %d, %q", r.code, r.stdout)
	}
	wantFailure(t, e.run("chat", "list", "--type", "robot"), exitcode.Usage)

	r = e.run("chat", "list", "--format", "jsonl")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != exitcode.OK || len(lines) != 3 {
		t.Fatalf("chat list --format jsonl: exit %d, %q", r.code, r.stdout)
	}
	for _, line := range lines {
		var c rpc.Chat
		if err := json.Unmarshal([]byte(line), &c); err != nil || c.ID == 0 || strings.Contains(line, `"ok"`) {
			t.Errorf("a JSONL line must be one chat without an envelope: %q", line)
		}
	}
	wantFailure(t, e.run("chat", "list", "--format", "xml"), exitcode.Usage)
}

func TestMessageHistoryPagesAndFormats(t *testing.T) {
	e := loggedInEnv(t)

	r := e.run("message", "history", "@teamchat", "--limit", "2")
	var page rpc.MessagesResult
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &page) != nil {
		t.Fatalf("history: exit %d, %q", r.code, r.stdout)
	}
	if len(page.Messages) != 2 || page.Messages[0].ID != 30 || page.NextCursor != "CURSOR-AFTER" {
		t.Errorf("history page: %+v", page)
	}

	// The last page has no cursor.
	r = e.run("message", "history", "@teamchat")
	var full rpc.MessagesResult // a fresh value: omitted fields would keep the old ones
	if json.Unmarshal(parse(t, r).Data, &full) != nil || len(full.Messages) != 3 || full.NextCursor != "" {
		t.Errorf("full history: %q", r.stdout)
	}

	// JSONL: one message per line, then the cursor line.
	r = e.run("message", "history", "@teamchat", "--limit", "2", "--format", "jsonl")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != exitcode.OK || len(lines) != 3 {
		t.Fatalf("history jsonl: exit %d, %q", r.code, r.stdout)
	}
	var first rpc.Message
	if json.Unmarshal([]byte(lines[0]), &first) != nil || first.ID != 30 || first.Text != "third" {
		t.Errorf("first JSONL line: %q", lines[0])
	}
	if lines[2] != `{"next_cursor":"CURSOR-AFTER"}` {
		t.Errorf("the last JSONL line must carry the cursor: %q", lines[2])
	}
	r = e.run("message", "history", "@teamchat", "--format", "jsonl")
	if strings.Contains(r.stdout, "next_cursor") {
		t.Errorf("no cursor line on the last page: %q", r.stdout)
	}

	// Bad arguments are usage errors.
	for _, args := range [][]string{
		{"message", "history", "@teamchat", "--limit", "101"},
		{"message", "history", "@teamchat", "--since", "yesterday"},
		{"message", "history", "@teamchat", "--format", "xml"},
		{"message", "history"},
	} {
		wantFailure(t, e.run(args...), exitcode.Usage)
	}
}

func TestMessageGetAndPinned(t *testing.T) {
	e := loggedInEnv(t)

	var msg rpc.Message
	r := e.run("message", "get", "@teamchat", "20")
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &msg) != nil || msg.Text != "second" || msg.MediaType != "photo" {
		t.Errorf("message get: exit %d, %q", r.code, r.stdout)
	}
	wantFailure(t, e.run("message", "get", "@teamchat", "999"), exitcode.NotFound)
	wantFailure(t, e.run("message", "get", "@teamchat", "abc"), exitcode.Usage)
	wantFailure(t, e.run("message", "get", "@teamchat", "0"), exitcode.Usage)

	r = e.run("message", "pinned", "@teamchat", "--limit", "2", "--format", "jsonl")
	if r.code != exitcode.OK || len(strings.Split(strings.TrimSpace(r.stdout), "\n")) != 2 {
		t.Errorf("message pinned jsonl: exit %d, %q", r.code, r.stdout)
	}
	r = e.run("message", "pinned", "@teamchat")
	var res rpc.MessagesResult
	if r.code != exitcode.OK || json.Unmarshal(parse(t, r).Data, &res) != nil || len(res.Messages) != 3 {
		t.Errorf("message pinned: exit %d, %q", r.code, r.stdout)
	}
}

func TestReadCommandsNeedDaemonAndLogin(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		{"chat", "resolve", "@teamchat"}, {"chat", "info", "@teamchat"}, {"chat", "invite-info", "t.me/+AbCdEfGh1234"},
		{"message", "history", "@teamchat"}, {"message", "get", "@teamchat", "1"}, {"message", "pinned", "@teamchat"},
	} {
		wantFailure(t, e.run(args...), exitcode.DaemonNotRunning)
	}
	e.startDaemon() // nobody is logged in
	for _, args := range [][]string{
		{"chat", "resolve", "@teamchat"}, {"message", "history", "@teamchat"}, {"message", "get", "@teamchat", "1"},
	} {
		wantFailure(t, e.run(args...), exitcode.NotAuthorized)
	}
}

func TestNegativeIDNeedsDoubleDash(t *testing.T) {
	e := loggedInEnv(t)
	// Without -- cobra takes the id for a flag: the error must say how to fix it.
	r := e.run("chat", "info", "-1001234567890")
	wantFailure(t, r, exitcode.Usage)
	if !strings.Contains(r.stdout, "chat info -- -1001234567890") {
		t.Errorf("the hint about -- is missing: %q", r.stdout)
	}
	// An unrelated unknown flag gets no hint about ids.
	for _, tc := range []struct{ flag, want string }{{"--bogus", "unknown flag: --bogus"}, {"-x", "unknown shorthand flag: 'x'"}} {
		r := e.run("chat", "info", tc.flag)
		wantFailure(t, r, exitcode.Usage)
		if !strings.Contains(r.stdout, tc.want) || strings.Contains(r.stdout, "negative chat ids") {
			t.Errorf("chat info %s: want %q and no hint about ids: %q", tc.flag, tc.want, r.stdout)
		}
	}
	// With -- it works, also for a basic group id.
	for _, id := range []string{"-1001234567890", "-12345"} {
		if r := e.run("message", "history", "--", id); r.code != exitcode.OK {
			t.Errorf("message history -- %s: exit %d, %q", id, r.code, r.stdout)
		}
	}
}

func (f *fakeTelegram) MessageReaders(_ context.Context, ref chatref.Ref, id int) (rpc.MessageReadersResult, error) {
	f.audit01Record("MessageReaders")
	return rpc.MessageReadersResult{ChatID: 77, ID: id, Readers: []rpc.MessageReader{{UserID: 5, Name: "Alice", Username: "alice", Date: "2026-01-01T00:00:00Z"}}}, nil
}

func (f *fakeTelegram) AccountProfile(context.Context) (rpc.AccountProfile, error) {
	f.audit01Record("AccountProfile")
	return rpc.AccountProfile{ID: 42, FirstName: "Alice"}, nil
}

func (f *fakeTelegram) AccountEmojiStatuses(context.Context, bool) (rpc.EmojiStatusesResult, error) {
	f.audit01Record("AccountEmojiStatuses")
	return rpc.EmojiStatusesResult{Statuses: []rpc.EmojiStatusOption{{DocumentID: 123}}}, nil
}

func (f *fakeTelegram) HandleJoinRequest(context.Context, chatref.Ref, chatref.Ref, bool) error {
	f.audit01Record("HandleJoinRequest")
	return nil
}

func (f *fakeTelegram) SetChannelSetting(context.Context, chatref.Ref, string, string, *string, *string) (bool, error) {
	f.audit01Record("SetChannelSetting")
	return true, nil
}

func (f *fakeTelegram) SetAccountField(context.Context, string, string, string, *chatref.Ref) (bool, error) {
	f.audit01Record("SetAccountField")
	return true, nil
}

func (f *fakeTelegram) ChatRequests(context.Context, chatref.Ref, rpc.JoinRequestsParams) (rpc.JoinRequestsResult, error) {
	f.audit01Record("ChatRequests")
	return rpc.JoinRequestsResult{Requests: []rpc.JoinRequest{{UserID: 5, Name: "Alice", Username: "alice", Date: "2026-01-01T00:00:00Z", About: "hello"}}}, nil
}

func (f *fakeTelegram) AccountPrivacyGet(_ context.Context, key string) (rpc.PrivacyRules, error) {
	f.audit01Record("AccountPrivacyGet")
	return rpc.PrivacyRules{Key: key, Default: "nobody", AllowUsers: []int64{}, DenyUsers: []int64{}, AllowChats: []int64{}, DenyChats: []int64{}}, nil
}
func (f *fakeTelegram) AccountPrivacySet(_ context.Context, p rpc.PrivacySetParams, _, _, _, _ []chatref.Ref) (rpc.PrivacyRules, error) {
	f.audit01Record("AccountPrivacySet")
	return rpc.PrivacyRules{Key: p.Key, Default: p.Default, AllowUsers: []int64{}, DenyUsers: []int64{}, AllowChats: []int64{}, DenyChats: []int64{}}, nil
}

func (f *fakeTelegram) CacheJoinRequestUsers(context.Context, []rpc.JoinRequest) error {
	f.audit01Record("CacheJoinRequestUsers")
	return nil
}
