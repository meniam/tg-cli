package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"tg-cli/internal/schema"

	"tg-cli/internal/rpc"
)

func TestStatsAndBoostsCommands(t *testing.T) {
	r := runDiscoveryCLI(t, []string{"chat", "stats", "42", "--graph", "growth"}, func(method string, raw json.RawMessage) (any, error) {
		var p rpc.ChatStatsParams
		if err := json.Unmarshal(raw, &p); err != nil || method != rpc.MethodChatStats || p.Chat != "42" || p.Graph != "growth" {
			return audit02Reject(t, "stats request: %s %+v %v", method, p, err)
		}
		return rpc.ChatStats{ChatID: 42, Name: "growth", Data: json.RawMessage(`{"columns":[]}`)}, nil
	}, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"name":"growth"`) {
		t.Fatalf("stats: %+v", r)
	}
	r = runDiscoveryCLI(t, []string{"chat", "boosts", "42"}, func(method string, _ json.RawMessage) (any, error) {
		if method != rpc.MethodChatBoosts {
			return audit02Reject(t, "method: %s", method)
		}
		return rpc.ChatBoosts{ChatID: 42, Level: 2}, nil
	}, nil)
	if r.code != 0 || !strings.Contains(r.stdout, `"level":2`) {
		t.Fatalf("boosts: %+v", r)
	}
}

// audit02Filled sets every exported field of the struct v points to, recursively,
// so that the response shows all of its paths. A response of the fixture
// Telegram is sparse; this one is not.
func audit02Filled(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		audit02Filled(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				audit02Filled(v.Field(i))
			}
		}
	case reflect.Slice:
		if v.Type() == reflect.TypeOf(json.RawMessage(nil)) {
			v.Set(reflect.ValueOf(json.RawMessage(`1`)))
			return
		}
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		audit02Filled(v.Index(0))
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	}
}

// TestAudit02_ChatStatsAndBoostsMatchSchema compares the responses with the
// schema on fully filled results: the fixture Telegram returns a sparse object
// of two or three fields for these commands, which leaves most of the schema
// unchecked.
func TestAudit02_ChatStatsAndBoostsMatchSchema(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		result  any
	}{
		{"chat stats", []string{"chat", "stats", "42"}, &rpc.ChatStats{}},
		{"chat boosts", []string{"chat", "boosts", "42"}, &rpc.ChatBoosts{}},
	} {
		audit02Filled(reflect.ValueOf(tc.result).Elem())
		r := runDiscoveryCLI(t, tc.args, func(string, json.RawMessage) (any, error) { return tc.result, nil }, nil)
		if r.code != 0 {
			t.Fatalf("%s: %+v", tc.command, r)
		}
		ok, data := audit02Envelope(t, r.stdout)
		if !ok {
			t.Fatalf("%s: %s", tc.command, r.stdout)
		}
		audit02CompareWithSchema(t, tc.command, data)
		paths, _ := schema.Paths(data)
		if want := len(schemaCommands(t)[tc.command].Fields); len(paths) != want {
			t.Errorf("%s: the filled response has %d paths, the schema %d: %v", tc.command, len(paths), want, paths)
		}
	}
}
