package tgraw

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tdp"
	"github.com/gotd/td/tg"
)

func messageFixture() *tg.Message {
	photo := &tg.Photo{ID: 9007199254740993, AccessHash: 77, FileReference: []byte{1, 2, 3}, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 100, H: 200, Size: 20}, &tg.PhotoStrippedSize{Type: "i", Bytes: []byte{4, 5}}}}
	media := &tg.MessageMediaPhoto{Photo: photo}
	media.SetFlags()
	reply := &tg.MessageReplyHeader{ReplyToMsgID: 42}
	reply.SetFlags()
	m := &tg.Message{ID: 1, FromID: &tg.PeerUser{UserID: 9007199254740993}, PeerID: &tg.PeerUser{UserID: 2}, Message: "hello", Media: media, ReplyTo: reply, Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 5}}}
	m.SetFlags()
	return m
}

func TestMarshal(t *testing.T) {
	filter := &tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 5, AccessHash: 123}}}
	filter.SetFlags()
	user := &tg.User{ID: 1, AccessHash: 9}
	user.SetFlags()
	channel := &tg.Channel{ID: 1, AccessHash: 9}
	channel.SetFlags()
	flags := &tg.AuthImportBotAuthorizationRequest{APIID: 1}
	flags.Flags = 1
	var int256 bin.Int256
	int256[0] = 0xab
	request := &tg.PhoneCreateConferenceCallRequest{}
	request.SetPublicKey(int256)
	tests := []struct {
		name             string
		obj              tdp.Object
		contains, absent []string
	}{
		{"message", messageFixture(), []string{`{"_":"message"`, `"from_id":{"_":"peerUser","user_id":9007199254740993}`, `"_":"messageMediaPhoto"`, `"_":"photo"`, `"_":"photoSize"`, `"_":"photoStrippedSize"`, `"bytes":"BAU="`, `"file_reference":"AQID"`, `"_":"messageReplyHeader"`, `"_":"messageEntityBold"`}, []string{`access_hash`, `"flags"`, `"out"`}},
		{"null optional", &tg.Message{ID: 1, FromID: &tg.PeerUser{UserID: 8}}, []string{`"id":1`}, []string{`"from_id"`}},
		{"user", user, []string{`"_":"user"`}, []string{`access_hash`}},
		{"channel", channel, []string{`"_":"channel"`}, []string{`access_hash`}},
		{"filter", filter, []string{`"_":"inputPeerChannel"`}, []string{`access_hash`}},
		{"photo", &tg.Photo{ID: 1, AccessHash: 9, FileReference: []byte{255}}, []string{`"file_reference":"/w=="`}, []string{`access_hash`}},
		{"document", &tg.Document{ID: 1, AccessHash: 9}, []string{`"_":"document"`}, []string{`access_hash`}},
		{"authorization", &tg.Authorization{Hash: 99, DeviceModel: "device"}, []string{`"device_model":"device"`}, []string{`"hash"`}},
		{"flags", flags, []string{`"api_id":1`}, []string{`"flags"`}},
		{"ordinary hash", &tg.MessagesGetHistoryRequest{Hash: 9007199254740993}, []string{`"hash":9007199254740993`}, nil},
		{"int256", request, []string{`"public_key":"ab00000000000000000000000000000000000000000000000000000000000000"`}, nil},
		{"json value", &tg.HelpAppConfig{Config: &tg.JSONObject{Value: []tg.JSONObjectValue{{Key: "a", Value: &tg.JSONArray{Value: []tg.JSONValueClass{&tg.JSONBool{}, &tg.JSONNumber{}, &tg.JSONString{}, &tg.JSONNull{}}}}, {Key: "access_hash", Value: &tg.JSONString{Value: "secret"}}}}}, []string{`"config":{"a":[false,0,"",null]}`}, []string{`access_hash`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(tt.obj)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(got) {
				t.Fatalf("invalid JSON: %s", got)
			}
			for _, s := range tt.contains {
				if !bytes.Contains(got, []byte(s)) {
					t.Errorf("missing %s in %s", s, got)
				}
			}
			for _, s := range tt.absent {
				if bytes.Contains(got, []byte(s)) {
					t.Errorf("unexpected %s in %s", s, got)
				}
			}
			again, err := Marshal(tt.obj)
			if err != nil || !bytes.Equal(got, again) {
				t.Fatalf("not deterministic: %s %s %v", got, again, err)
			}
		})
	}
}

type unsupported struct{ Value map[string]int }

func (*unsupported) TypeName() string { return "unsupported" }
func (*unsupported) TypeInfo() tdp.Type {
	return tdp.Type{Name: "unsupported", Fields: []tdp.Field{{Name: "Value", SchemaName: "value"}}}
}
func TestErrors(t *testing.T) {
	var nilUser *tg.User
	for _, obj := range []tdp.Object{nil, nilUser, &unsupported{Value: map[string]int{"a": 1}}} {
		if _, err := Marshal(obj); err == nil {
			t.Errorf("expected error for %T", obj)
		}
	}
}
func TestConstructors(t *testing.T) {
	for _, ctor := range tg.TypesConstructorMap() {
		obj := ctor()
		o, ok := obj.(tdp.Object)
		if !ok {
			t.Errorf("%T lacks TypeInfo", obj)
			continue
		}
		b, err := Marshal(o)
		if err != nil {
			t.Errorf("%T: %v", o, err)
			continue
		}
		if !json.Valid(b) {
			t.Errorf("%T invalid JSON %s", o, b)
		}
		if len(b) > 0 && b[0] == '{' && !strings.HasPrefix(string(b), `{"_":`) {
			t.Errorf("%T missing first type key: %s", o, b)
		}
	}
}

func TestSchemaOrder(t *testing.T) {
	got, err := Marshal(&tg.Photo{ID: 5, AccessHash: 42, FileReference: []byte{1}, Date: 7, DCID: 2})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"_":"photo","id":5,"file_reference":"AQ==","date":7,"dc_id":2}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
