// Package tgclient wraps gotd/td: one account, one client.
//
// It converts gotd types into the wire types of package rpc and maps gotd
// errors to *rpc.Error, so no other package needs to import gotd.
package tgclient
