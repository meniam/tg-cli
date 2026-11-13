package exitcode

import "testing"

func TestCodesAreUniqueAndNamed(t *testing.T) {
	seenName := map[string]int{}
	for _, c := range All() {
		n := Name(c)
		if n == "unknown" {
			t.Errorf("code %d has no name", c)
		}
		if prev, dup := seenName[n]; dup {
			t.Errorf("name %q used by codes %d and %d", n, prev, c)
		}
		seenName[n] = c
	}
	if len(seenName) != len(names) {
		t.Errorf("All() returned %d codes, expected %d", len(seenName), len(names))
	}
}

func TestStableValues(t *testing.T) {
	want := map[int]string{
		0: "ok", 1: "failure", 2: "usage", 3: "daemon_not_running",
		4: "not_authorized", 5: "telegram_error", 6: "flood_wait", 7: "not_found",
	}
	for code, name := range want {
		if got := Name(code); got != name {
			t.Errorf("Name(%d) = %q, want %q", code, got, name)
		}
	}
	if Name(99) != "unknown" {
		t.Error("undefined code must be unknown")
	}
}
