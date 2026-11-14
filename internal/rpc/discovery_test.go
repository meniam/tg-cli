package rpc_test

import (
	"reflect"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func TestDiscoverySchemasMarkOtherPeopleText(t *testing.T) {
	fields := schema.Fields(reflect.TypeOf(rpc.PublicSearchResult{}))
	marked := map[string]bool{"hits.chat.title": false, "hits.chat.username": false, "hits.message.text": false, "hits.message.sender.name": false}
	for _, field := range fields {
		if _, ok := marked[field.Path]; ok {
			marked[field.Path] = field.Untrusted
		}
	}
	for path, ok := range marked {
		if !ok {
			t.Errorf("untrusted field %s missing", path)
		}
	}
	for _, field := range schema.Fields(reflect.TypeOf(rpc.LinkResult{})) {
		if field.Path == "link" && field.Untrusted {
			t.Fatal("generated link must not be untrusted")
		}
	}
}
