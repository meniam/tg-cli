// Package cli parses argv, calls the daemon and prints the response.
//
// It must not import tgclient or store: the Telegram client is injected into
// the daemon by main through Deps.NewTelegram, and everything else reaches the
// daemon over rpc.
package cli
