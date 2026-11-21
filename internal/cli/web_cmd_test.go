package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/web"
)

func TestWebCommandStartsAndStops(t *testing.T) {
	e := loggedInEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var stdout, stderr safeBuffer
	done := make(chan int, 1)
	go func() { done <- Execute(ctx, []string{"web"}, e.deps(&stdout, &stderr)) }()
	settled := false
	t.Cleanup(func() {
		cancel()
		if settled {
			return
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("tg web did not stop")
		}
	})

	var envelope struct {
		OK   bool       `json:"ok"`
		Data web.Result `json:"data"`
	}
	for {
		if err := json.Unmarshal([]byte(stdout.String()), &envelope); err == nil {
			if !envelope.OK {
				t.Fatal("tg web did not return a startup URL")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("web startup did not print an envelope")
		case <-time.After(10 * time.Millisecond):
		}
	}
	address, err := url.Parse(envelope.Data.URL)
	if err != nil || address.Hostname() != "127.0.0.1" || address.Host != envelope.Data.Listen || envelope.Data.Profile != "default" {
		t.Fatal("web startup envelope has an invalid loopback URL, address or profile")
	}
	token, ok := strings.CutPrefix(address.Fragment, "token=")
	if !ok || len(token) != 43 {
		t.Fatal("web startup URL did not provide a 32-byte token")
	}
	origin := address.Scheme + "://" + address.Host
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/chat.overview", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer could not use daemon socket: HTTP %d", res.StatusCode)
	}
	cancel()
	select {
	case code := <-done:
		settled = true
		if code != exitcode.OK {
			t.Fatalf("web shutdown exit = %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("web server did not shut down")
	}
}
