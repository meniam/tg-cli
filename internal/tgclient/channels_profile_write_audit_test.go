package tgclient

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestAudit08_ChannelSettingValueIsPassedThrough(t *testing.T) {
	for _, tc := range []struct {
		flag, value string
		want        bool
	}{
		{"antispam", "on", true}, {"antispam", "off", false},
		{"forum", "on", true}, {"forum", "off", false},
		{"prehistory", "hidden", true}, {"prehistory", "visible", false},
		{"join-requests", "on", true}, {"join-requests", "off", false},
		{"join-to-send", "on", true}, {"join-to-send", "off", false},
	} {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			var seen []bool
			api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch v := in.(type) {
				case *tg.ChannelsToggleAntiSpamRequest:
					seen = append(seen, v.Enabled)
				case *tg.ChannelsToggleForumRequest:
					seen = append(seen, v.Enabled)
				case *tg.ChannelsTogglePreHistoryHiddenRequest:
					seen = append(seen, v.Enabled)
				case *tg.ChannelsToggleJoinRequestRequest:
					seen = append(seen, v.Enabled)
				case *tg.ChannelsToggleJoinToSendRequest:
					seen = append(seen, v.Enabled)
				default:
					t.Errorf("unexpected request %T", in)
				}
				return respond(out, &tg.Updates{})
			}))
			ch := testPeers(api).Channel(channel(9, "Group", "group", false, true))
			changed, err := setChannelSettingAPI(context.Background(), api, ch, tc.flag, tc.value, nil, nil)
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			if len(seen) != 1 || seen[0] != tc.want {
				t.Errorf("Enabled sent = %v, want [%v]", seen, tc.want)
			}
		})
	}
}

func TestAudit08_ChannelSignaturesAndProfilesAreIndependent(t *testing.T) {
	on, off := "on", "off"
	for _, tc := range []struct {
		name                    string
		signatures, profiles    *string
		wantSignatures, wantPro bool
	}{
		{"signatures on, profiles off", &on, &off, true, false},
		{"signatures off, profiles on", &off, &on, false, true},
		{"both off", &off, &off, false, false},
		{"both on", &on, &on, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []*tg.ChannelsToggleSignaturesRequest
			api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				v, ok := in.(*tg.ChannelsToggleSignaturesRequest)
				if !ok {
					t.Errorf("unexpected request %T", in)
					return respond(out, &tg.Updates{})
				}
				got = append(got, v)
				return respond(out, &tg.Updates{})
			}))
			ch := testPeers(api).Channel(channel(9, "Group", "group", false, true))
			changed, err := setChannelSettingAPI(context.Background(), api, ch, "signatures", "on", tc.signatures, tc.profiles)
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			if len(got) != 1 || got[0].SignaturesEnabled != tc.wantSignatures || got[0].ProfilesEnabled != tc.wantPro {
				t.Errorf("request = %+v, want signatures=%v profiles=%v", got, tc.wantSignatures, tc.wantPro)
			}
		})
	}
}
