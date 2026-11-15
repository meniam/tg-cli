package access

import "testing"

func TestDownloadsPolicy(t *testing.T) {
	for _, tc := range []struct {
		input string
		ok    bool
	}{{`{"downloads":["/tmp/a"]}`, true}, {`{"downloads":["relative"]}`, false}, {`{"downloads":["/tmp/a","/tmp/a"]}`, false}, {`{"downloads":null}`, false}} {
		p, err := parse([]byte(tc.input))
		if (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.input, err)
		}
		if tc.ok && !p.DownloadsAllowed() {
			t.Fatal("downloads denied")
		}
	}
}

func TestDownloadsWithOtherPolicyFields(t *testing.T) {
	p, err := parse([]byte(`{"allow":[42],"block":[43],"write":[42],"admin":[42],"create":true,"profile":true,"files":["/tmp/source"],"downloads":["/tmp/target"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.DownloadsAllowed() || !p.FilesAllowed() || !p.ProfileAllowed() || !p.CreateAllowed() || !p.AdminAllowed(42) || !p.Allowed(42) || !p.WriteAllowed(42) || p.Allowed(43) {
		t.Fatalf("policy=%+v", p)
	}
	roots := p.DownloadRoots()
	roots[0] = "changed"
	if p.DownloadRoots()[0] != "/tmp/target" {
		t.Fatal("roots are not detached")
	}
	empty, err := parse([]byte(`{"downloads":[]}`))
	if err != nil || empty.DownloadsAllowed() {
		t.Fatalf("empty downloads: %+v %v", empty, err)
	}
}
