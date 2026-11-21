package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// secret stands for the text of a private message. No error may contain it.
const secret = "SECRET-MESSAGE-TEXT"

// recorded is what the fake service saw in one /api/embed request.
type recorded struct {
	Model     string       `json:"model"`
	Input     []string     `json:"input"`
	Options   embedOptions `json:"options"`
	KeepAlive string       `json:"keep_alive"`
}

// fakeOllama is an httptest server with a replaceable /api/embed handler and a
// fixed /api/tags answer. embedFn gets the decoded request and writes the answer.
type fakeOllama struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []recorded
	tags string
}

func newFake(t *testing.T, embedFn func(w http.ResponseWriter, r *http.Request, req recorded)) *fakeOllama {
	t.Helper()
	f := &fakeOllama{tags: `{"models":[]}`}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		var req recorded
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		f.mu.Unlock()
		embedFn(w, r, req)
	})
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		body := f.tags
		f.mu.Unlock()
		fmt.Fprint(w, body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOllama) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeOllama) client(t *testing.T, o Options) *Client {
	t.Helper()
	o.BaseURL = f.srv.URL
	if o.Model == "" {
		o.Model = "bge-m3"
	}
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// lengthVectors answers every text with the vector {len(text), 1}, so a test can
// tell which text a vector belongs to without any real model.
func lengthVectors(w http.ResponseWriter, _ *http.Request, req recorded) {
	vectors := make([][]float32, len(req.Input))
	for i, text := range req.Input {
		vectors[i] = []float32{float32(len(text)), 1}
	}
	writeEmbeddings(w, vectors)
}

func writeEmbeddings(w http.ResponseWriter, vectors [][]float32) {
	_ = json.NewEncoder(w).Encode(map[string]any{"model": "bge-m3", "embeddings": vectors})
}

func TestEmbedBatchesKeepOrderAndSendTheRequestShape(t *testing.T) {
	f := newFake(t, lengthVectors)
	c := f.client(t, Options{Batch: 2})
	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}
	got, err := c.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(texts) {
		t.Fatalf("%d vectors for %d texts", len(got), len(texts))
	}
	for i, v := range got {
		if v[0] != float32(len(texts[i])) {
			t.Fatalf("vector %d belongs to another text: %v", i, v)
		}
	}
	reqs := f.requests()
	var sizes []int
	for _, r := range reqs {
		sizes = append(sizes, len(r.Input))
		if r.Model != "bge-m3" || r.Options.NumCtx != 8192 || r.KeepAlive != "30m" {
			t.Fatalf("request shape: %+v", r)
		}
	}
	if fmt.Sprint(sizes) != "[2 2 1]" {
		t.Fatalf("batch sizes = %v, want [2 2 1]", sizes)
	}
}

func TestEmbedSplitsBatchesByTotalLength(t *testing.T) {
	f := newFake(t, lengthVectors)
	c := f.client(t, Options{Batch: 16})
	// The first two fit under 24 000 characters together, the third does not. A
	// text above the limit on its own still goes out, alone. Lengths differ so a
	// vector says which text it belongs to.
	texts := []string{strings.Repeat("x", 10000), strings.Repeat("x", 10001), strings.Repeat("x", 10002), strings.Repeat("x", 30000), "y"}
	got, err := c.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if v[0] != float32(len(texts[i])) {
			t.Fatalf("vector %d belongs to another text: %v", i, v)
		}
	}
	var sizes []int
	for _, r := range f.requests() {
		sizes = append(sizes, len(r.Input))
	}
	if fmt.Sprint(sizes) != "[2 1 1 1]" {
		t.Fatalf("batch sizes = %v, want [2 1 1 1]", sizes)
	}
}

func TestSplitBatchesCountsCharactersNotBytes(t *testing.T) {
	// 20 000 Cyrillic letters are 40 000 bytes but 20 000 characters: two such
	// texts must not fit together (40 000 > 24 000), one and a short one must.
	long := strings.Repeat("ж", 20000)
	if got := splitBatches([]string{long, "я"}, 16); len(got) != 1 {
		t.Fatalf("%d batches, want 1: characters, not bytes, are counted", len(got))
	}
	if got := splitBatches([]string{long, long}, 16); len(got) != 2 {
		t.Fatalf("%d batches, want 2", len(got))
	}
}

func TestEmbedWithoutTextsMakesNoRequest(t *testing.T) {
	f := newFake(t, lengthVectors)
	got, err := f.client(t, Options{}).Embed(context.Background(), nil)
	if err != nil || got != nil || len(f.requests()) != 0 {
		t.Fatalf("got %v, err %v, %d requests", got, err, len(f.requests()))
	}
}

func TestEmbedRejectsABadAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"extra vector", `{"embeddings":[[1,2],[3,4],[5,6]]}`},
		{"missing vector", `{"embeddings":[[1,2]]}`},
		{"no vectors at all", `{}`},
		{"sizes differ inside an answer", `{"embeddings":[[1,2],[3]]}`},
		{"empty vector", `{"embeddings":[[],[]]}`},
		{"not json", `<html>proxy error</html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) { fmt.Fprint(w, tc.body) })
			_, err := f.client(t, Options{}).Embed(context.Background(), []string{"one", "two"})
			if !errors.Is(err, ErrBadResponse) {
				t.Fatalf("error = %v, want ErrBadResponse", err)
			}
		})
	}
}

func TestEmbedRejectsADimensionThatChangesBetweenBatches(t *testing.T) {
	calls := 0
	f := newFake(t, func(w http.ResponseWriter, _ *http.Request, req recorded) {
		calls++
		size := 2
		if calls == 2 {
			size = 3
		}
		vectors := make([][]float32, len(req.Input))
		for i := range vectors {
			vectors[i] = make([]float32, size)
		}
		writeEmbeddings(w, vectors)
	})
	_, err := f.client(t, Options{Batch: 1}).Embed(context.Background(), []string{"a", "b"})
	if !errors.Is(err, ErrBadResponse) {
		t.Fatalf("error = %v, want ErrBadResponse", err)
	}
}

func TestEmbedClassifiesErrorStatuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"404 without a body", http.StatusNotFound, "", func(err error) bool { return errors.Is(err, ErrModelMissing) }},
		{"404 json", http.StatusNotFound, `{"error":"model \"x\" not found, try pulling it first"}`, func(err error) bool { return errors.Is(err, ErrModelMissing) }},
		{"not found text with another status", http.StatusInternalServerError, `{"error":"model not found"}`, func(err error) bool { return errors.Is(err, ErrModelMissing) }},
		{"500", http.StatusInternalServerError, `{"error":"boom"}`, func(err error) bool {
			var se *StatusError
			return errors.As(err, &se) && se.Code == 500 && !errors.Is(err, ErrModelMissing)
		}},
		{"400 echoing the input", http.StatusBadRequest, `{"error":"bad input ` + secret + `"}`, func(err error) bool {
			var se *StatusError
			return errors.As(err, &se) && se.Code == 400 && !strings.Contains(err.Error(), secret)
		}},
		{"redirect is not followed", http.StatusFound, "", func(err error) bool {
			var se *StatusError
			return errors.As(err, &se) && se.Code == 302
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
				if tc.status == http.StatusFound {
					w.Header().Set("Location", "http://example.com/steal")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			_, err := f.client(t, Options{}).Embed(context.Background(), []string{secret})
			if err == nil || !tc.check(err) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("the error contains the message text: %v", err)
			}
		})
	}
}

func TestEmbedReportsAClosedServerAsUnavailable(t *testing.T) {
	f := newFake(t, lengthVectors)
	c := f.client(t, Options{})
	f.srv.Close()
	_, err := c.Embed(context.Background(), []string{secret})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the error contains the message text: %v", err)
	}
}

// blockingFake answers nothing until the test ends or the client gives up.
func blockingFake(t *testing.T, started chan<- struct{}) *fakeOllama {
	t.Helper()
	release := make(chan struct{})
	f := newFake(t, func(_ http.ResponseWriter, r *http.Request, _ recorded) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	// Cleanups run last in, first out: release the handler before Close waits for it.
	t.Cleanup(func() { close(release) })
	return f
}

func TestEmbedTimeoutIsUnavailable(t *testing.T) {
	f := blockingFake(t, nil)
	_, err := f.client(t, Options{Timeout: 50 * time.Millisecond}).Embed(context.Background(), []string{"a"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("a request timeout is not a cancellation: %v", err)
	}
}

func TestEmbedStopsOnCancelledContext(t *testing.T) {
	started := make(chan struct{}, 1)
	f := blockingFake(t, started)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.client(t, Options{Timeout: time.Minute}).Embed(ctx, []string{"a"})
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want context.Canceled and not ErrUnavailable", err)
	}
}

func TestEmbedRefusesAnAlreadyCancelledContext(t *testing.T) {
	f := newFake(t, lengthVectors)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.client(t, Options{}).Embed(ctx, []string{"a"})
	if !errors.Is(err, context.Canceled) || len(f.requests()) != 0 {
		t.Fatalf("error = %v, %d requests", err, len(f.requests()))
	}
}

func TestInfoFindsTheDigestWithAndWithoutLatest(t *testing.T) {
	tags := `{"models":[
		{"name":"bge-m3:567m","model":"bge-m3:567m","digest":"sha256:other"},
		{"name":"bge-m3:latest","model":"bge-m3:latest","digest":"sha256:abc123"},
		{"name":"embeddinggemma:latest","model":"embeddinggemma:latest","digest":"sha256:gemma"}]}`
	for _, tc := range []struct {
		model, wantName, wantDigest string
	}{
		{"bge-m3", "bge-m3:latest", "sha256:abc123"},
		{"bge-m3:latest", "bge-m3:latest", "sha256:abc123"},
		{"bge-m3:567m", "bge-m3:567m", "sha256:other"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			f := newFake(t, func(w http.ResponseWriter, _ *http.Request, req recorded) {
				writeEmbeddings(w, [][]float32{make([]float32, 1024)})
			})
			f.tags = tags
			info, err := f.client(t, Options{Model: tc.model}).Info(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if info != (ModelInfo{Name: tc.wantName, Digest: tc.wantDigest, Dim: 1024}) {
				t.Fatalf("info = %+v", info)
			}
		})
	}
}

func TestInfoErrors(t *testing.T) {
	probe := func(w http.ResponseWriter, _ *http.Request, _ recorded) { writeEmbeddings(w, [][]float32{{1, 2}}) }
	t.Run("model is not installed", func(t *testing.T) {
		f := newFake(t, probe)
		f.tags = `{"models":[{"name":"other:latest","digest":"sha256:x"}]}`
		if _, err := f.client(t, Options{}).Info(context.Background()); !errors.Is(err, ErrModelMissing) {
			t.Fatalf("error = %v, want ErrModelMissing", err)
		}
	})
	t.Run("an untagged name does not match another tag", func(t *testing.T) {
		f := newFake(t, probe)
		f.tags = `{"models":[{"name":"bge-m3:567m","digest":"sha256:x"}]}`
		if _, err := f.client(t, Options{}).Info(context.Background()); !errors.Is(err, ErrModelMissing) {
			t.Fatalf("error = %v, want ErrModelMissing", err)
		}
	})
	t.Run("no digest", func(t *testing.T) {
		f := newFake(t, probe)
		f.tags = `{"models":[{"name":"bge-m3:latest"}]}`
		if _, err := f.client(t, Options{}).Info(context.Background()); !errors.Is(err, ErrBadResponse) {
			t.Fatalf("error = %v, want ErrBadResponse", err)
		}
	})
	t.Run("tags is not json", func(t *testing.T) {
		f := newFake(t, probe)
		f.tags = `nope`
		if _, err := f.client(t, Options{}).Info(context.Background()); !errors.Is(err, ErrBadResponse) {
			t.Fatalf("error = %v, want ErrBadResponse", err)
		}
	})
	t.Run("service is closed", func(t *testing.T) {
		f := newFake(t, probe)
		c := f.client(t, Options{})
		f.srv.Close()
		if _, err := c.Info(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v, want ErrUnavailable", err)
		}
	})
}

func TestNewAcceptsOnlyLoopbackAddresses(t *testing.T) {
	for _, ok := range []string{
		"http://127.0.0.1:11434", "http://127.0.0.1", "https://localhost:11434", "http://LOCALHOST:11434/", "http://[::1]:11434",
	} {
		if _, err := New(Options{BaseURL: ok, Model: "bge-m3"}); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "127.0.0.1:11434", "http://", "http://example.com:11434", "http://192.168.1.5:11434", "http://10.0.0.1",
		"http://0.0.0.0:11434", "http://127.0.0.2:11434", "http://127.0.0.1.evil.com", "http://localhost.evil.com",
		"http://[::ffff:127.0.0.1]:11434", "ftp://127.0.0.1", "ws://127.0.0.1",
		"http://user:pass@127.0.0.1:11434", "http://127.0.0.1:11434/api", "http://127.0.0.1:11434?x=1", "http://127.0.0.1:11434#x",
		"http://127.0.0.1:99999999999999999999",
	} {
		if _, err := New(Options{BaseURL: bad, Model: "bge-m3"}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNewRejectsBadOptions(t *testing.T) {
	base := "http://127.0.0.1:11434"
	for name, o := range map[string]Options{
		"empty model":      {BaseURL: base, Model: " "},
		"negative batch":   {BaseURL: base, Model: "m", Batch: -1},
		"negative timeout": {BaseURL: base, Model: "m", Timeout: -time.Second},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	c, err := New(Options{BaseURL: base, Model: "m"})
	if err != nil || c.batch != DefaultBatch || c.timeout != DefaultTimeout {
		t.Fatalf("defaults: %+v, %v", c, err)
	}
}

func TestNewDoesNotChangeTheCallersHTTPClient(t *testing.T) {
	h := &http.Client{}
	if _, err := New(Options{BaseURL: "http://127.0.0.1:11434", Model: "m", HTTP: h}); err != nil {
		t.Fatal(err)
	}
	if h.CheckRedirect != nil {
		t.Fatal("New changed the redirect rule of the caller's client")
	}
}

func TestEmbedContextLengthFallback(t *testing.T) {
	long := strings.Repeat("я🙂", 150)
	for _, tc := range []struct {
		name  string
		texts []string
		want  []string
	}{
		{"batch", []string{"a", "bb", long, "ccc", "dddd"}, []string{"a", "bb", string([]rune(long)[:150]), "ccc", "dddd"}},
		{"single", []string{long}, []string{string([]rune(long)[:150])}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, func(w http.ResponseWriter, r *http.Request, req recorded) {
				for _, s := range req.Input {
					if !utf8.ValidString(s) {
						t.Error("invalid UTF-8")
					}
					if utf8.RuneCountInString(s) > 160 {
						w.WriteHeader(http.StatusRequestEntityTooLarge)
						fmt.Fprint(w, `{"error":"the input length exceeds the context length"}`)
						return
					}
				}
				lengthVectors(w, r, req)
			})
			got, err := f.client(t, Options{}).Embed(context.Background(), tc.texts)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d vectors", len(got))
			}
			for i, s := range tc.want {
				if got[i][0] != float32(len(s)) {
					t.Fatalf("vector %d = %v", i, got[i])
				}
			}
			requests := f.requests()
			found := false
			for _, req := range requests {
				for _, s := range req.Input {
					if s == string([]rune(long)[:150]) {
						found = true
					}
				}
			}
			if !found {
				t.Fatal("shortened prefix was not sent")
			}
		})
	}
}

func TestEmbedContextLengthErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		retries bool
	}{
		{"too dense", 413, `{"error":"the input length exceeds the context length"}`, true},
		{"other 413", 413, `{"error":"different error"}`, false},
		{"server error", 500, `{"error":"the input length exceeds the context length"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			_, err := f.client(t, Options{}).Embed(context.Background(), []string{strings.Repeat(secret, 100)})
			var status *StatusError
			if !errors.As(err, &status) || status.Code != tc.status {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "context length") {
				t.Fatalf("error leaked text: %v", err)
			}
			count := len(f.requests())
			if (!tc.retries && count != 1) || (tc.retries && (count < 2 || count > 14)) {
				t.Fatalf("requests = %d", count)
			}
		})
	}
}

func TestEmbedContextLengthCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ recorded) {
		cancel()
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		fmt.Fprint(w, `{"error":"the input length exceeds the context length"}`)
	})
	_, err := f.client(t, Options{}).Embed(ctx, []string{strings.Repeat("x", 1000)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if len(f.requests()) != 1 {
		t.Fatalf("requests = %d", len(f.requests()))
	}
}
