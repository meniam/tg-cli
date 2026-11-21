package cli

import (
	"github.com/spf13/cobra"
	"testing"
)

func TestSettingsOverridesRecordsEnvAndFlags(t *testing.T) {
	env := map[string]string{"TG_CLI_THROTTLE_PERCENT": " 35 ", "TG_CLI_PROXY": "socks5://localhost:1080", "TG_CLI_EXPECT_USER": "@env"}
	a := &app{deps: Deps{Getenv: func(k string) string { return env[k] }}}
	cmd := &cobra.Command{}
	cmd.Flags().String("expect-user", "", "")
	cmd.Flags().String("transport", "tcp", "")
	if err := cmd.Flags().Set("expect-user", "@flag"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("transport", "wss"); err != nil {
		t.Fatal(err)
	}
	got, err := a.settingsOverrides(cmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, source, value string }{
		{"behavior.throttle_percent", "env", "35"}, {"profile.proxy", "env", `"socks5://localhost:1080"`},
		{"behavior.expect_user", "flag", `"@flag"`}, {"profile.transport", "flag", `"wss"`},
	} {
		v, ok := got[tc.key]
		if !ok || v.Source != tc.source || string(v.Value) != tc.value {
			t.Errorf("%s = %+v", tc.key, v)
		}
	}
}
