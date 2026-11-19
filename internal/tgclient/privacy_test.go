package tgclient

import (
	"context"
	"errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"slices"
	"testing"
	"tg-cli/internal/rpc"
)

func TestPrivacyRuleConversion(t *testing.T) {
	for _, key := range rpc.PrivacyKeys {
		if privacyKey(key) == nil {
			t.Fatalf("missing key %s", key)
		}
	}
	source := &tg.AccountPrivacyRules{Rules: []tg.PrivacyRuleClass{&tg.PrivacyValueAllowUsers{Users: []int64{7}}, &tg.PrivacyValueDisallowChatParticipants{Chats: []int64{9}}, &tg.PrivacyValueAllowPremium{}, &tg.PrivacyValueAllowContacts{}, &tg.PrivacyValueDisallowAll{}}, Chats: []tg.ChatClass{&tg.Channel{ID: 9}}}
	r, err := privacyFromTL("last-seen", source)
	if err != nil {
		t.Fatal(err)
	}
	if r.Default != "contacts" || len(r.AllowUsers) != 1 || r.AllowUsers[0] != 7 || len(r.DenyChats) != 1 || r.DenyChats[0] != -1000000000009 || !r.AllowPremium {
		t.Fatalf("rules %+v", r)
	}
	source.Chats = nil
	r, err = privacyFromTL("last-seen", source)
	if err != nil || r.Hidden != 1 || len(r.DenyChats) != 0 {
		t.Fatalf("hidden %+v %v", r, err)
	}
}

func TestPrivacySetRequest(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.AccountSetPrivacyRequest)
		if !ok {
			t.Fatalf("request %T", input)
		}
		if _, ok := req.Key.(*tg.InputPrivacyKeyStatusTimestamp); !ok {
			t.Fatalf("key %T", req.Key)
		}
		if len(req.Rules) != 7 {
			t.Fatalf("rules %#v", req.Rules)
		}
		if r, ok := req.Rules[0].(*tg.InputPrivacyValueAllowUsers); !ok || len(r.Users) != 1 || r.Users[0].(*tg.InputUser).UserID != 1 {
			t.Fatalf("first %#v", req.Rules[0])
		}
		if r, ok := req.Rules[1].(*tg.InputPrivacyValueDisallowUsers); !ok || len(r.Users) != 1 || r.Users[0].(*tg.InputUser).UserID != 2 {
			t.Fatalf("second %#v", req.Rules[1])
		}
		if r, ok := req.Rules[2].(*tg.InputPrivacyValueAllowChatParticipants); !ok || !slices.Equal(r.Chats, []int64{3}) {
			t.Fatalf("third %#v", req.Rules[2])
		}
		if _, ok := req.Rules[3].(*tg.InputPrivacyValueAllowPremium); !ok {
			t.Fatalf("fourth %T", req.Rules[3])
		}
		if _, ok := req.Rules[4].(*tg.InputPrivacyValueDisallowBots); !ok {
			t.Fatalf("fifth %T", req.Rules[4])
		}
		if _, ok := req.Rules[5].(*tg.InputPrivacyValueAllowContacts); !ok {
			t.Fatalf("sixth %T", req.Rules[5])
		}
		// "contacts" must end with a deny-all rule, otherwise everyone else stays allowed.
		if _, ok := req.Rules[6].(*tg.InputPrivacyValueDisallowAll); !ok {
			t.Fatalf("seventh %T", req.Rules[6])
		}
		return respond(output, &tg.AccountPrivacyRules{Rules: []tg.PrivacyRuleClass{&tg.PrivacyValueDisallowAll{}}})
	}))
	p := rpc.PrivacySetParams{Key: "last-seen", Default: "contacts", AllowPremium: true, DenyBots: true}
	result, err := accountPrivacySetAPI(context.Background(), api, p, []tg.InputUserClass{&tg.InputUser{UserID: 1}}, []tg.InputUserClass{&tg.InputUser{UserID: 2}}, []int64{3}, nil)
	if err != nil || result.Default != "nobody" {
		t.Fatalf("result %+v err %v", result, err)
	}
}

func TestAudit09_PrivacySetRejectsAmbiguousChatAndInvalidDefault(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		t.Error("request must not reach Telegram")
		return errors.New("unexpected request")
	}))
	for _, tc := range []struct {
		name   string
		p      rpc.PrivacySetParams
		ac, dc []int64
		want   string
	}{
		{"chat in both lists", rpc.PrivacySetParams{Key: "last-seen", Default: "nobody"}, []int64{3}, []int64{3}, "ambiguous or duplicate privacy chat"},
		{"duplicate chat", rpc.PrivacySetParams{Key: "last-seen", Default: "nobody"}, []int64{3, 3}, nil, "ambiguous or duplicate privacy chat"},
		{"invalid default", rpc.PrivacySetParams{Key: "last-seen", Default: "friends"}, nil, nil, "invalid default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := accountPrivacySetAPI(context.Background(), api, tc.p, nil, nil, tc.ac, tc.dc)
			var re *rpc.Error
			if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams || re.Message != tc.want {
				t.Fatalf("error %v, want invalid params %q", err, tc.want)
			}
		})
	}
}

func TestPrivacyGetRequest(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.AccountGetPrivacyRequest)
		if !ok {
			t.Fatalf("request %T", input)
		}
		if _, ok := req.Key.(*tg.InputPrivacyKeyPhoneNumber); !ok {
			t.Fatalf("key %T", req.Key)
		}
		return respond(output, &tg.AccountPrivacyRules{Rules: []tg.PrivacyRuleClass{&tg.PrivacyValueDisallowAll{}}})
	}))
	r, err := accountPrivacyGetAPI(context.Background(), api, "phone-number")
	if err != nil || r.Default != "nobody" {
		t.Fatalf("result %+v %v", r, err)
	}
}
