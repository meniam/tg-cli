package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"tg-cli/internal/rpc"
)

func TestTranslateCommand(t *testing.T) {
	for _, format := range []string{"json", "jsonl"} {
		r := runDiscoveryCLI(t, []string{"message", "translate", "https://t.me/example/7", "--to", "EN", "--format", format}, func(method string, raw json.RawMessage) (any, error) {
			var p rpc.TranslateParams
			if err := json.Unmarshal(raw, &p); err != nil || method != rpc.MethodMessageTranslate || p.Chat != "@example" || !slices.Equal(p.IDs, []int{7}) || p.ToLang != "en" {
				return audit02Reject(t, "request: %s %+v %v", method, p, err)
			}
			return rpc.TranslateResult{ChatID: 42, ToLang: "en", Items: []rpc.TranslationItem{{ID: 7, Text: "hello"}}}, nil
		}, nil)
		if r.code != 0 || !strings.Contains(r.stdout, `"text":"hello"`) {
			t.Fatalf("%s: %+v", format, r)
		}
	}
	r := runDiscoveryCLI(t, []string{"message", "translate", "42", "7"}, nil, nil)
	if r.code != 2 || r.method != "" {
		t.Fatalf("missing --to: %+v", r)
	}
}
