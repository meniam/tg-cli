package daemon

import (
	"encoding/json"
	"testing"
)

// audit05MustJSON marshals a fixture and fails the test on error, so a broken
// fixture is reported as such instead of surfacing as a confusing mismatch.
func audit05MustJSON(t testing.TB, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
