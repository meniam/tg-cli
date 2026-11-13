// Package rpc implements JSON-RPC 2.0 over a stream connection, one JSON
// message per line, plus the wire contract between the CLI and the daemon.
//
// The server and the client know nothing about Telegram: methods are plain
// handlers. The method names and the parameter and result types live in api.go.
package rpc
