package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

func TestStatusAccessIsOptionalAndNeverShowsIDs(t *testing.T) {
	d, tg, _, path := accessTestDaemon(t)
	d.login = newLoginManager(context.Background(), tg)
	status := func() (rpc.StatusResult, string) {
		t.Helper()
		result, err := d.status(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return result.(rpc.StatusResult), string(data)
	}
	_, plain := status()
	want := fmt.Sprintf(`{"profile":"","pid":%d,"connected":true,"authorized":true,"user":{"id":42},"active_requests":0}`, os.Getpid())
	if plain != want {
		t.Fatalf("status without file changed: got %s, want %s", plain, want)
	}
	// 42 is the account's own id and appears in "user"; the ids in the lists must
	// be different, so that a leaked id cannot hide behind the user's one.
	writeAccess(t, path, `{"allow":[-1001234567890,987654321],"block":[987654321]}`)
	st, enabled := status()
	if st.Access == nil || !st.Access.Enabled || st.Access.Allow != 2 || st.Access.Block != 1 || st.Access.Closed {
		t.Errorf("access status = %+v", st.Access)
	}
	if strings.Contains(enabled, "1001234567890") || strings.Contains(enabled, "987654321") {
		t.Errorf("status leaked chat IDs: %s", enabled)
	}
	writeAccess(t, path, `{"admin":[987654321],"create":true}`)
	st, enabled = status()
	if st.Access == nil || st.Access.Admin != 1 || !st.Access.Create || strings.Contains(enabled, "987654321") {
		t.Errorf("admin status = %s", enabled)
	}
	writeAccess(t, path, `{"allow":["secret"]}`)
	st, invalid := status()
	if st.Access == nil || !st.Access.Enabled || !st.Access.Closed {
		t.Errorf("invalid access status = %+v", st.Access)
	}
	if strings.Contains(invalid, "secret") {
		t.Errorf("invalid status leaked file content: %s", invalid)
	}
}
