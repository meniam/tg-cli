package access

import "testing"

func TestProfilePolicy(t *testing.T) {
	for _, tc := range []struct {
		input   string
		allowed bool
		invalid bool
	}{
		{`{}`, false, false},
		{`{"profile":false}`, false, false},
		{`{"profile":true}`, true, false},
		{`{"profile":null}`, false, true},
		{`{"profile":1}`, false, true},
	} {
		p, err := parse([]byte(tc.input))
		if (err != nil) != tc.invalid || p.ProfileAllowed() != tc.allowed {
			t.Errorf("parse(%s): allowed=%v, err=%v", tc.input, p.ProfileAllowed(), err)
		}
	}
	if denyAll.ProfileAllowed() {
		t.Fatal("closed policy granted profile access")
	}
}
