// Package output encodes a response into the chosen output format.
//
// The default format is the JSON envelope: {"ok":true,"data":...} on success and
// {"ok":false,"error":{"code":...,"message":...}} on failure, one line each.
// Commands that return lists can also print JSONL, see Lines. The other formats
// encode the same envelope: TOON for an agent that saves tokens, and Human for a
// person to read. A failure is the envelope in every format but JSONL, which has
// no envelope to put it in.
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Format is an output format of a command.
type Format string

// The formats a command can print.
const (
	// FormatJSON is one envelope on one line. It is the default: other programs
	// and agents read it without knowing the tool.
	FormatJSON Format = "json"
	// FormatJSONL is one item per line with no envelope, for a command that
	// returns a list. A page that continues ends with a {"next_cursor":"..."}
	// line.
	FormatJSONL Format = "jsonl"
	// FormatToon is the same envelope in TOON, a compact format for an agent's
	// input.
	FormatToon Format = "toon"
	// FormatHuman is the same envelope for a person: data as text, a failure as
	// one line.
	FormatHuman Format = "human"
)

// Formats lists every format a command may accept, in a fixed order. The first
// is the default.
func Formats() []Format { return []Format{FormatJSON, FormatJSONL, FormatToon, FormatHuman} }

// ParseFormat turns the value of --format into a Format.
func ParseFormat(s string) (Format, error) {
	for _, f := range Formats() {
		if string(f) == s {
			return f, nil
		}
	}
	return "", fmt.Errorf("--format must be %s, %s, %s or %s, got %q", FormatJSON, FormatJSONL, FormatToon, FormatHuman, s)
}

// FormatsWithoutJSONL is the list of formats for a command that does not return
// a list: JSONL needs items to put one per line.
func FormatsWithoutJSONL() []Format { return []Format{FormatJSON, FormatToon, FormatHuman} }

type success struct {
	OK          bool  `json:"ok"`
	Data        any   `json:"data"`
	ThrottledMS int64 `json:"throttled_ms,omitempty"`
}

// ErrorBody describes a failed command.
type ErrorBody struct {
	// Code is the stable machine name of the exit code, see package exitcode.
	Code string `json:"code"`
	// Message is a human-readable explanation, never containing secrets.
	Message           string `json:"message"`
	Reason            string `json:"reason,omitempty"`
	TrialRemains      *int   `json:"trial_remains,omitempty"`
	TrialResetsAt     string `json:"trial_resets_at,omitempty"`
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
}

type failure struct {
	OK          bool      `json:"ok"`
	Error       ErrorBody `json:"error"`
	ThrottledMS int64     `json:"throttled_ms,omitempty"`
}

// SuccessData is the success envelope of a result.
func SuccessData(data any) any { return SuccessDataWithThrottle(data, 0) }

// SuccessDataWithThrottle adds a positive server wait to the success envelope.
func SuccessDataWithThrottle(data any, milliseconds int64) any {
	return success{OK: true, Data: data, ThrottledMS: milliseconds}
}

// FailureData is the failure envelope of an error.
func FailureData(code, message string) any {
	return FailureDataWithThrottle(code, message, 0)
}

// FailureDataWithThrottle adds a positive server wait to the failure envelope.
func FailureDataWithThrottle(code, message string, milliseconds int64) any {
	return failure{OK: false, Error: ErrorBody{Code: code, Message: message}, ThrottledMS: milliseconds}
}

// FailureDataWithDetails includes supported structured Telegram error details.
func FailureDataWithDetails(code, message string, milliseconds int64, data map[string]any) any {
	body := ErrorBody{Code: code, Message: message}
	body.Reason, _ = data["reason"].(string)
	body.TrialResetsAt, _ = data["trial_resets_at"].(string)
	if n, ok := data["trial_remains"].(float64); ok {
		v := int(n)
		body.TrialRemains = &v
	}
	if n, ok := data["retry_after_seconds"].(float64); ok {
		v := int(n)
		body.RetryAfterSeconds = &v
	}
	return failure{OK: false, Error: body, ThrottledMS: milliseconds}
}

// Success writes a success envelope in the format.
func Success(w io.Writer, f Format, data any) error {
	return Write(w, f, SuccessData(data))
}

// Failure writes a failure envelope in the format. A failure is never one item
// per line, so JSONL gets the envelope as well: an agent finds the error the
// same way whatever format it asked for.
func Failure(w io.Writer, f Format, code, message string) error {
	return FailureWithThrottle(w, f, code, message, 0)
}

// FailureWithThrottle writes a failure and its optional server wait.
func FailureWithThrottle(w io.Writer, f Format, code, message string, milliseconds int64) error {
	return Write(w, f, FailureDataWithThrottle(code, message, milliseconds))
}

// Write writes an envelope in the format. A format without an envelope of its
// own, JSONL today, falls back to the JSON envelope.
func Write(w io.Writer, f Format, envelope any) error {
	switch f {
	case FormatToon:
		return Toon(w, envelope)
	case FormatHuman:
		return Human(w, envelope)
	}
	return encode(w, envelope)
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
