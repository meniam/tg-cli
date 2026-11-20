// Package embed is a client of a local Ollama service that turns texts into
// embedding vectors. It talks to loopback addresses only: the text of a private
// conversation never leaves the machine. Neither the errors nor anything else
// this package produces contain the text it was given.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// numCtx is the context window asked from the model. bge-m3 reads up to 8192
	// tokens; Ollama truncates a longer input instead of failing.
	numCtx = 8192
	// keepAlive keeps the model loaded between two indexing runs, so the query
	// embedding of a search does not pay the load time.
	keepAlive = "30m"
	// maxRequestChars bounds the summed length of the texts of one request, in
	// characters. A single longer text still goes out, alone.
	maxRequestChars = 24000
	// probeText is the short text embedded to learn the vector size of a model.
	probeText = "dim"
	// maxResponseBytes bounds a response body: 128 vectors of 1024 numbers are
	// about 1.5 MB, so this leaves a wide margin.
	maxResponseBytes = 64 << 20
	// maxErrorBytes bounds the part of an error body that is read.
	maxErrorBytes = 4 << 10

	// DefaultBatch is the number of texts per request when Options.Batch is zero.
	DefaultBatch = 16
	// DefaultTimeout is the time limit of one request when Options.Timeout is zero.
	// The first request after an idle period loads the model, which takes seconds.
	DefaultTimeout = 2 * time.Minute
)

var (
	// ErrUnavailable means the service did not answer: the connection was refused
	// or dropped, or the request ran out of time.
	ErrUnavailable = errors.New("ollama is unavailable")
	// ErrModelMissing means the service answered that it has no such model
	// (HTTP 404, or an error text that says "not found").
	ErrModelMissing = errors.New("ollama does not have the model")
	// ErrBadResponse means the answer does not match the request: not JSON, a
	// wrong number of vectors, an empty vector, or vectors of different sizes.
	ErrBadResponse = errors.New("ollama returned an invalid response")
)

// StatusError is an unexpected HTTP status. It carries the code only: the body
// of the answer is not copied into the error.
type StatusError struct {
	// Code is the HTTP status code.
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("ollama answered HTTP %d", e.Code) }

// ModelInfo identifies the model that produced the vectors. A stored vector is
// comparable with a new one only when the name, the digest and the size agree.
type ModelInfo struct {
	// Name is the full model name as Ollama lists it, with the tag.
	Name string
	// Digest is the content hash of the model, so a re-pulled model with the
	// same name but different weights is noticed.
	Digest string
	// Dim is the length of the vectors.
	Dim int
}

// Embedder is what the daemon needs from the client.
type Embedder interface {
	// Embed returns one vector per text, in order, all of the same length.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Info reports the name, the digest and the vector size of the model.
	Info(ctx context.Context) (ModelInfo, error)
}

// Options configures a Client.
type Options struct {
	// BaseURL is the address of Ollama: http or https, with a loopback host.
	BaseURL string
	// Model is the model name, for example "bge-m3".
	Model string
	// Batch is the largest number of texts in one request; zero means DefaultBatch.
	Batch int
	// Timeout limits one request; zero means DefaultTimeout.
	Timeout time.Duration
	// HTTP is the transport. Nil gives a client that ignores the proxy settings
	// of the environment. Redirects are never followed either way.
	HTTP *http.Client
}

// Client talks to Ollama. It is safe for concurrent use.
type Client struct {
	base    *url.URL
	model   string
	batch   int
	timeout time.Duration
	http    *http.Client
}

var _ Embedder = (*Client)(nil)

// New checks the options and returns a client. It makes no request.
func New(o Options) (*Client, error) {
	base, err := ValidateBaseURL(o.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(o.Model) == "" {
		return nil, errors.New("embed: the model name is empty")
	}
	if o.Batch < 0 {
		return nil, errors.New("embed: the batch size must not be negative")
	}
	if o.Timeout < 0 {
		return nil, errors.New("embed: the timeout must not be negative")
	}
	c := &Client{base: base, model: o.Model, batch: o.Batch, timeout: o.Timeout}
	if c.batch == 0 {
		c.batch = DefaultBatch
	}
	if c.timeout == 0 {
		c.timeout = DefaultTimeout
	}
	// A copy, so the redirect rule does not change the caller's client. A
	// redirect could lead the text out of the machine.
	var h http.Client
	if o.HTTP != nil {
		h = *o.HTTP
	} else {
		h.Transport = &http.Transport{Proxy: nil}
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.http = &h
	return c, nil
}

// ValidateBaseURL checks an Ollama address and returns it parsed. The scheme is
// http or https and the host is exactly 127.0.0.1, ::1 or localhost, because
// the texts sent to the service are private conversations. The address has no
// credentials, query, fragment or path.
func ValidateBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("embed: the address is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("embed: the address scheme must be http or https")
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "::1", "localhost":
	default:
		return nil, errors.New("embed: the address host must be 127.0.0.1, ::1 or localhost")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, errors.New("embed: the address port must be from 1 to 65535")
		}
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("embed: the address must be a scheme, a host and an optional port only")
	}
	return u, nil
}

// Embed sends the texts in requests of at most Batch texts and about 24 000
// characters each, and returns the vectors in the order of the texts. It fails
// as a whole: a partial result is never returned.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	dim := 0
	for _, batch := range splitBatches(texts, c.batch) {
		vectors, err := c.embedBatch(ctx, batch)
		if err != nil {
			return nil, err
		}
		for _, v := range vectors {
			if len(v) == 0 {
				return nil, fmt.Errorf("%w: an empty vector", ErrBadResponse)
			}
			if dim == 0 {
				dim = len(v)
			} else if len(v) != dim {
				return nil, fmt.Errorf("%w: vectors of different sizes", ErrBadResponse)
			}
		}
		out = append(out, vectors...)
	}
	return out, nil
}

// splitBatches cuts texts into consecutive runs of at most size texts whose
// summed length stays within maxRequestChars. A run holds at least one text.
func splitBatches(texts []string, size int) [][]string {
	var batches [][]string
	start, chars := 0, 0
	for i, t := range texts {
		n := utf8.RuneCountInString(t)
		if i > start && (i-start >= size || chars+n > maxRequestChars) {
			batches = append(batches, texts[start:i])
			start, chars = i, 0
		}
		chars += n
	}
	return append(batches, texts[start:])
}

type embedRequest struct {
	Model     string       `json:"model"`
	Input     []string     `json:"input"`
	Options   embedOptions `json:"options"`
	KeepAlive string       `json:"keep_alive"`
}

type embedOptions struct {
	NumCtx int `json:"num_ctx"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.model, Input: texts, Options: embedOptions{NumCtx: numCtx}, KeepAlive: keepAlive})
	if err != nil {
		return nil, fmt.Errorf("embed: encode the request: %w", err)
	}
	data, err := c.do(ctx, http.MethodPost, "/api/embed", body)
	if err != nil {
		return nil, err
	}
	var resp embedResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("%w: not a JSON answer", ErrBadResponse)
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("%w: %d vectors for %d texts", ErrBadResponse, len(resp.Embeddings), len(texts))
	}
	return resp.Embeddings, nil
}

// do sends one request and returns the body of a successful answer. Every
// failure is classified: a cancelled or expired ctx returns its own error,
// a transport failure is ErrUnavailable, an error status is ErrModelMissing or
// a *StatusError.
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, c.base.JoinPath(path).String(), reader)
	if err != nil {
		return nil, fmt.Errorf("embed: build the request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, transportError(ctx, err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("%w: the answer is too large", ErrBadResponse)
	}
	return data, nil
}

// transportError separates the cancellation of the caller from a failure of the
// service. The request timeout belongs to the second kind.
func transportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("embed: request stopped: %w", ctxErr)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// statusError turns an error status into a typed error. Only the status code
// and the fact of "not found" are used: the error text of the service is not
// copied, so nothing it echoes can reach a log.
func statusError(resp *http.Response) error {
	text, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	if resp.StatusCode == http.StatusNotFound || strings.Contains(strings.ToLower(string(text)), "not found") {
		return fmt.Errorf("%w (HTTP %d)", ErrModelMissing, resp.StatusCode)
	}
	return &StatusError{Code: resp.StatusCode}
}

type tagsResponse struct {
	Models []struct {
		Name   string `json:"name"`
		Model  string `json:"model"`
		Digest string `json:"digest"`
	} `json:"models"`
}

// Info finds the model among the installed ones (GET /api/tags) for its name and
// digest, and embeds a short text for the vector size. The probe is used rather
// than /api/show because the size key there is named after the architecture
// ("bert.embedding_length") and differs between models; the probe works for any
// model and also loads it, so the first real request is fast.
func (c *Client) Info(ctx context.Context) (ModelInfo, error) {
	data, err := c.do(ctx, http.MethodGet, "/api/tags", nil)
	if err != nil {
		return ModelInfo{}, err
	}
	var tags tagsResponse
	if err := json.Unmarshal(data, &tags); err != nil {
		return ModelInfo{}, fmt.Errorf("%w: not a JSON answer", ErrBadResponse)
	}
	want := c.model
	if !strings.Contains(want, ":") {
		want += ":latest"
	}
	info := ModelInfo{}
	for _, m := range tags.Models {
		if m.Name == want || m.Model == want {
			info.Name, info.Digest = want, m.Digest
			if m.Name != "" {
				info.Name = m.Name
			}
			break
		}
	}
	if info.Name == "" {
		return ModelInfo{}, fmt.Errorf("%w: it is not among the installed models", ErrModelMissing)
	}
	if info.Digest == "" {
		return ModelInfo{}, fmt.Errorf("%w: the model has no digest", ErrBadResponse)
	}
	vectors, err := c.Embed(ctx, []string{probeText})
	if err != nil {
		return ModelInfo{}, err
	}
	info.Dim = len(vectors[0])
	return info, nil
}
