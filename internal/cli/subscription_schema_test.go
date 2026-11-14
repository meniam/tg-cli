package cli

import (
	"encoding/json"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestSchemaDescribesRPCSubscriptionWithoutDaemon(t *testing.T) {
	e := newEnv(t)
	r := e.run("schema")
	if r.code != exitcode.OK {
		t.Fatalf("schema exit %d: %s", r.code, r.stdout)
	}
	var doc schemaDoc
	if err := json.Unmarshal(parse(t, r).Data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Subscription.Method != rpc.MethodSubscribe || doc.Subscription.Notification != rpc.MethodEvent {
		t.Errorf("subscription methods: %+v", doc.Subscription)
	}
	if len(doc.Subscription.Result) != 2 {
		t.Errorf("subscribe result fields: %+v", doc.Subscription.Result)
	}
	var eventDataMarked bool
	for _, field := range doc.Subscription.Event {
		if field.Path == "data" && field.Untrusted {
			eventDataMarked = true
		}
	}
	if !eventDataMarked {
		t.Errorf("event data not marked untrusted: %+v", doc.Subscription.Event)
	}
}
