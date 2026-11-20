package daemon

import (
	"context"
	"testing"
	"time"

	"tg-cli/internal/netconf"
)

// A transport the user pinned never switches to WSS: not on a timeout, not on
// a connection failure, not through watch.
func TestAudit03PinnedTransportNeverSwitchesToWSS(t *testing.T) {
	for _, transport := range []netconf.Transport{netconf.TCP, netconf.WSS} {
		t.Run(string(transport), func(t *testing.T) {
			a := newAutoTransport(netconf.Settings{Transport: transport}, make(chan struct{}))
			if a.switchToWSS() || a.switchOnFailure() {
				t.Fatal("a pinned transport switched")
			}
			clock := make(chan time.Time, 1)
			clock <- time.Now()
			switched := false
			a.watch(context.Background(), clock, func() { switched = true })
			if switched || a.hasSwitched() || a.network().Transport != transport {
				t.Fatalf("pinned %s changed: switched=%v settings=%+v", transport, a.hasSwitched(), a.network())
			}
		})
	}
}
