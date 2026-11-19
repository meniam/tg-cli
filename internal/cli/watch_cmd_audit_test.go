package cli

import (
	"context"
	"testing"
)

// The context is cancelled as soon as the daemon has accepted the subscription
// (the "watching from cursor" notice goes to stderr), before any event.
// A cancelled json watch must still print the success envelope.
func TestAudit03WatchJSONContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := audit03RunWatch(t, ctx, []string{"message", "watch", "--format", "json", "--max-events", "5"}, nil, nil, false, nil, &cancelWriter{after: cancel})
	if r.code != 0 || r.stdout != "{\"ok\":true,\"data\":{\"events\":[]}}\n" {
		t.Fatalf("cancelled json watch: %+v", r)
	}
}
