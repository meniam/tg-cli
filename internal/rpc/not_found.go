package rpc

// ErrChatNotFound returns the shared error for a missing or invisible chat or
// message. Callers must not reveal which of those conditions occurred.
func ErrChatNotFound() *Error {
	return NewError(CodeNotFound, "not found: the chat or message does not exist or is not visible to this account")
}
