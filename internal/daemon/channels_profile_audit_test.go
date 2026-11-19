package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/rpc"
)

// Positive control for the blocked-person filters: the same fake answer with a
// policy that blocks someone else must come through intact.
func TestAudit03ReadersAndRequestsKeepAllowedPeople(t *testing.T) {
	d, tg, _, path := accessTestDaemon(t)
	writeAccess(t, path, `{"block":[6]}`)
	tg.resolved = &rpc.Chat{ID: -10, Type: "group"}
	result, err := d.messageReaders(context.Background(), params(t, rpc.MessageGetParams{Chat: "-10", ID: 11}))
	if err != nil {
		t.Fatal(err)
	}
	readers := result.(rpc.MessageReadersResult).Readers
	if len(readers) != 1 || readers[0].UserID != 5 {
		t.Fatalf("readers under a policy that blocks another person: %+v", readers)
	}
	tg.resolved = &rpc.Chat{ID: -42, Type: "supergroup"}
	requests, err := d.chatRequests(context.Background(), params(t, rpc.JoinRequestsParams{Chat: "-42"}))
	if err != nil {
		t.Fatal(err)
	}
	got := requests.(rpc.JoinRequestsResult).Requests
	if len(got) != 1 || got[0].UserID != 5 {
		t.Fatalf("requests under a policy that blocks another person: %+v", got)
	}
}
