package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func mediaPtr(s string) *string { return &s }

func TestSearchMediaFilterAndHitType(t *testing.T) {
	client, db, tg, _ := rpcTestDaemon(t)
	ctx := context.Background()
	if err := db.UpsertMessages(ctx, []store.Message{
		{ChatID: 1, ID: 1, Date: 100, Text: "годовой отчёт", MediaType: mediaPtr("voice"), Raw: []byte(`{}`)},
		{ChatID: 1, ID: 2, Date: 101, Text: "годовой отчёт", MediaType: mediaPtr("photo"), Raw: []byte(`{}`)},
		{ChatID: 1, ID: 3, Date: 102, Text: "годовой отчёт", Raw: []byte(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	var result rpc.SearchResult
	if err := rpcCall(t, client, rpc.MethodSearch, rpc.SearchParams{Query: "отчёт", Media: "voice"}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].ID != 1 || result.Hits[0].Scope != "messages" || result.Hits[0].MediaType != "voice" {
		t.Fatalf("voice filter without scope = %+v", result.Hits)
	}
	if err := rpcCall(t, client, rpc.MethodSearch, rpc.SearchParams{Query: "отчёт", Scopes: []string{"messages"}}, &result); err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for _, hit := range result.Hits {
		got[hit.ID] = hit.MediaType
	}
	if len(got) != 3 || got[1] != "voice" || got[2] != "photo" || got[3] != "" {
		t.Errorf("media types of unfiltered hits = %v", got)
	}
	for _, params := range []rpc.SearchParams{
		{Query: "отчёт", Media: "sticker"},
		{Query: "отчёт", Media: "bad"},
		{Query: "отчёт", Media: "voice", Scopes: []string{"notes"}},
		{Query: "отчёт", Media: "voice", Scopes: []string{"messages", "chats"}},
	} {
		if err := rpcCall(t, client, rpc.MethodSearch, params, nil); rpcCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("params %+v: error = %v, want invalid params", params, err)
		}
	}
	if n := resolveCount(tg); n != 0 {
		t.Errorf("a local search asked Telegram %d times", n)
	}
}
