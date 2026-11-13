// Package daemon owns the process lifecycle: the profile directory and lock,
// the Unix socket, the JSON-RPC handlers and the stop sequence.
//
// The daemon is the only owner of a Telegram session. It talks to Telegram
// through the Telegram interface it declares, so everything here is testable
// without a network.
package daemon
