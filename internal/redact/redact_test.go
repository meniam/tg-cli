package redact

import (
	"strings"
	"testing"
)

func TestTextSecrets(t *testing.T) {
	for _, secret := range []string{"+79991234567", "socks5://user:pass@", "password=hunter2", strings.Repeat("a", 32), strings.Repeat("z", 64)} {
		if got := Text(secret); strings.Contains(got, secret) {
			t.Errorf("not redacted: %q", secret)
		}
	}
	for _, s := range []string{"chat -1001234567", "message 12345", "2026-11-15"} {
		if got := Text(s); got != s {
			t.Errorf("%q changed to %q", s, got)
		}
	}
}

func TestAudit07_TextKeysAndSeparators(t *testing.T) {
	for in, want := range map[string]string{
		"api_hash: abc123":                    "[redacted]",
		"API_HASH=abc123":                     "[redacted]",
		"secret=abc":                          "[redacted]",
		"phone: 1":                            "[redacted]",
		"password = hunter2":                  "[redacted]",
		"login failed, password=hunter2; ok":  "login failed, [redacted]; ok",
		"dial socks5://user:pass@host failed": "dial [redacted]host failed",
		"call from +7 999 123-45-67 dropped":  "call from [redacted] dropped",
		"no secrets in this line":             "no secrets in this line",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAudit07_ValueRedactsNestedData(t *testing.T) {
	in := map[string]any{
		"password":          "hunter2",
		"Phone":             "+79991234567",
		"detail":            "retry with api_hash=abc",
		"count":             3,
		"flag":              true,
		"nothing":           nil,
		"nested":            map[string]any{"secret": "x", "note": "plain", "text": "password: y"},
		"list":              []any{"secret=z", 7, map[string]any{"api_hash": "q"}, []any{"phone: 1"}},
		"key password=oops": "value",
	}
	got, ok := Value(in).(map[string]any)
	if !ok {
		t.Fatalf("Value returned %T, want a map", Value(in))
	}
	nested, _ := got["nested"].(map[string]any)
	list, _ := got["list"].([]any)
	if len(list) != 4 {
		t.Fatalf("list = %v", got["list"])
	}
	inner, _ := list[2].(map[string]any)
	innerList, _ := list[3].([]any)
	checks := map[string][2]any{
		"password":           {got["password"], "[redacted]"},
		"Phone":              {got["Phone"], "[redacted]"},
		"detail":             {got["detail"], "retry with [redacted]"},
		"count":              {got["count"], 3},
		"flag":               {got["flag"], true},
		"nested.secret":      {nested["secret"], "[redacted]"},
		"nested.note":        {nested["note"], "plain"},
		"nested.text":        {nested["text"], "[redacted]"},
		"list[0]":            {list[0], "[redacted]"},
		"list[1]":            {list[1], 7},
		"list[2].api_hash":   {inner["api_hash"], "[redacted]"},
		"list[3][0]":         {innerList[0], "[redacted]"},
		"the key of a value": {got["key [redacted]"], "value"},
	}
	for name, pair := range checks {
		if pair[0] != pair[1] {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	if v, present := got["nothing"]; !present || v != nil {
		t.Errorf("a nil value must stay: %v %v", v, present)
	}
	if in["password"] != "hunter2" || in["nested"].(map[string]any)["secret"] != "x" {
		t.Error("Value changed its input")
	}
	if Value(42) != 42 || Value(nil) != nil || Value("password=1") != "[redacted]" {
		t.Error("scalar values are not handled")
	}
}
