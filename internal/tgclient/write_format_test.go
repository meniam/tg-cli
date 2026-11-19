package tgclient

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func knownUser(id int64) (tg.InputUserClass, error) {
	if id != 7 {
		return nil, errors.New("unknown")
	}
	return &tg.InputUser{UserID: id, AccessHash: 123}, nil
}

func formatErrorCode(t *testing.T, err error, code int) {
	t.Helper()
	var got *rpc.Error
	if !errors.As(err, &got) || got.Code != code {
		t.Fatalf("error=%v, want RPC code %d", err, code)
	}
}

func TestPrepareWriteTextEntities(t *testing.T) {
	for _, tc := range []struct {
		name, source, parse, wantText string
		wantTypes                     []string
	}{
		{"html nested emoji", "<b>😀<i>x</i></b>", "html", "😀x", []string{"messageEntityBold", "messageEntityItalic"}},
		{"markdown nested emoji", "**😀 _x_**", "markdown", "😀 x", []string{"messageEntityBold", "messageEntityItalic"}},
		{"html quote code", `<blockquote expandable>quote</blockquote><pre><code class="language-go">fmt</code></pre>`, "html", "quotefmt", []string{"messageEntityBlockquote", "messageEntityPre"}},
		{"markdown quote code", "> quote\n\n```go\nfmt\n```", "markdown", "quote\n\nfmt", []string{"messageEntityBlockquote", "messageEntityPre"}},
		{"html link", `<a href="https://example.com">link</a>`, "html", "link", []string{"messageEntityTextUrl"}},
		{"markdown link", `[link](https://example.com)`, "markdown", "link", []string{"messageEntityTextUrl"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := prepareWriteText(tc.source, tc.parse, nil, knownUser)
			if err != nil {
				t.Fatal(err)
			}
			if p.text != tc.wantText {
				t.Fatalf("text=%q want %q", p.text, tc.wantText)
			}
			if len(p.entities) != len(tc.wantTypes) {
				t.Fatalf("entities=%v", p.entities)
			}
			for i, e := range p.entities {
				if e.TypeName() != tc.wantTypes[i] {
					t.Errorf("entity %d=%s", i, e.TypeName())
				}
			}
			if strings.Contains(tc.name, "emoji") {
				if p.entities[0].GetLength() < 2 {
					t.Fatalf("entity misses UTF-16 surrogate pair: %v", p.entities)
				}
				wantInnerOffset := 2
				if tc.parse == "markdown" {
					wantInnerOffset = 3
				}
				if p.entities[1].GetOffset() != wantInnerOffset {
					t.Fatalf("inner offset=%d want=%d", p.entities[1].GetOffset(), wantInnerOffset)
				}
			}
			if strings.Contains(tc.name, "quote code") {
				if !p.entities[0].(*tg.MessageEntityBlockquote).Collapsed && tc.parse == "html" {
					t.Fatal("quote is not expandable")
				}
				if p.entities[1].(*tg.MessageEntityPre).Language != "go" {
					t.Fatal("code language lost")
				}
			}
		})
	}
}

// audit10Secret is a marker long enough that it cannot appear in an error message by accident.
const audit10Secret = "SECRET-INPUT-42"

func TestPrepareWriteTextManualEntitiesAndErrors(t *testing.T) {
	valid := []rpc.Entity{{Type: "bold", Offset: 2, Length: 1}, {Type: "custom_emoji", Offset: 0, Length: 2, CustomEmojiID: 99}}
	p, err := prepareWriteText("😀x", "", valid, knownUser)
	if err != nil || p.text != "😀x" || p.entities[0].GetOffset() != 2 || p.entities[1].GetLength() != 2 {
		t.Fatalf("manual=%+v %v", p, err)
	}
	for _, tc := range []struct {
		name, text, parse string
		entities          []rpc.Entity
	}{
		{"unknown type", audit10Secret, "", []rpc.Entity{{Type: "nope", Length: 1}}},
		{"out of range", audit10Secret, "", []rpc.Entity{{Type: "bold", Offset: len(audit10Secret), Length: 1}}},
		{"surrogate boundary", "😀x", "", []rpc.Entity{{Type: "bold", Offset: 1, Length: 1}}},
		{"zero length", audit10Secret, "", []rpc.Entity{{Type: "bold", Length: 0}}},
		{"parse and entities", audit10Secret, "html", []rpc.Entity{}},
		{"empty", "", "", nil},
		{"long plain", strings.Repeat("x", 4097), "", nil},
		{"long parsed", "<b>" + strings.Repeat("x", 4097) + "</b>", "html", nil},
		{"long rich", strings.Repeat("x", 4097), "rich-html", nil},
		{"unknown parse", audit10Secret, "other", nil},
		{"unclosed html", "<b>x", "html", nil},
		{"invalid link", `<a href="https://">x</a>`, "html", nil},
		{"unknown html mention", `<a href="tg://user?id=8">x</a>`, "html", nil},
		{"unknown markdown mention", `[x](tg://user?id=8)`, "markdown", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := prepareWriteText(tc.text, tc.parse, tc.entities, knownUser)
			formatErrorCode(t, err, rpc.CodeInvalidParams)
			if tc.text != "" && strings.Contains(err.Error(), tc.text) {
				t.Fatalf("error leaked input: %v", err)
			}
		})
	}
	for _, source := range []string{strings.Repeat("x", 4096), "<b>" + strings.Repeat("x", 4096) + "</b>"} {
		parse := ""
		if strings.HasPrefix(source, "<") {
			parse = "html"
		}
		if _, err := prepareWriteText(source, parse, nil, knownUser); err != nil {
			t.Fatal(err)
		}
	}
	for _, parse := range []string{"html", "markdown"} {
		source := `<a href="tg://user?id=7">name</a>`
		if parse == "markdown" {
			source = `[name](tg://user?id=7)`
		}
		p, err := prepareWriteText(source, parse, nil, knownUser)
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := p.entities[0].(*tg.InputMessageEntityMentionName); !ok || e.UserID.(*tg.InputUser).AccessHash != 123 {
			t.Fatalf("mention=%v", p.entities)
		}
		unknown := source
		if parse == "html" {
			unknown = `<a href="tg://user?id=8">name</a>`
		} else {
			unknown = `[name](tg://user?id=8)`
		}
		_, err = prepareWriteText(unknown, parse, nil, knownUser)
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Message != "unknown user id for this session" {
			t.Fatalf("unknown mention=%v", err)
		}
	}
	p, err = prepareWriteText("name", "", []rpc.Entity{{Type: "mention_name", Length: 4, UserID: 7}}, knownUser)
	if err != nil || p.entities[0].(*tg.InputMessageEntityMentionName).UserID.(*tg.InputUser).AccessHash != 123 {
		t.Fatalf("manual mention=%v %v", p.entities, err)
	}
	date, err := prepareWriteText("today", "", []rpc.Entity{{Type: "date_time", Length: 5, DateUnix: 1700000000, Format: "d"}}, knownUser)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := date.entities[0].(*tg.MessageEntityFormattedDate); !ok || e.Date != 1700000000 || !e.ShortDate {
		t.Fatalf("date=%v", date.entities)
	}
	if _, err := prepareWriteText("today", "", []rpc.Entity{{Type: "date_time", Length: 5, Format: "!"}}, knownUser); err == nil {
		t.Fatal("invalid date format accepted")
	}
}

func TestAllManualEntityTypes(t *testing.T) {
	cases := []struct {
		typ      string
		wantType string
		check    func(t *testing.T, e tg.MessageEntityClass)
	}{
		{"bold", "messageEntityBold", nil},
		{"italic", "messageEntityItalic", nil},
		{"underline", "messageEntityUnderline", nil},
		{"strikethrough", "messageEntityStrike", nil},
		{"spoiler", "messageEntitySpoiler", nil},
		{"code", "messageEntityCode", nil},
		{"pre", "messageEntityPre", func(t *testing.T, e tg.MessageEntityClass) {
			if got := e.(*tg.MessageEntityPre).Language; got != "go" {
				t.Errorf("language = %q, want go", got)
			}
		}},
		{"blockquote", "messageEntityBlockquote", func(t *testing.T, e tg.MessageEntityClass) {
			if !e.(*tg.MessageEntityBlockquote).Collapsed {
				t.Error("collapsed flag lost")
			}
		}},
		{"text_url", "messageEntityTextUrl", func(t *testing.T, e tg.MessageEntityClass) {
			if got := e.(*tg.MessageEntityTextURL).URL; got != "https://example.com" {
				t.Errorf("url = %q", got)
			}
		}},
		{"url", "messageEntityUrl", nil},
		{"email", "messageEntityEmail", nil},
		{"phone", "messageEntityPhone", nil},
		{"mention", "messageEntityMention", nil},
		{"mention_name", "inputMessageEntityMentionName", func(t *testing.T, e tg.MessageEntityClass) {
			if got := e.(*tg.InputMessageEntityMentionName).UserID.(*tg.InputUser).UserID; got != 7 {
				t.Errorf("user id = %d, want 7", got)
			}
		}},
		{"hashtag", "messageEntityHashtag", nil},
		{"cashtag", "messageEntityCashtag", nil},
		{"bot_command", "messageEntityBotCommand", nil},
		{"custom_emoji", "messageEntityCustomEmoji", func(t *testing.T, e tg.MessageEntityClass) {
			if got := e.(*tg.MessageEntityCustomEmoji).DocumentID; got != 9 {
				t.Errorf("document id = %d, want 9", got)
			}
		}},
		{"date_time", "messageEntityFormattedDate", func(t *testing.T, e tg.MessageEntityClass) {
			if got := e.(*tg.MessageEntityFormattedDate); got.Date != 1700000000 || !got.ShortDate {
				t.Errorf("date = %+v", got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			item := rpc.Entity{Type: tc.typ, Length: 1, URL: "https://example.com", UserID: 7, CustomEmojiID: 9, DateUnix: 1700000000, Format: "d", Language: "go", Collapsed: true}
			p, err := prepareWriteText("x", "", []rpc.Entity{item}, knownUser)
			if err != nil || len(p.entities) != 1 || p.entities[0].GetLength() != 1 {
				t.Fatalf("entity=%v err=%v", p.entities, err)
			}
			if got := p.entities[0].TypeName(); got != tc.wantType {
				t.Fatalf("type = %s, want %s", got, tc.wantType)
			}
			if tc.check != nil {
				tc.check(t, p.entities[0])
			}
		})
	}
}

func TestFormattedWriteRequests(t *testing.T) {
	for _, mode := range []string{"html", "markdown", "rich-html", "rich-markdown", "entities"} {
		for _, forum := range []bool{false, true} {
			name := mode
			if forum {
				name += " forum"
			}
			t.Run(name, func(t *testing.T) {
				p := rpc.SendParams{Text: "<b>bold</b>", Parse: mode, Silent: true}
				if mode == "markdown" {
					p.Text = "**bold**"
				}
				if mode == "rich-markdown" {
					p.Text = "# heading"
				}
				if mode == "entities" {
					p.Parse = ""
					p.Text = "bold"
					p.Entities = []rpc.Entity{{Type: "bold", Length: 4}}
				}
				if forum {
					p.ReplyTo = 502
					p.Topic = 501
				}
				prepared, err := prepareWriteText(p.Text, p.Parse, p.Entities, knownUser)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
					calls++
					req, ok := input.(*tg.MessagesSendMessageRequest)
					if !ok {
						t.Fatalf("request=%T", input)
					}
					if !req.Silent {
						t.Error("silent flag lost")
					}
					if forum {
						r, ok := req.ReplyTo.(*tg.InputReplyToMessage)
						if !ok {
							t.Fatalf("r = %T, want *tg.InputReplyToMessage", req.ReplyTo)
						}
						if r.ReplyToMsgID != 502 {
							t.Errorf("reply=%d", r.ReplyToMsgID)
						}
						if top, _ := r.GetTopMsgID(); top != 501 {
							t.Errorf("top=%d", top)
						}
					}
					if strings.HasPrefix(mode, "rich") {
						if req.RichMessage == nil || req.Message != "" {
							t.Errorf("rich request=%+v", req)
						}
						if mode == "rich-html" {
							if _, ok := req.RichMessage.(*tg.InputRichMessageHTML); !ok {
								t.Errorf("rich type=%T", req.RichMessage)
							}
						}
						if mode == "rich-markdown" {
							if _, ok := req.RichMessage.(*tg.InputRichMessageMarkdown); !ok {
								t.Errorf("rich type=%T", req.RichMessage)
							}
						}
					} else if req.Message != "bold" || len(req.Entities) != 1 || req.Entities[0].TypeName() != "messageEntityBold" {
						t.Errorf("entities request=%+v", req)
					}
					return respond(output, &tg.UpdateShortSentMessage{ID: 17, Date: 1700000000})
				}))
				out, err := sendPreparedText(context.Background(), api, &tg.InputPeerUser{UserID: 42, AccessHash: 99}, 42, p, prepared)
				if err != nil || calls != 1 || out.ID != 17 {
					t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
				}
			})
		}
	}
}

func TestFormattedEditsAndRichRejection(t *testing.T) {
	for _, mode := range []string{"html", "markdown", "rich-html", "rich-markdown", "entities"} {
		t.Run(mode, func(t *testing.T) {
			p := rpc.EditParams{ID: 5, Text: "<b>bold</b>", Parse: mode}
			if mode == "markdown" {
				p.Text = "**bold**"
			}
			if mode == "rich-markdown" {
				p.Text = "# heading"
			}
			if mode == "entities" {
				p.Parse = ""
				p.Text = "bold"
				p.Entities = []rpc.Entity{{Type: "bold", Length: 4}}
			}
			prepared, err := prepareWriteText(p.Text, p.Parse, p.Entities, knownUser)
			if err != nil {
				t.Fatal(err)
			}
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				req, ok := input.(*tg.MessagesEditMessageRequest)
				if !ok {
					t.Fatalf("req = %T, want *tg.MessagesEditMessageRequest", input)
				}
				if strings.HasPrefix(mode, "rich") {
					if req.RichMessage == nil || req.Message != "" {
						t.Errorf("rich edit=%+v", req)
					}
					if mode == "rich-html" {
						if _, ok := req.RichMessage.(*tg.InputRichMessageHTML); !ok {
							t.Errorf("rich edit type=%T", req.RichMessage)
						}
					}
					if mode == "rich-markdown" {
						if _, ok := req.RichMessage.(*tg.InputRichMessageMarkdown); !ok {
							t.Errorf("rich edit type=%T", req.RichMessage)
						}
					}
				} else if req.Message != "bold" || len(req.Entities) != 1 {
					t.Errorf("edit=%+v", req)
				}
				return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &tg.Message{ID: 5, Date: 1700000000, PeerID: &tg.PeerUser{UserID: 42}}}}})
			}))
			out, err := editPreparedText(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, p, prepared)
			if err != nil || out.ID != 5 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		return tgerr.New(400, "RICH_MESSAGE_NOT_ALLOWED")
	}))
	_, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.SendParams{Text: "# heading", Parse: "rich-markdown"})
	formatErrorCode(t, err, rpc.CodeTelegram)
	if strings.Contains(err.Error(), "# heading") {
		t.Fatal("source leaked")
	}
	calls := 0
	floodAPI := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return tgerr.New(420, "FLOOD_WAIT_8")
	}))
	_, err = sendText(context.Background(), floodAPI, &tg.InputPeerUser{UserID: 42}, 42, rpc.SendParams{Text: "# heading", Parse: "rich-markdown"})
	formatErrorCode(t, err, rpc.CodeFloodWait)
	// sendText has no retry loop of its own; retries are the middleware's job
	// (see TestAudit10_WritesAreNotRetriedByTheThrottleMiddleware).
	if calls != 1 {
		t.Fatalf("sendText sent %d requests, want 1", calls)
	}
}

func TestPlainWriteRequestIsUnchanged(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesSendMessageRequest)
		if !ok {
			t.Fatalf("req = %T, want *tg.MessagesSendMessageRequest", input)
		}
		want := &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerUser{UserID: 42, AccessHash: 99}, Message: "plain", RandomID: req.RandomID}
		if !reflect.DeepEqual(req, want) {
			t.Errorf("plain request changed: got=%+v want=%+v", req, want)
		}
		return respond(output, &tg.UpdateShortSentMessage{ID: 17, Date: 1700000000})
	}))
	out, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 42, AccessHash: 99}, 42, rpc.SendParams{Text: "plain"})
	if err != nil || out.ID != 17 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestPlainEditRequestIsUnchanged(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesEditMessageRequest)
		if !ok {
			t.Fatalf("req = %T, want *tg.MessagesEditMessageRequest", input)
		}
		want := &tg.MessagesEditMessageRequest{Peer: &tg.InputPeerUser{UserID: 42, AccessHash: 99}, ID: 5, Message: "plain"}
		if !reflect.DeepEqual(req, want) {
			t.Errorf("plain edit changed: got=%+v want=%+v", req, want)
		}
		return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &tg.Message{ID: 5, Date: 1700000000, PeerID: &tg.PeerUser{UserID: 42}}}}})
	}))
	out, err := editText(context.Background(), api, &tg.InputPeerUser{UserID: 42, AccessHash: 99}, 42, rpc.EditParams{ID: 5, Text: "plain"})
	if err != nil || out.ID != 5 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestMentionResolverUsesPeerCache(t *testing.T) {
	ctx := context.Background()
	cache := &peers.InmemoryCache{}
	if err := cache.SaveUsers(ctx, &tg.User{ID: 7, AccessHash: 123}); err != nil {
		t.Fatal(err)
	}
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		t.Fatal("cache hit made Telegram request")
		return nil
	}))
	c := &Client{mgr: peers.Options{Cache: cache}.Build(api), mgrReady: true}
	user, err := c.mentionResolver(ctx)(7)
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := user.(*tg.InputUser); !ok || u.AccessHash != 123 {
		t.Fatalf("resolved user=%v", user)
	}
	for _, p := range mentionParams(7) {
		if _, err := c.CheckText(ctx, p); err != nil {
			t.Fatalf("cached mention: %v", err)
		}
	}
}

func mentionParams(id int64) []rpc.SendParams {
	return []rpc.SendParams{
		{Text: fmt.Sprintf(`<a href="tg://user?id=%d">x</a>`, id), Parse: "html"},
		{Text: fmt.Sprintf(`[x](tg://user?id=%d)`, id), Parse: "markdown"},
		{Text: "x", Entities: []rpc.Entity{{Type: "mention_name", Offset: 0, Length: 1, UserID: id}}},
	}
}

type mentionTimeout struct{}

func (*mentionTimeout) Error() string   { return "lookup timed out" }
func (*mentionTimeout) Timeout() bool   { return true }
func (*mentionTimeout) Temporary() bool { return true }

func TestUnknownMentionResolutionErrors(t *testing.T) {
	timeout := &mentionTimeout{}
	for _, tc := range []struct {
		name         string
		invoke       func(bin.Decoder) error
		code         int
		wantText     string
		contextError error
	}{
		{"empty user", func(out bin.Decoder) error {
			return respond(out, &tg.UserClassVector{Elems: []tg.UserClass{&tg.UserEmpty{ID: 999999999}}})
		}, rpc.CodeInvalidParams, "unknown user id for this session", nil},
		{"empty result", func(out bin.Decoder) error { return respond(out, &tg.UserClassVector{}) }, rpc.CodeInvalidParams, "unknown user id for this session", nil},
		{"USER_ID_INVALID", func(bin.Decoder) error { return tgerr.New(400, "USER_ID_INVALID") }, rpc.CodeInvalidParams, "unknown user id for this session", nil},
		{"PEER_ID_INVALID", func(bin.Decoder) error { return tgerr.New(400, "PEER_ID_INVALID") }, rpc.CodeInvalidParams, "unknown user id for this session", nil},
		{"user not found", func(bin.Decoder) error { return errors.New("user not found") }, rpc.CodeInvalidParams, "unknown user id for this session", nil},
		{"FLOOD_WAIT", func(bin.Decoder) error { return tgerr.New(420, "FLOOD_WAIT_8") }, rpc.CodeFloodWait, "", nil},
		{"not authorized", func(bin.Decoder) error { return tgerr.New(401, "AUTH_KEY_UNREGISTERED") }, rpc.CodeNotAuthorized, "", nil},
		{"canceled", func(bin.Decoder) error { return context.Canceled }, 0, "", context.Canceled},
		{"deadline", func(bin.Decoder) error { return context.DeadlineExceeded }, 0, "", context.DeadlineExceeded},
		{"timeout", func(bin.Decoder) error { return timeout }, 0, "", timeout},
	} {
		for _, p := range mentionParams(999999999) {
			name := tc.name + " / " + p.Parse
			if p.Entities != nil {
				name += "entities"
			}
			t.Run(name, func(t *testing.T) {
				calls := 0
				api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
					if _, ok := input.(*tg.UsersGetUsersRequest); !ok {
						t.Fatalf("unexpected Telegram request %T", input)
					}
					calls++
					return tc.invoke(out)
				}))
				c := &Client{mgr: peers.Options{}.Build(api), mgrReady: true, preloaded: true}
				check := func(err error) {
					if tc.contextError != nil {
						if !errors.Is(err, tc.contextError) {
							t.Fatalf("context error=%v", err)
						}
						return
					}
					var rpcErr *rpc.Error
					if !errors.As(err, &rpcErr) || rpcErr.Code != tc.code || (tc.wantText != "" && rpcErr.Message != tc.wantText) {
						t.Fatalf("error=%v", err)
					}
				}
				_, err := c.CheckText(context.Background(), p)
				check(err)
				_, err = c.Send(context.Background(), chatref.Ref{Kind: chatref.ID, ID: 42}, p)
				check(err)
				if calls != 2 {
					t.Fatalf("resolution calls=%d", calls)
				}
			})
		}
	}
}

func TestInvalidFormatDoesNotSend(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return nil }))
	_, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.SendParams{Text: "<b>bad", Parse: "html"})
	formatErrorCode(t, err, rpc.CodeInvalidParams)
	_, err = editText(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.EditParams{ID: 5, Text: "x", Entities: []rpc.Entity{{Type: "bold", Offset: 2, Length: 1}}})
	formatErrorCode(t, err, rpc.CodeInvalidParams)
	if calls != 0 {
		t.Fatalf("invalid input made %d Telegram calls", calls)
	}
}

func TestCheckTextDoesNotWrite(t *testing.T) {
	c := &Client{}
	for _, tc := range []struct {
		parse, source, mode string
		entities            []rpc.Entity
		length, count       int
		rich                bool
	}{
		{"", "plain", "plain", nil, 5, 0, false},
		{"html", "<b>hi</b>", "html", nil, 2, 1, false},
		{"markdown", "**hi**", "markdown", nil, 2, 1, false},
		{"rich-html", "<h1>hi</h1>", "rich-html", nil, 0, 0, true},
		{"rich-markdown", "# hi", "rich-markdown", nil, 0, 0, true},
		{"", "hi", "entities", []rpc.Entity{{Type: "bold", Length: 2}}, 2, 1, false},
	} {
		got, err := c.CheckText(context.Background(), rpc.SendParams{Text: tc.source, Parse: tc.parse, Entities: tc.entities})
		if err != nil || got.Mode != tc.mode || got.PlainLength != tc.length || got.Entities != tc.count || got.Rich != tc.rich {
			t.Fatalf("check=%+v err=%v", got, err)
		}
	}
	if _, err := c.CheckText(context.Background(), rpc.SendParams{Text: "<b>bad", Parse: "html"}); err == nil {
		t.Fatal("invalid HTML accepted")
	}
}

// TestCheckTextLocalLengthLimit covers the 4096 UTF-16 unit limit with emoji;
// the text holds no mention, so the resolver is never involved.
func TestCheckTextLocalLengthLimit(t *testing.T) {
	c := &Client{}
	for _, mode := range []string{"html", "markdown"} {
		text := strings.Repeat("😀", 4097)
		if mode == "html" {
			text = "<b>" + text + "</b>"
		} else {
			text = "**" + text + "**"
		}
		if err := c.CheckTextLocal(rpc.SendParams{Text: text, Parse: mode}); err == nil || !strings.Contains(err.Error(), "4096") {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}

func TestEditCaptionLimitErrorIsPreserved(t *testing.T) {
	for _, mode := range []string{"", "html", "rich-html"} {
		t.Run(mode, func(t *testing.T) {
			text := strings.Repeat("x", 1025)
			p := rpc.EditParams{ID: 5, Text: text, Parse: mode}
			prepared, err := prepareWriteText(text, mode, nil, knownUser)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
				calls++
				if _, ok := input.(*tg.MessagesEditMessageRequest); !ok {
					t.Fatalf("unexpected request: %T", input)
				}
				return tgerr.New(400, "MEDIA_CAPTION_TOO_LONG")
			}))
			_, err = editPreparedText(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, p, prepared)
			formatErrorCode(t, err, rpc.CodeTelegram)
			if calls != 1 || !strings.Contains(err.Error(), "MEDIA_CAPTION_TOO_LONG") {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}
