package clientpreset

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// fixture mimics the structure of opentele api.py with made-up values.
const fixture = `
class API(BaseObject):
    """
        api_id (int) : 9999
        api_hash (str) : "docstring-values-must-be-ignored"
    """

    class TelegramDesktop(APIData):
        """
        api_id = 1111
        """

        api_id = 11111
        api_hash = "0123456789abcdef0123456789abcdef"
        device_model = "Desktop"

    class TelegramIOS(APIData):
        # api_id           = 8
        # api_hash         = "ffffffffffffffffffffffffffffffff"
        api_id = 22222
        api_hash = "11111111111111111111111111111111"

    class TelegramWeb_K(APIData):
        api_id = 33333
        api_hash = "22222222222222222222222222222222"

    class Testy(APIData):
        api_id = 17349
        api_hash = "33333333333333333333333333333333"

    class Huge(APIData):
        api_id = 2000000
        api_hash = "44444444444444444444444444444444"

    class NoHash(APIData):
        api_id = 44444
`

func TestParse(t *testing.T) {
	got, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Credentials{
		"desktop": {11111, "0123456789abcdef0123456789abcdef"},
		"ios":     {22222, "11111111111111111111111111111111"},
		"web-k":   {33333, "22222222222222222222222222222222"},
	}
	if len(got) != len(want) {
		t.Errorf("got %d presets %v, want %d", len(got), got, len(want))
	}
	for name, c := range want {
		if got[name] != c {
			t.Errorf("preset %s: got %+v, want %+v", name, got[name], c)
		}
	}
}

func TestParseRejectsContentWithoutPresets(t *testing.T) {
	if _, err := Parse([]byte("nothing useful here")); err == nil {
		t.Error("content without presets must be an error")
	}
}

func TestResolve(t *testing.T) {
	c, err := Resolve([]byte(fixture), "ios")
	if err != nil || c.AppID != 22222 {
		t.Fatalf("Resolve(ios): %+v, %v", c, err)
	}
	_, err = Resolve([]byte(fixture), "nope")
	if err == nil || !strings.Contains(err.Error(), "desktop, ios, web-k") {
		t.Fatalf("an unknown preset must list the available ones, got %v", err)
	}
	if strings.Contains(err.Error(), "0123456789abcdef") {
		t.Error("an error must not contain hashes")
	}
}

func TestFetchChecksumAndHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()
	// Fetch always asks SourceURL, so route it to the test server.
	hc := &http.Client{Transport: rewrite{target: srv.URL}}
	if _, err := Fetch(context.Background(), hc); err == nil || !strings.Contains(err.Error(), "pinned checksum") {
		t.Errorf("a file with a wrong checksum must be rejected, got %v", err)
	}

	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	hc = &http.Client{Transport: rewrite{target: notFound.URL}}
	if _, err := Fetch(context.Background(), hc); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("an HTTP error must be reported, got %v", err)
	}
}

type rewrite struct{ target string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = "http", strings.TrimPrefix(r.target, "http://")
	req2.URL = &u
	return http.DefaultTransport.RoundTrip(req2)
}

// TestRealFile checks the parser and the pinned checksum against the real
// file. Set TG_PRESET_FILE to a local copy of SourceURL to run it.
func TestRealFile(t *testing.T) {
	path := os.Getenv("TG_PRESET_FILE")
	if path == "" {
		t.Skip("TG_PRESET_FILE is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(data); err != nil {
		t.Fatal(err)
	}
	presets, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"desktop", "android", "ios", "macos", "web-z", "web-k"} {
		if _, ok := presets[name]; !ok {
			t.Errorf("preset %s is missing in the real file", name)
		}
	}
}

// TestFetchLive downloads the real file. Set TG_PRESET_LIVE=1 to run it.
func TestFetchLive(t *testing.T) {
	if os.Getenv("TG_PRESET_LIVE") == "" {
		t.Skip("TG_PRESET_LIVE is not set")
	}
	data, err := Fetch(context.Background(), &http.Client{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(data, "desktop"); err != nil {
		t.Fatal(err)
	}
}
