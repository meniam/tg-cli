package access

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFromSettingsMatchesFilePolicy(t *testing.T) {
	cases := []string{
		`{}`, `{"allow":[],"block":[]}`, `{"allow":[42,-77,-1001234567890]}`,
		`{"block":[-77]}`, `{"allow":[42,-77],"block":[-77]}`,
		`{"write":[42,-77],"admin":[42],"allow":[42],"block":[-77]}`,
		`{"files":["/tmp/a/."],"downloads":["/tmp/b"],"dialogs":true,"sessions":true,"create":true,"profile":true}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			want, err := parse([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				t.Fatal(err)
			}
			settings := map[string]json.RawMessage{"behavior.expect_user": []byte(`"test"`)}
			for key, v := range fields {
				settings["access."+key] = v
			}
			got, err := FromSettings(settings)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("policies differ: got %+v want %+v", got, want)
			}
			for _, id := range []int64{42, -77, -1001234567890, 8} {
				if got.Allowed(id) != want.Allowed(id) || got.WriteAllowed(id) != want.WriteAllowed(id) || got.AdminAllowed(id) != want.AdminAllowed(id) {
					t.Errorf("different permission for %d", id)
				}
			}
		})
	}
}

func TestFromSettingsInvalidDeniesAll(t *testing.T) {
	for key, values := range map[string][]string{
		"allow": {`null`, `[0]`, `[42,42]`, `["secret"]`, `[1.5]`, `[9223372036854775808]`},
		"block": {`[0]`}, "write": {`null`, `[0]`, `[42,42]`}, "admin": {`[0]`},
		"files":     {`null`, `[null]`, `[42]`, `[""]`, `["relative"]`, `["/tmp/a","/tmp/a/."]`},
		"downloads": {`null`, `["relative"]`}, "dialogs": {`null`}, "sessions": {`1`}, "create": {`null`}, "profile": {`1`}, "unknown": {`[]`},
	} {
		for _, raw := range values {
			t.Run(key+raw, func(t *testing.T) {
				p, err := FromSettings(map[string]json.RawMessage{"access." + key: []byte(raw)})
				if err == nil || !p.Closed() || p.Allowed(42) || p.InviteAllowed() || p.WriteAllowed(42) || p.AdminAllowed(42) || p.FilesAllowed() || p.DownloadsAllowed() || p.DialogsAllowed() || p.SessionsAllowed() || p.CreateAllowed() || p.ProfileAllowed() {
					t.Fatalf("invalid policy accepted: %+v, %v", p, err)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("error leaks value")
				}
			})
		}
	}
}

// parseCompared keeps every file-policy assertion and checks the equivalent
// stored fields whenever the input is a valid JSON object.
func parseCompared(t *testing.T, raw []byte) (Policy, error) {
	t.Helper()
	policy, err := parse(raw)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return policy, err
	}
	values := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		values["access."+key] = value
	}
	actual, fromErr := FromSettings(values)
	if err != nil {
		if fromErr == nil || !actual.Closed() {
			t.Errorf("invalid stored policy accepted: %v", fromErr)
		}
	} else if fromErr != nil || !reflect.DeepEqual(policy, actual) {
		t.Errorf("file and stored policies differ: %v", fromErr)
	}
	return policy, err
}
