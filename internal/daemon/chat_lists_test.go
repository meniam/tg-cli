package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type listTelegram struct {
	*fakeTelegram
	membersCalls int
	topicsCalls  int
	commonCalls  int
	members      rpc.MembersResult
	topics       rpc.TopicsResult
	pages        map[string]rpc.CommonChatsResult
	commonPage   func(int, rpc.CommonChatsParams) rpc.CommonChatsResult

	// The parameters of the last call, what the daemon handed to Telegram.
	membersParams rpc.MembersParams
	topicsParams  rpc.TopicsParams
	commonParams  rpc.CommonChatsParams
	// err is returned by all three reads; during runs while a read is in flight, so
	// a test can change access.json between the request and the answer.
	err    error
	during func()
}

func (f *listTelegram) Members(_ context.Context, _ chatref.Ref, p rpc.MembersParams) (rpc.MembersResult, error) {
	f.membersCalls++
	f.membersParams = p
	if f.during != nil {
		f.during()
	}
	return f.members, f.err
}

func (f *listTelegram) Topics(_ context.Context, _ chatref.Ref, p rpc.TopicsParams) (rpc.TopicsResult, error) {
	f.topicsCalls++
	f.topicsParams = p
	if f.during != nil {
		f.during()
	}
	return f.topics, f.err
}

func (f *listTelegram) CommonChats(_ context.Context, _ chatref.Ref, p rpc.CommonChatsParams) (rpc.CommonChatsResult, error) {
	f.commonCalls++
	f.commonParams = p
	if f.during != nil {
		f.during()
	}
	if f.err != nil {
		return rpc.CommonChatsResult{}, f.err
	}
	if f.commonPage != nil {
		return f.commonPage(f.commonCalls, p), nil
	}
	return f.pages[p.Cursor], nil
}

func listTestDaemon(t *testing.T) (*daemon, *listTelegram, *store.DB, string) {
	t.Helper()
	d, tg, db, path := accessTestDaemon(t)
	f := &listTelegram{fakeTelegram: tg}
	d.opts.Telegram = f
	return d, f, db, path
}

func TestChatMembersTopicsClosedBeforeRead(t *testing.T) {
	d, tg, db, path := listTestDaemon(t)
	tg.resolved = &rpc.Chat{ID: -200, Type: "group", Title: "Closed"}
	writeAccess(t, path, `{"allow":[-100]}`)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		chat string
		want error
		call func(string) error
	}{
		{"members id", "-200", rpc.ErrUnknownChatID(), func(chat string) error {
			_, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: chat}))
			return err
		}},
		{"members username", "@privatechat", rpc.ErrChatNotFound(), func(chat string) error {
			_, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: chat}))
			return err
		}},
		{"topics id", "-200", rpc.ErrUnknownChatID(), func(chat string) error {
			_, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: chat}))
			return err
		}},
		{"topics username", "@privatechat", rpc.ErrChatNotFound(), func(chat string) error {
			_, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: chat}))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { sameRPCError(t, tc.call(tc.chat), tc.want) })
	}
	if tg.membersCalls != 0 || tg.topicsCalls != 0 || tg.resolveCalls != 2 {
		t.Fatalf("read calls: members=%d topics=%d resolve=%d", tg.membersCalls, tg.topicsCalls, tg.resolveCalls)
	}
	if _, err := db.GetChat(ctx, -200); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed chat saved: %v", err)
	}
}

func TestChatMembersTopicsOpenAndPeopleSaved(t *testing.T) {
	d, tg, db, _ := listTestDaemon(t)
	tg.resolved = &rpc.Chat{ID: -100, Type: "group", Title: "Open"}
	tg.members = rpc.MembersResult{Members: []rpc.Member{{ID: 21, Name: "Alice", Username: "alice", Role: "member"}, {ID: 22, Name: "Bot", Username: "robot", IsBot: true, Role: "member"}}, NextOffset: 2}
	ctx := context.Background()
	got, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "-100", Limit: 2}))
	if err != nil || len(got.(rpc.MembersResult).Members) != 2 || got.(rpc.MembersResult).NextOffset != 2 || tg.membersCalls != 1 {
		t.Fatalf("members=%+v calls=%d err=%v", got, tg.membersCalls, err)
	}
	for _, tc := range []struct {
		id             int64
		name, username string
		bot            bool
	}{{21, "Alice", "alice", false}, {22, "Bot", "robot", true}} {
		person, err := db.GetPerson(ctx, tc.id)
		if err != nil || person.FirstName == nil || *person.FirstName != tc.name || person.Username == nil || *person.Username != tc.username || person.IsBot == nil || *person.IsBot != tc.bot {
			t.Fatalf("person %d: %+v %v", tc.id, person, err)
		}
	}
	topics, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "-100"}))
	if err != nil || topics.(rpc.TopicsResult).Topics == nil || len(topics.(rpc.TopicsResult).Topics) != 0 || tg.topicsCalls != 1 {
		t.Fatalf("topics=%+v calls=%d err=%v", topics, tg.topicsCalls, err)
	}
}

func TestChatMembersTopicsValidationAndStorageFailure(t *testing.T) {
	d, tg, db, _ := listTestDaemon(t)
	tg.resolved = &rpc.Chat{ID: -100, Type: "group"}
	ctx := context.Background()
	for _, p := range []rpc.MembersParams{{Chat: "-100", Limit: -1}, {Chat: "-100", Limit: 201}, {Chat: "-100", Offset: -1}, {Chat: "-100", Filter: "unknown"}} {
		_, err := d.chatMembers(ctx, params(t, p))
		if rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Fatal(err)
		}
	}
	for _, p := range []rpc.TopicsParams{{Chat: "-100", Limit: -1}, {Chat: "-100", Limit: 101}} {
		_, err := d.chatTopics(ctx, params(t, p))
		if rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Fatal(err)
		}
	}
	if tg.membersCalls != 0 || tg.topicsCalls != 0 || tg.resolveCalls != 0 {
		t.Fatalf("Telegram called on invalid input: %+v", tg)
	}
	tg.members = rpc.MembersResult{Members: []rpc.Member{{ID: 21, Name: "Alice"}}}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "-100"}))
	if err != nil || len(got.(rpc.MembersResult).Members) != 1 {
		t.Fatalf("storage failure broke read: %+v %v", got, err)
	}
}

func TestChatMembersTopicsNeedLoginAndRejectUser(t *testing.T) {
	d, tg, _, _ := listTestDaemon(t)
	tg.user = nil
	ctx := context.Background()
	for _, call := range []func() error{
		func() error { _, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "-100"})); return err },
		func() error { _, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "-100"})); return err },
		func() error { _, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "42"})); return err },
	} {
		if err := call(); rpcCode(t, err) != rpc.CodeNotAuthorized {
			t.Fatal(err)
		}
	}
	if tg.membersCalls != 0 || tg.topicsCalls != 0 || tg.commonCalls != 0 {
		t.Fatal("read called without login")
	}
	tg.user = &rpc.User{ID: 1}
	tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Alice"}
	if _, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "42"})); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "42"})); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	// A bot is a person as well: the same refusal, no read.
	tg.resolved = &rpc.Chat{ID: 43, Type: "bot", Title: "Robot"}
	if _, err := d.chatMembers(ctx, params(t, rpc.MembersParams{Chat: "43"})); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := d.chatTopics(ctx, params(t, rpc.TopicsParams{Chat: "43"})); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if tg.membersCalls != 0 || tg.topicsCalls != 0 {
		t.Fatal("read called for a user or a bot")
	}
}

func TestChatCommonFiltersAndSavesOnlyVisible(t *testing.T) {
	d, tg, db, path := listTestDaemon(t)
	tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Alice", Username: "aliceuser"}
	writeAccess(t, path, `{"allow":[42,-101]}`)
	tg.pages = map[string]rpc.CommonChatsResult{"": {Chats: []rpc.CommonChat{{ID: -100, Type: "group", Title: "Closed"}, {ID: -101, Type: "supergroup", Title: "Open", Username: "openchat"}}, NextCursor: "next"}}
	ctx := context.Background()
	got, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "@aliceuser", Limit: 2}))
	if err != nil {
		t.Fatal(err)
	}
	result := got.(rpc.CommonChatsResult)
	if len(result.Chats) != 1 || result.Chats[0].ID != -101 || result.NextCursor != "next" || tg.commonCalls != 1 {
		t.Fatalf("result %+v, calls %d", result, tg.commonCalls)
	}
	if _, err := db.GetChat(ctx, -100); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed chat saved: %v", err)
	}
	if _, err := db.GetChat(ctx, -101); err != nil {
		t.Fatalf("open chat missing: %v", err)
	}
	person, err := db.GetPerson(ctx, 42)
	if err != nil || person.FirstName == nil || *person.FirstName != "Alice" || person.Username == nil || *person.Username != "aliceuser" {
		t.Fatalf("person %+v %v", person, err)
	}
}

func TestChatCommonReadsNextPageAndStopsAtTen(t *testing.T) {
	ctx := context.Background()
	for _, capAtTen := range []bool{false, true} {
		t.Run(fmt.Sprint(capAtTen), func(t *testing.T) {
			d, tg, db, path := listTestDaemon(t)
			tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Alice"}
			writeAccess(t, path, `{"allow":[42,-101]}`)
			tg.commonPage = func(call int, p rpc.CommonChatsParams) rpc.CommonChatsResult {
				if call > 1 && p.Cursor != fmt.Sprintf("page-%d", call-1) {
					t.Errorf("cursor %q", p.Cursor)
				}
				if !capAtTen && call == 2 {
					return rpc.CommonChatsResult{Chats: []rpc.CommonChat{{ID: -101, Type: "group", Title: "Open"}}, NextCursor: "page-2"}
				}
				return rpc.CommonChatsResult{Chats: []rpc.CommonChat{{ID: -100, Type: "group", Title: "Closed"}}, NextCursor: fmt.Sprintf("page-%d", call)}
			}
			got, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "42"}))
			if err != nil {
				t.Fatal(err)
			}
			result := got.(rpc.CommonChatsResult)
			wantCalls := 2
			if capAtTen {
				wantCalls = 10
			}
			if tg.commonCalls != wantCalls {
				t.Fatalf("calls %d, want %d", tg.commonCalls, wantCalls)
			}
			if capAtTen {
				if len(result.Chats) != 0 || result.NextCursor != "page-10" {
					t.Fatalf("capped result %+v", result)
				}
				if _, err := db.GetChat(ctx, -101); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("unexpected open chat: %v", err)
				}
			} else if len(result.Chats) != 1 || result.Chats[0].ID != -101 || result.NextCursor != "page-2" {
				t.Fatalf("result %+v", result)
			}
			if _, err := db.GetChat(ctx, -100); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("closed chat saved: %v", err)
			}
		})
	}
}

func TestChatCommonValidationAndEmpty(t *testing.T) {
	d, tg, db, _ := listTestDaemon(t)
	ctx := context.Background()
	for _, p := range []rpc.CommonChatsParams{{User: "0"}, {User: "-12"}, {User: "42", Limit: -1}, {User: "42", Limit: 101}, {User: "+15550001234"}} {
		_, err := d.chatCommon(ctx, params(t, p))
		if rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Fatal(err)
		}
	}
	if tg.commonCalls != 0 || tg.resolveCalls != 0 {
		t.Fatal("Telegram called on invalid input")
	}
	tg.resolved = &rpc.Chat{ID: -100, Type: "group", Title: "Group"}
	if _, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "@groupchat"})); rpcCode(t, err) != rpc.CodeInvalidParams {
		t.Fatal(err)
	}
	if tg.commonCalls != 0 {
		t.Fatal("common read called for a group")
	}
	tg.resolved = &rpc.Chat{ID: 42, Type: "bot", Title: "Bot"}
	got, err := d.chatCommon(ctx, params(t, rpc.CommonChatsParams{User: "42"}))
	if err != nil || got.(rpc.CommonChatsResult).Chats == nil || len(got.(rpc.CommonChatsResult).Chats) != 0 {
		t.Fatalf("empty result %+v %v", got, err)
	}
	if _, err := db.GetPerson(ctx, 42); err != nil {
		t.Fatalf("bot not saved: %v", err)
	}
}

func TestChatCommonUserAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		user   string
		policy string
		want   error
	}{
		{name: "closed numeric", user: "42", policy: `{"allow":[-101]}`, want: rpc.ErrUnknownChatID()},
		{name: "closed username", user: "@aliceuser", policy: `{"allow":[-101]}`, want: rpc.ErrChatNotFound()},
		{name: "open with access list", user: "42", policy: `{"allow":[42,-101]}`},
		{name: "open without access list", user: "42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tg, db, path := listTestDaemon(t)
			tg.resolved = &rpc.Chat{ID: 42, Type: "user", Title: "Alice", Username: "aliceuser"}
			tg.pages = map[string]rpc.CommonChatsResult{"": {Chats: []rpc.CommonChat{{ID: -101, Type: "group", Title: "Shared"}}}}
			if tc.policy != "" {
				writeAccess(t, path, tc.policy)
			}
			result, err := d.chatCommon(context.Background(), params(t, rpc.CommonChatsParams{User: tc.user}))
			if tc.want != nil {
				sameRPCError(t, err, tc.want)
				if result != nil || tg.commonCalls != 0 {
					t.Fatalf("closed user returned data or read common chats: result=%+v calls=%d", result, tg.commonCalls)
				}
				if tc.user == "42" && tg.resolveCalls != 0 {
					t.Fatalf("closed numeric user was resolved %d times", tg.resolveCalls)
				}
				for _, id := range []int64{42, -101} {
					if id > 0 {
						if _, err := db.GetPerson(context.Background(), id); !errors.Is(err, store.ErrNotFound) {
							t.Fatalf("closed user saved in people: %v", err)
						}
					} else if _, err := db.GetChat(context.Background(), id); !errors.Is(err, store.ErrNotFound) {
						t.Fatalf("shared chat saved: %v", err)
					}
				}
				if _, err := db.GetChat(context.Background(), 42); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("closed private chat saved: %v", err)
				}
				return
			}
			if err != nil || tg.commonCalls != 1 || len(result.(rpc.CommonChatsResult).Chats) != 1 {
				t.Fatalf("open user: result=%+v calls=%d err=%v", result, tg.commonCalls, err)
			}
			if _, err := db.GetPerson(context.Background(), 42); err != nil {
				t.Fatalf("open user not saved: %v", err)
			}
			if _, err := db.GetChat(context.Background(), -101); err != nil {
				t.Fatalf("shared chat not saved: %v", err)
			}
		})
	}
}
