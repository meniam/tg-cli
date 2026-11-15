package tgclient

import (
	"context"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
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
		if _, ok := req.Rules[0].(*tg.InputPrivacyValueAllowUsers); !ok {
			t.Fatalf("first %T", req.Rules[0])
		}
		if _, ok := req.Rules[1].(*tg.InputPrivacyValueDisallowUsers); !ok {
			t.Fatalf("second %T", req.Rules[1])
		}
		if _, ok := req.Rules[2].(*tg.InputPrivacyValueAllowChatParticipants); !ok {
			t.Fatalf("third %T", req.Rules[2])
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
		return respond(output, &tg.AccountPrivacyRules{Rules: []tg.PrivacyRuleClass{&tg.PrivacyValueDisallowAll{}}})
	}))
	p := rpc.PrivacySetParams{Key: "last-seen", Default: "contacts", AllowPremium: true, DenyBots: true}
	result, err := accountPrivacySetAPI(context.Background(), api, p, []tg.InputUserClass{&tg.InputUser{UserID: 1}}, []tg.InputUserClass{&tg.InputUser{UserID: 2}}, []int64{3}, nil)
	if err != nil || result.Default != "nobody" {
		t.Fatalf("result %+v err %v", result, err)
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
