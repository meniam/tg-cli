package daemon

import (
	"context"
	"time"

	"tg-cli/internal/rpc"
)

type connectionReporter interface{ Connection() rpc.ConnectionInfo }
type connectionSink interface {
	SetConnectionSink(func(rpc.ConnectionInfo))
}

func (d *daemon) fillConnection(st *rpc.StatusResult) bool {
	reporter, ok := d.opts.Telegram.(connectionReporter)
	if !ok {
		return false
	}
	info := reporter.Connection()
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
func (d *daemon) logConnection(ctx context.Context) {
	source, ok := d.opts.Telegram.(connectionSink)
	if !ok {
		return
	}
	updates := make(chan rpc.ConnectionInfo, 16)
	source.SetConnectionSink(func(s rpc.ConnectionInfo) {
		select {
		case updates <- s:
		default:
		}
	})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case s := <-updates:
				if s.Connection == "disconnected" {
					d.log.Warn("telegram connection", "state", s.Connection, "dc", s.DC)
				} else {
					d.log.Info("telegram connection", "state", s.Connection, "dc", s.DC)
				}
			}
		}
	}()
}

const connectionWait = 15 * time.Second
