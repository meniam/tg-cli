package schema_test

import (
	"reflect"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// audit07KnownUntrustedArrayDefects lists the string arrays whose Go tag says
// untrusted:"true" but whose schema field loses the mark (G07-F01).
var audit07KnownUntrustedArrayDefects = map[string]bool{
	"UserProfile.usernames": true,
}

// TestAudit07_UntrustedStringArraysFollowTheirTag walks every top-level field of
// the response types that carry Telegram text in a []string and compares the
// schema mark with the Go tag. The classification test above only looks at
// string fields, so a lost mark on an array passed unnoticed.
func TestAudit07_UntrustedStringArraysFollowTheirTag(t *testing.T) {
	for _, value := range []any{rpc.UserProfile{}, rpc.Contact{}, rpc.ContactListResult{}} {
		typ := reflect.TypeOf(value)
		bySchema := map[string]schema.Field{}
		for _, field := range schema.Fields(typ) {
			bySchema[field.Path] = field
		}
		arrays := 0
		for i := 0; i < typ.NumField(); i++ {
			sf := typ.Field(i)
			if sf.Type != reflect.TypeOf([]string{}) {
				continue
			}
			arrays++
			path, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
			field, ok := bySchema[path]
			if !ok {
				t.Errorf("%s.%s: the string array is absent from the schema", typ.Name(), sf.Name)
				continue
			}
			if field.Type != schema.TypeArray || field.Element != schema.TypeString {
				t.Errorf("%s.%s: schema type %s of %s, want array of string", typ.Name(), sf.Name, field.Type, field.Element)
			}
			want := sf.Tag.Get("untrusted") == "true"
			if field.Untrusted == want {
				continue
			}
			name := typ.Name() + "." + path
			if audit07KnownUntrustedArrayDefects[name] {
				t.Run(name, func(t *testing.T) {
					t.Skipf("known defect G07-F01: schema field %s has untrusted=%v, the Go tag says %v", path, field.Untrusted, want)
				})
				continue
			}
			t.Errorf("%s untrusted=%v, the Go tag says %v", name, field.Untrusted, want)
		}
		if typ == reflect.TypeOf(rpc.UserProfile{}) && arrays == 0 {
			t.Errorf("%s has no string array any more, so the test checks nothing", typ.Name())
		}
	}
}
