package access

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func audit01Write(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "access.json")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile is subject to the umask, so set the exact mode explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// A broken or unsafe file that has not changed must keep denying access on every
// call, not only on the first one.
func TestAudit01_FailureIsCachedAndStillDenies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		mode    os.FileMode
		reads   int
	}{
		{"malformed JSON", `{"allow":[42],"block":`, 0o600, 1},
		{"writable by others", `{"allow":[42]}`, 0o666, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := Open(audit01Write(t, tc.content, tc.mode))
			reads := 0
			loader.readFile = func(path string) ([]byte, error) { reads++; return os.ReadFile(path) }
			var first string
			for call := 1; call <= 3; call++ {
				policy, err := loader.Policy()
				if err == nil {
					t.Fatalf("call %d: the failure was lost, policy=%+v", call, policy)
				}
				if !policy.Closed() || policy.Allowed(42) || policy.InviteAllowed() {
					t.Fatalf("call %d: cached policy is not closed: %+v", call, policy)
				}
				if call == 1 {
					first = err.Error()
				} else if err.Error() != first {
					t.Fatalf("call %d: error changed from %q to %q", call, first, err)
				}
			}
			if reads != tc.reads {
				t.Fatalf("file read %d times, want %d", reads, tc.reads)
			}
		})
	}
}

func TestAudit01_StatErrorOtherThanMissingDeniesAll(t *testing.T) {
	file := audit01Write(t, `{"allow":[42]}`, 0o600)
	// A path below a regular file fails with ENOTDIR, not with ErrNotExist.
	policy, err := Open(filepath.Join(file, "inner.json")).Policy()
	if err == nil || !strings.Contains(err.Error(), "stat access file") {
		t.Fatalf("error = %v", err)
	}
	if !policy.Closed() || policy.Allowed(42) || policy.InviteAllowed() {
		t.Fatalf("policy is not closed: %+v", policy)
	}
}

func TestAudit01_NonObjectRootDeniesAll(t *testing.T) {
	for _, content := range []string{`[1]`, `"x"`, `42`, `null`, `true`} {
		t.Run(content, func(t *testing.T) {
			policy, err := Open(audit01Write(t, content, 0o600)).Policy()
			if err == nil || !strings.Contains(err.Error(), "expected a JSON object") {
				t.Fatalf("error = %v", err)
			}
			if !policy.Closed() || policy.Allowed(42) {
				t.Fatalf("policy is not closed: %+v", policy)
			}
		})
	}
}

func TestAudit01_ClosedAndDownloadRoots(t *testing.T) {
	good, err := Open(audit01Write(t, `{"downloads":["/tmp/target"]}`, 0o600)).Policy()
	if err != nil || good.Closed() || len(good.DownloadRoots()) != 1 {
		t.Fatalf("good policy: %+v %v", good, err)
	}
	missing, err := Open(filepath.Join(t.TempDir(), "none.json")).Policy()
	if err != nil || missing.Closed() {
		t.Fatalf("missing file must not close access: %+v %v", missing, err)
	}
	bad, err := Open(audit01Write(t, `{`, 0o600)).Policy()
	if err == nil || !bad.Closed() || bad.DownloadRoots() != nil || bad.DownloadsAllowed() {
		t.Fatalf("denied policy keeps download roots: %+v %v", bad, err)
	}
}
