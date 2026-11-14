package rpc

// ErrChatNotFound returns the shared error for a missing or invisible chat or
// message. Callers must not reveal which of those conditions occurred.
func ErrChatNotFound() *Error {
	return NewError(CodeNotFound, "not found: the chat or message does not exist or is not visible to this account")
}

// ErrUnknownChatID returns the error for a numeric chat id the session does not
// know. A closed chat id must give this same answer, so a caller cannot tell a
// closed chat from an unknown id.
func ErrUnknownChatID() *Error {
	return NewError(CodeNotFound, "not found: this session does not know that id; use @username, or read the chat list first")
}
