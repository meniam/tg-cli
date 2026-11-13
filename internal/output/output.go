// Package output encodes a response into the chosen output format.
//
// The default format is the JSON envelope: {"ok":true,"data":...} on success and
// {"ok":false,"error":{"code":...,"message":...}} on failure, one line each.
// Commands that return lists can also print JSONL, see Lines. A failure is
// always the envelope, whatever the format.
package output

import (
	"encoding/json"
	"io"
)

type success struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

// ErrorBody describes a failed command.
type ErrorBody struct {
	// Code is the stable machine name of the exit code, see package exitcode.
	Code string `json:"code"`
	// Message is a human-readable explanation, never containing secrets.
	Message string `json:"message"`
}

type failure struct {
	OK    bool      `json:"ok"`
	Error ErrorBody `json:"error"`
}

// Success writes a success envelope followed by a newline.
func Success(w io.Writer, data any) error {
	return encode(w, success{OK: true, Data: data})
}

// Failure writes a failure envelope followed by a newline.
func Failure(w io.Writer, code, message string) error {
	return encode(w, failure{OK: false, Error: ErrorBody{Code: code, Message: message}})
}

// Lines writes every item as one compact JSON line, JSONL style, with no
// envelope. When more items follow, a last line {"next_cursor":"..."} carries
// the cursor: it is told apart from the items by that key.
func Lines[T any](w io.Writer, items []T, nextCursor string) error {
	for _, item := range items {
		if err := encode(w, item); err != nil {
			return err
		}
	}
	if nextCursor == "" {
		return nil
	}
	return encode(w, struct {
		NextCursor string `json:"next_cursor"`
	}{nextCursor})
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
