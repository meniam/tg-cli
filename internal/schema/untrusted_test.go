package schema_test

import (
	"reflect"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// Every string of these response types must be classified when a field is
// added: otherwise new Telegram text could silently bypass the human frame.
func TestEveryResponseStringHasTrustClassification(t *testing.T) {
	cases := []struct {
		value  any
		fields map[string]bool
	}{
		{rpc.Chat{}, map[string]bool{"type": false, "title": true, "username": true}},
		{rpc.ChatInfo{}, map[string]bool{"type": false, "title": true, "username": true, "about": true, "restriction_reason.platform": false, "restriction_reason.reason": false, "restriction_reason.text": true}},
		{rpc.InviteInfo{}, map[string]bool{"title": true, "type": false, "about": true}},
		{rpc.Sender{}, map[string]bool{"name": true, "username": true}},
		{rpc.Message{}, map[string]bool{"date": false, "sender.name": true, "sender.username": true, "text": true, "media_type": false, "service": false}},
		{rpc.SearchHit{}, map[string]bool{"scope": false, "title": true, "snippet": true, "date": false, "author": false, "model": false, "note_kind": false, "link": false, "link_unavailable": false}},
	}
	for _, tc := range cases {
		t.Run(reflect.TypeOf(tc.value).Name(), func(t *testing.T) {
			seen := map[string]bool{}
			for _, field := range schema.Fields(reflect.TypeOf(tc.value)) {
				if field.Type != schema.TypeString {
					continue
				}
				want, ok := tc.fields[field.Path]
				if !ok {
					t.Errorf("string field %s is not classified", field.Path)
					continue
				}
				if field.Untrusted != want {
					t.Errorf("%s untrusted=%v, want %v", field.Path, field.Untrusted, want)
				}
				seen[field.Path] = true
			}
			for path := range tc.fields {
				if !seen[path] {
					t.Errorf("classified field %s is absent", path)
				}
			}
		})
	}
}
