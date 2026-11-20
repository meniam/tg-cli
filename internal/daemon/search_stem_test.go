package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func searchStemString(s string) *string { return &s }

// The stemmed leg must obey the access list like the exact one: "trees" reaches
// "tree" only through stems, and a closed chat must stay out of both legs.
func TestSearchStemLegHidesClosedChatsAndCarriesLinks(t *testing.T) {
	d, _, db, path := accessTestDaemon(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{
		{ID: -1001000000001, Type: "supergroup", Title: "Open", Username: searchStemString("opengroup")},
		{ID: -1001000000002, Type: "supergroup", Title: "Closed", Username: searchStemString("closedgroup")},
		{ID: 55, Type: "user", Title: "Alice"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []store.Message{
		{ChatID: -1001000000001, ID: 11, Date: 1, Text: "The tree is tall", Raw: []byte(`{}`)},
		{ChatID: -1001000000002, ID: 22, Date: 2, Text: "The secret tree", Raw: []byte(`{}`)},
		{ChatID: 55, ID: 33, Date: 3, Text: "A tree in a private chat", Raw: []byte(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	writeAccess(t, path, `{"block":[-1001000000002]}`)
	result, err := d.search(ctx, params(t, rpc.SearchParams{Query: "trees", Scopes: []string{"messages"}}))
	if err != nil {
		t.Fatal(err)
	}
	hits := result.(rpc.SearchResult).Hits
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want the open group and the private chat", hits)
	}
	for _, hit := range hits {
		switch hit.ID {
		case 11:
			if hit.Link == nil || *hit.Link != "https://t.me/opengroup/11" || hit.LinkUnavailable != "" {
				t.Errorf("open group hit = %+v", hit)
			}
		case 33:
			if hit.Link != nil || hit.LinkUnavailable != "private_chat" {
				t.Errorf("private chat hit = %+v", hit)
			}
		default:
			t.Errorf("closed chat leaked through a leg: %+v", hit)
		}
	}
}
