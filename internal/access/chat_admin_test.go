package access

import "testing"

func TestAdminPolicy(t *testing.T) {
	p, err := parseCompared(t, []byte(`{"allow":[1,2],"block":[2],"write":[1],"admin":[2,3],"create":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.AdminAllowed(1) || p.AdminAllowed(2) || p.AdminAllowed(3) || p.AdminCount() != 2 || !p.CreateAllowed() {
		t.Fatal("invalid admin policy")
	}
	p, err = parseCompared(t, []byte(`{"admin":[1],"create":true}`))
	if err != nil || !p.AdminAllowed(1) || p.WriteAllowed(1) || !p.CreateAllowed() {
		t.Fatal("independent admin permission", err)
	}
	for _, input := range []string{`{"admin":[0]}`, `{"admin":[1,1]}`, `{"admin":null}`, `{"create":null}`, `{"create":1}`} {
		if _, err := parseCompared(t, []byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	if denyAll.CreateAllowed() || denyAll.AdminAllowed(1) {
		t.Fatal("closed policy granted access")
	}
}
