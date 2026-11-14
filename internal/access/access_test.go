package access

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPolicyRules(t *testing.T) {
	cases := []struct {
		name, file             string
		enabled, invite        bool
		allowCount, blockCount int
		visible                map[int64]bool
	}{
		{"empty file", "", false, true, 0, 0, map[int64]bool{42: true}},
		{"empty lists", `{"allow":[],"block":[]}`, false, true, 0, 0, map[int64]bool{42: true}},
		{"allow", `{"allow":[42,-77,-1001234567890]}`, true, false, 3, 0, map[int64]bool{42: true, -77: true, -1001234567890: true, 8: false}},
		{"block", `{"block":[-77]}`, true, true, 0, 1, map[int64]bool{42: true, -77: false}},
		{"block wins", `{"allow":[42,-77],"block":[-77]}`, true, false, 2, 1, map[int64]bool{42: true, -77: false, 8: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "access.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			policy, err := Open(path).Policy()
			if err != nil {
				t.Fatal(err)
			}
			if policy.Enabled() != tc.enabled || policy.InviteAllowed() != tc.invite || policy.AllowCount() != tc.allowCount || policy.BlockCount() != tc.blockCount {
				t.Errorf("policy status: enabled=%v invite=%v allow=%d block=%d", policy.Enabled(), policy.InviteAllowed(), policy.AllowCount(), policy.BlockCount())
			}
			for id, want := range tc.visible {
				if got := policy.Allowed(id); got != want {
					t.Errorf("Allowed(%d) = %v, want %v", id, got, want)
				}
			}
		})
	}
}

func TestMissingFileDisablesFilter(t *testing.T) {
	policy, err := Open(filepath.Join(t.TempDir(), "access.json")).Policy()
	if err != nil || policy.Enabled() || !policy.Allowed(42) || !policy.InviteAllowed() {
		t.Errorf("missing file: policy=%+v, err=%v", policy, err)
	}
}

func TestInvalidFileDeniesAllAndDoesNotLeakContent(t *testing.T) {
	cases := []string{
		`{"allow":[42,42]}`,
		`{"block":[0]}`,
		`{"allow":["secret"]}`,
		`{"allow":null}`,
		`{"allow":[1.5]}`,
		`{"allow":[9223372036854775808]}`,
		`{"secret":[]}`,
		`{"allow":[42],"block":`,
	}
	for i, file := range cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "access.json")
			if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
				t.Fatal(err)
			}
			policy, err := Open(path).Policy()
			if err == nil || !policy.Enabled() || policy.Allowed(42) || policy.Allowed(-77) || policy.InviteAllowed() {
				t.Fatalf("unsafe policy=%+v, err=%v", policy, err)
			}
			if !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "secret") {
				t.Errorf("unsafe error: %v", err)
			}
		})
	}
}

func TestCorrectionReloadsOnlyWhenMetadataChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	if err := os.WriteFile(path, []byte(`{"allow":[42]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := Open(path)
	reads := 0
	loader.readFile = func(path string) ([]byte, error) { reads++; return os.ReadFile(path) }
	for i := 0; i < 3; i++ {
		policy, err := loader.Policy()
		if err != nil || !policy.Allowed(42) || policy.Allowed(8) {
			t.Fatalf("first policy=%+v, err=%v", policy, err)
		}
	}
	if reads != 1 {
		t.Errorf("unchanged file read %d times", reads)
	}
	if err := os.WriteFile(path, []byte(`{"allow":[42],"block":[42]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := loader.Policy()
	if err != nil || policy.Allowed(42) || reads != 2 {
		t.Errorf("changed file: policy=%+v, reads=%d, err=%v", policy, reads, err)
	}
	if err := os.WriteFile(path, []byte(`{"allow":["secret"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err = loader.Policy()
	if err == nil || policy.Allowed(42) {
		t.Errorf("broken file: policy=%+v, err=%v", policy, err)
	}
	if err := os.WriteFile(path, []byte(`{"allow":[42,-77]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err = loader.Policy()
	if err != nil || !policy.Allowed(-77) {
		t.Errorf("corrected file: policy=%+v, err=%v", policy, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	policy, err = loader.Policy()
	if err != nil || policy.Enabled() || !policy.Allowed(8) {
		t.Errorf("removed file: policy=%+v, err=%v", policy, err)
	}
}

func TestUnsafePermissionsAndUnreadableFileDenyAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	if err := os.WriteFile(path, []byte(`{"allow":[42]}`), 0o622); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o622); err != nil {
		t.Fatal(err)
	}
	loader := Open(path)
	policy, err := loader.Policy()
	if err == nil || policy.Allowed(42) || policy.InviteAllowed() {
		t.Errorf("unsafe mode: policy=%+v err=%v", policy, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err = loader.Policy()
	if err != nil || !policy.Allowed(42) {
		t.Errorf("fixed mode: policy=%+v err=%v", policy, err)
	}
	loader.readFile = func(string) ([]byte, error) { return nil, errors.New("permission denied") }
	loader.cached = false
	policy, err = loader.Policy()
	if err == nil || policy.Allowed(42) || policy.InviteAllowed() {
		t.Errorf("unreadable: policy=%+v err=%v", policy, err)
	}
}

func TestConcurrentPolicyCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	if err := os.WriteFile(path, []byte(`{"block":[-77]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := Open(path)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				policy, err := loader.Policy()
				if err != nil || policy.Allowed(-77) || !policy.Allowed(42) {
					t.Errorf("policy=%+v err=%v", policy, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestFilterReturnsDetachedSortedLists(t *testing.T) {
	policy, err := parse([]byte(`{"allow":[42,-1001234567890],"block":[42,-77]}`))
	if err != nil {
		t.Fatal(err)
	}
	hide, allow, block := policy.Filter()
	if hide || len(allow) != 2 || allow[0] != -1001234567890 || len(block) != 2 || block[0] != -77 {
		t.Fatalf("filter = %v, %v, %v", hide, allow, block)
	}
	allow[0] = 999
	if !policy.Allowed(-1001234567890) {
		t.Fatal("filter mutated policy")
	}
	hide, _, _ = denyAll.Filter()
	if !hide {
		t.Fatal("denied policy must hide all")
	}
}
