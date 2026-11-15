package rpc

import (
	"errors"

	"tg-cli/internal/exitcode"
)

// Batch describes failures in an otherwise successful batch.
type Batch struct {
	Partial bool        `json:"partial,omitempty"`
	Errors  []ItemError `json:"errors,omitempty"`
}

// ItemError describes one item that was not processed.
type ItemError struct {
	ID                int    `json:"id,omitempty"`
	Code              string `json:"code"`
	Message           string `json:"message"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

func (b Batch) IsPartial() bool { return b.Partial }

// ItemErrorFrom maps an RPC error to the public exit-code name.
func ItemErrorFrom(id int, err error) ItemError {
	out := ItemError{ID: id, Code: exitcode.Name(exitcode.Failure), Message: "internal error"}
	var e *Error
	if !errors.As(err, &e) {
		return out
	}
	code := exitcode.Failure
	switch e.Code {
	case CodeInvalidParams, CodeMethodNotFound:
		code = exitcode.Usage
	case CodeNotAuthorized:
		code = exitcode.NotAuthorized
	case CodeTelegram:
		code = exitcode.Telegram
	case CodeFloodWait:
		code = exitcode.FloodWait
	case CodeNotFound:
		code = exitcode.NotFound
	case CodeCursorExpired:
		code = exitcode.CursorExpired
	case CodeWriteDisabled:
		code = exitcode.WriteDisabled
	}
	out.Code, out.Message = exitcode.Name(code), e.Message
	if data, ok := e.Data.(map[string]any); ok {
		switch n := data["retry_after_seconds"].(type) {
		case int:
			out.RetryAfterSeconds = n
		case float64:
			out.RetryAfterSeconds = int(n)
		}
	}
	return out
}

// Chunks divides ids into slices of at most size. A non-positive size yields no chunks.
func Chunks(ids []int, size int) [][]int {
	if size <= 0 {
		return nil
	}
	var chunks [][]int
	for len(ids) > 0 {
		n := min(size, len(ids))
		chunks = append(chunks, ids[:n:n])
		ids = ids[n:]
	}
	return chunks
}
