package clientpreset

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A body one byte over the limit is refused before the checksum is looked at.
func TestAudit03FetchRefusesAnOversizedFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxSourceSize+1)))
	}))
	defer srv.Close()
	hc := &http.Client{Transport: rewrite{target: srv.URL}}
	if _, err := Fetch(context.Background(), hc); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("an oversized file must be refused as too large, got %v", err)
	}
}

// A class missing from the name table is still offered, under its lower-case
// class name.
func TestAudit03ParseFallsBackToTheLowerCaseClassName(t *testing.T) {
	const content = `class TelegramDesktop(APIData):
    api_id   = 111
    api_hash = "0123456789abcdef0123456789abcdef"

class SomethingNew(APIData):
    api_id   = 222
    api_hash = "fedcba9876543210fedcba9876543210"
`
	got, err := Parse([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := got["somethingnew"]; !ok || c.AppID != 222 {
		t.Fatalf("presets = %v, want somethingnew with id 222", got)
	}
}
