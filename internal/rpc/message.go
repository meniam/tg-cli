package rpc

import (
	"encoding/json"
	"fmt"
)

// Version is the only supported JSON-RPC protocol version.
const Version = "2.0"

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// Application error codes, in the range JSON-RPC reserves for implementations.
const (
	// CodeNotAuthorized means the profile has no logged-in account.
	CodeNotAuthorized = -32001
	// CodeTelegram means Telegram rejected the request.
	CodeTelegram = -32002
	// CodeFloodWait means Telegram asked to wait; Data has retry_after_seconds.
	CodeFloodWait = -32003
	// CodeBusy means the server already runs the maximum of parallel requests.
	CodeBusy = -32004
	// CodeLoginState means the login step does not fit the current login state.
	CodeLoginState = -32005
	// CodeNotFound means the chat, user or message does not exist or is not visible.
	CodeNotFound = -32006
)

// Request is a JSON-RPC request. A request without ID is a notification and
// gets no response.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request expects no response.
func (r Request) IsNotification() bool { return len(r.ID) == 0 }

// Response is a JSON-RPC response. ID is null when the request id is unknown.
type Response struct {
	JSONRPC     string          `json:"jsonrpc"`
	ID          json.RawMessage `json:"id"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       *Error          `json:"error,omitempty"`
	ThrottledMS int64           `json:"throttled_ms,omitempty"`
}

// Error is a JSON-RPC error object. It also serves as a Go error, so handlers
// and Telegram wrappers return it directly and the dispatcher passes it on.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// NewError builds an *Error.
func NewError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}
