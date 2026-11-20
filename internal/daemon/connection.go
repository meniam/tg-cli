package daemon

import (
	"context"
	"time"

	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
)

type connectionReporter interface{ Connection() rpc.ConnectionInfo }
type connectionSink interface {
	SetConnectionSink(func(rpc.ConnectionInfo))
}

// connectionOf reads the connection state of client without a request; false
// when the client does not report one.
func (d *daemon) connectionOf(client Telegram) (rpc.ConnectionInfo, bool) {
	reporter, ok := client.(connectionReporter)
	if !ok {
		return rpc.ConnectionInfo{}, false
	}
	info := reporter.Connection()
	if d.auto != nil && d.opts.TelegramConfig.Network.Transport == netconf.Auto {
		info.Transport = string(netconf.Auto)
		if d.auto.hasSwitched() {
			info.TransportSwitches = 1
		}
	}
	return info, true
}

func (d *daemon) fillConnection(ctx context.Context, st *rpc.StatusResult) bool {
	info, ok := d.connectionOf(d.telegram(ctx))
	if !ok {
		return false
	}
	st.ConnectionInfo = &info
	st.Connected = st.Connection == "ready"
	if st.Connected {
		return false
	}
	d.account.mu.RLock()
	if d.account.user != nil {
		u := *d.account.user
		st.User = &u
		st.Authorized = true
	}
	d.account.mu.RUnlock()
	return true
}
func (d *daemon) logConnection(ctx context.Context, client Telegram, observe func(rpc.ConnectionInfo)) {
	source, ok := client.(connectionSink)
	if !ok {
		return
	}
	source.SetConnectionSink(func(state rpc.ConnectionInfo) {
		if ctx.Err() != nil {
			return
		}
		if observe != nil {
			observe(state)
		}
		if state.Connection == "disconnected" {
			d.log.Warn("telegram connection", "state", state.Connection, "dc", state.DC)
		} else {
			d.log.Info("telegram connection", "state", state.Connection, "dc", state.DC)
		}
	})
}

const connectionWait = 15 * time.Second
