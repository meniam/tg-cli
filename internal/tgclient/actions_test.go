package tgclient

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func actionAPI(t *testing.T, check func(bin.Encoder)) (*tg.Client, *int) {
	t.Helper()
	calls := new(int)
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		*calls = *calls + 1
		check(input)
		return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{
			&tg.UpdateMessageID{ID: 17, RandomID: 1},
			&tg.UpdateNewMessage{Message: &tg.Message{ID: 17, Date: 1700000000, PeerID: &tg.PeerUser{UserID: 42}}},
		}})
	}))
	return api, calls
}

func TestReactAndPinRequests(t *testing.T) {
	peer := &tg.InputPeerUser{UserID: 42}
	for _, remove := range []bool{false, true} {
		t.Run("reaction remove="+strconv.FormatBool(remove), func(t *testing.T) {
			api, calls := actionAPI(t, func(input bin.Encoder) {
				req, ok := input.(*tg.MessagesSendReactionRequest)
				if !ok {
					t.Errorf("request type %T", input)
					return
				}
				wantCount := 1
				if remove {
					wantCount = 0
				}
				if req.MsgID != 5 || req.Peer != peer || req.Big || req.AddToRecent || req.Reaction == nil || len(req.Reaction) != wantCount {
					t.Errorf("reaction request: %+v", req)
				}
				if !remove {
					if emoji, ok := req.Reaction[0].(*tg.ReactionEmoji); !ok || emoji.Emoticon != "👍" {
						t.Errorf("reaction: %+v", req.Reaction)
					}
				}
			})
			got, err := react(context.Background(), api, peer, 42, rpc.ReactParams{ID: 5, Emoji: "👍", Remove: remove})
			if err != nil || got.ChatID != 42 || got.ID != 5 || got.Emoji != "👍" || got.Removed != remove || *calls != 1 {
				t.Fatalf("reaction=%+v calls=%d err=%v", got, *calls, err)
			}
		})
	}
	for _, tc := range []struct {
		name          string
		silent, unpin bool
	}{
		{"pin notified", false, false},
		{"pin silent", true, false},
		{"unpin", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, calls := actionAPI(t, func(input bin.Encoder) {
				req, ok := input.(*tg.MessagesUpdatePinnedMessageRequest)
				if !ok || req.Peer != peer || req.ID != 5 || req.Silent != tc.silent || req.Unpin != tc.unpin || req.PmOneside {
					t.Errorf("pin request: %+v", input)
				}
			})
			got, err := updatePin(context.Background(), api, peer, 42, 5, tc.silent, tc.unpin)
			if err != nil || got.ChatID != 42 || got.ID != 5 || got.Pinned == tc.unpin || *calls != 1 {
				t.Fatalf("pin=%+v calls=%d err=%v", got, *calls, err)
			}
		})
	}
}

func TestPollRequestVariants(t *testing.T) {
	peer := &tg.InputPeerUser{UserID: 42}
	for _, tc := range []struct {
		name string
		p    rpc.PollParams
	}{
		{"plain", rpc.PollParams{Question: "Question?", Options: []string{"A", "B"}}},
		{"multiple public", rpc.PollParams{Question: "Question?", Options: []string{"A", "B"}, Multiple: true, Public: true}},
		{"quiz", rpc.PollParams{Question: "Question?", Options: []string{"A", "B"}, Quiz: true, Correct: 2, Explanation: "Because", Silent: true}},
		{"topic reply", rpc.PollParams{Question: "Question?", Options: []string{"A", "B"}, ReplyTo: 502, Topic: 501}},
		{"topic only", rpc.PollParams{Question: "Question?", Options: []string{"A", "B"}, Topic: 501}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, calls := actionAPI(t, func(input bin.Encoder) {
				req, ok := input.(*tg.MessagesSendMediaRequest)
				if !ok {
					t.Errorf("request type %T", input)
					return
				}
				media, ok := req.Media.(*tg.InputMediaPoll)
				if !ok || req.Peer != peer || req.RandomID == 0 || req.Silent != tc.p.Silent || req.Message != "" {
					t.Errorf("media request: %+v", req)
					return
				}
				poll := media.Poll
				if poll.ID == 0 || poll.Question.Text != tc.p.Question || poll.MultipleChoice != tc.p.Multiple || poll.PublicVoters != tc.p.Public || poll.Quiz != tc.p.Quiz || len(poll.Answers) != len(tc.p.Options) {
					t.Errorf("poll: %+v", poll)
				}
				for i, value := range poll.Answers {
					answer, ok := value.(*tg.PollAnswer)
					if !ok || answer.Text.Text != tc.p.Options[i] || string(answer.Option) != strconv.Itoa(i) {
						t.Errorf("answer %d: %+v", i, value)
					}
				}
				if tc.p.Quiz {
					if len(media.CorrectAnswers) != 1 || media.CorrectAnswers[0] != tc.p.Correct-1 || media.Solution != tc.p.Explanation {
						t.Errorf("quiz media: %+v", media)
					}
				} else if media.CorrectAnswers != nil || media.Solution != "" {
					t.Errorf("plain media: %+v", media)
				}
				wantReply := tc.p.ReplyTo
				if wantReply == 0 {
					wantReply = tc.p.Topic
				}
				if wantReply == 0 && req.ReplyTo != nil {
					t.Errorf("unexpected reply: %+v", req.ReplyTo)
				} else if wantReply > 0 {
					reply, ok := req.ReplyTo.(*tg.InputReplyToMessage)
					if !ok || reply.ReplyToMsgID != wantReply {
						t.Errorf("reply: %+v", req.ReplyTo)
					} else {
						top, present := reply.GetTopMsgID()
						wantTop := tc.p.Topic > 1 && tc.p.ReplyTo > 0 && tc.p.ReplyTo != tc.p.Topic
						if present != wantTop || (wantTop && top != tc.p.Topic) {
							t.Errorf("top=%d present=%v", top, present)
						}
					}
				}
			})
			got, err := sendPoll(context.Background(), api, peer, 42, tc.p)
			if err != nil || got.ChatID != 42 || got.ID != 17 || got.Date != time.Unix(1700000000, 0).UTC().Format(time.RFC3339) || *calls != 1 {
				t.Fatalf("poll=%+v calls=%d err=%v", got, *calls, err)
			}
		})
	}
}

func TestPollValidationBoundaries(t *testing.T) {
	base := rpc.PollParams{Question: "Q", Options: []string{"A", "B"}}
	options := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = strconv.Itoa(i)
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		edit  func(*rpc.PollParams)
		valid bool
	}{
		{"question 255", func(p *rpc.PollParams) { p.Question = strings.Repeat("Я", 255) }, true},
		{"question 256", func(p *rpc.PollParams) { p.Question = strings.Repeat("Я", 256) }, false},
		{"question blank", func(p *rpc.PollParams) { p.Question = " \t" }, false},
		{"option 100", func(p *rpc.PollParams) { p.Options[0] = strings.Repeat("Я", 100) }, true},
		{"option 101", func(p *rpc.PollParams) { p.Options[0] = strings.Repeat("Я", 101) }, false},
		{"option blank", func(p *rpc.PollParams) { p.Options[0] = " \t" }, false},
		{"one option", func(p *rpc.PollParams) { p.Options = options(1) }, false},
		{"two options", func(p *rpc.PollParams) { p.Options = options(2) }, true},
		{"ten options", func(p *rpc.PollParams) { p.Options = options(10) }, true},
		{"eleven options", func(p *rpc.PollParams) { p.Options = options(11) }, false},
		{"duplicate", func(p *rpc.PollParams) { p.Options[1] = "A" }, false},
		{"duplicate after trim", func(p *rpc.PollParams) { p.Options[1] = " A " }, false},
		{"explanation 200", func(p *rpc.PollParams) { p.Quiz, p.Correct, p.Explanation = true, 1, strings.Repeat("Я", 200) }, true},
		{"explanation 201", func(p *rpc.PollParams) { p.Quiz, p.Correct, p.Explanation = true, 1, strings.Repeat("Я", 201) }, false},
		{"explanation two line feeds", func(p *rpc.PollParams) { p.Quiz, p.Correct, p.Explanation = true, 1, "a\nb\nc" }, true},
		{"explanation three line feeds", func(p *rpc.PollParams) { p.Quiz, p.Correct, p.Explanation = true, 1, "a\nb\nc\nd" }, false},
		{"explanation without quiz", func(p *rpc.PollParams) { p.Explanation = "why" }, false},
		{"correct zero", func(p *rpc.PollParams) { p.Quiz = true }, false},
		{"correct too high", func(p *rpc.PollParams) { p.Quiz, p.Correct = true, 3 }, false},
		{"quiz multiple", func(p *rpc.PollParams) { p.Quiz, p.Correct, p.Multiple = true, 1, true }, false},
		{"negative topic", func(p *rpc.PollParams) { p.Topic = -1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			p.Options = append([]string(nil), base.Options...)
			tc.edit(&p)
			api, calls := actionAPI(t, func(bin.Encoder) {})
			_, err := sendPoll(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, p)
			if tc.valid {
				if err != nil || *calls != 1 {
					t.Fatalf("valid poll: err=%v calls=%d", err, *calls)
				}
			} else if *calls != 0 || actionCode(err) != rpc.CodeInvalidParams {
				t.Fatalf("invalid poll: err=%v calls=%d", err, *calls)
			}
		})
	}
}

func actionCode(err error) int {
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}
	return 0
}

func TestActionErrorsAndCancellation(t *testing.T) {
	peer := &tg.InputPeerUser{UserID: 42}
	for _, tc := range []struct {
		typ  string
		code int
	}{
		{"REACTION_INVALID", rpc.CodeInvalidParams},
		{"MESSAGE_ID_INVALID", rpc.CodeInvalidParams},
		{"POLL_QUESTION_INVALID", rpc.CodeInvalidParams},
		{"QUIZ_MULTIPLE_INVALID", rpc.CodeInvalidParams},
		{"CHAT_SEND_POLL_FORBIDDEN", rpc.CodeTelegram},
		{"CHAT_ADMIN_REQUIRED", rpc.CodeTelegram},
	} {
		err := mapActionError(tgerr.New(400, tc.typ))
		if got := actionCode(err); got != tc.code {
			t.Errorf("%s: code=%d err=%v", tc.typ, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api, calls := actionAPI(t, func(bin.Encoder) {})
	for _, call := range []func() error{
		func() error { _, err := react(ctx, api, peer, 42, rpc.ReactParams{ID: 5, Emoji: "👍"}); return err },
		func() error { _, err := updatePin(ctx, api, peer, 42, 5, false, false); return err },
		func() error {
			_, err := sendPoll(ctx, api, peer, 42, rpc.PollParams{Question: "Q", Options: []string{"A", "B"}})
			return err
		},
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("cancellation: %v", err)
		}
	}
	if *calls != 0 {
		t.Fatalf("cancelled requests invoked Telegram %d times", *calls)
	}
}

func TestActionDryRunAndTelegramErrors(t *testing.T) {
	client := &Client{}
	ref := chatref.Ref{}
	for _, call := range []func() error{
		func() error {
			_, err := client.React(context.Background(), ref, rpc.ReactParams{DryRun: true})
			return err
		},
		func() error { _, err := client.Pin(context.Background(), ref, rpc.PinParams{DryRun: true}); return err },
		func() error {
			_, err := client.Unpin(context.Background(), ref, rpc.UnpinParams{DryRun: true})
			return err
		},
		func() error {
			_, err := client.SendPoll(context.Background(), ref, rpc.PollParams{DryRun: true})
			return err
		},
	} {
		if err := call(); actionCode(err) != rpc.CodeInvalidParams {
			t.Errorf("dry run: %v", err)
		}
	}
	peer := &tg.InputPeerUser{UserID: 42}
	for _, tc := range []struct {
		name, telegramError string
		call                func(*tg.Client) error
		code                int
	}{
		{"reaction", "REACTION_INVALID", func(api *tg.Client) error {
			_, err := react(context.Background(), api, peer, 42, rpc.ReactParams{ID: 5, Emoji: "👍"})
			return err
		}, rpc.CodeInvalidParams},
		{"pin", "CHAT_ADMIN_REQUIRED", func(api *tg.Client) error {
			_, err := updatePin(context.Background(), api, peer, 42, 5, false, false)
			return err
		}, rpc.CodeTelegram},
		{"poll", "POLL_QUESTION_INVALID", func(api *tg.Client) error {
			_, err := sendPoll(context.Background(), api, peer, 42, rpc.PollParams{Question: "Q", Options: []string{"A", "B"}})
			return err
		}, rpc.CodeInvalidParams},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
				calls++
				return tgerr.New(400, tc.telegramError)
			}))
			if err := tc.call(api); actionCode(err) != tc.code || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestReactValidationBeforeTelegram(t *testing.T) {
	for _, tc := range []rpc.ReactParams{
		{ID: 0, Emoji: "👍"},
		{ID: 5, Emoji: ""},
		{ID: 5, Emoji: "a b"},
		{ID: 5, Emoji: strings.Repeat("a", 33)},
	} {
		api, calls := actionAPI(t, func(bin.Encoder) {})
		_, err := react(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, tc)
		if actionCode(err) != rpc.CodeInvalidParams || *calls != 0 {
			t.Errorf("params=%+v err=%v calls=%d", tc, err, *calls)
		}
	}
}

func TestPollQuestionInReadMessage(t *testing.T) {
	media := &tg.MessageMediaPoll{Poll: tg.Poll{Question: tg.TextWithEntities{Text: "Question?"}, Answers: []tg.PollAnswerClass{
		&tg.PollAnswer{Text: tg.TextWithEntities{Text: "A"}, Option: []byte("0")},
		&tg.PollAnswer{Text: tg.TextWithEntities{Text: "B"}, Option: []byte("1")},
	}}}
	for _, tc := range []struct{ message, want string }{{"", "Question?"}, {"caption", "caption"}} {
		got, ok := toMessage(42, &tg.Message{ID: 5, Date: 1700000000, Message: tc.message, Media: media}, peer.Entities{}, nil)
		if !ok || got.Text != tc.want || got.MediaType != "poll" {
			t.Errorf("message=%q converted=%+v ok=%v", tc.message, got, ok)
		}
	}
}
