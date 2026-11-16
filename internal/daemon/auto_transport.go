package daemon

import (
	"context"
	"sync"
	"time"

	"tg-cli/internal/netconf"
)

// InitialNetwork selects a concrete transport while retaining the original
// proxy. HTTP proxies only support the WSS path.
func InitialNetwork(s netconf.Settings) netconf.Settings {
	if s.Transport == netconf.Auto {
		s.Transport = netconf.TCP
		if s.Proxy != nil && s.Proxy.Kind == netconf.HTTP {
			s.Transport = netconf.WSS
		}
	}
	return s
}

type autoTransport struct {
	mu       sync.Mutex
	settings netconf.Settings
	eligible bool
	switched bool
	ready    <-chan struct{}
}

func newAutoTransport(settings netconf.Settings, ready <-chan struct{}) *autoTransport {
	initial := InitialNetwork(settings)
	return &autoTransport{settings: initial, eligible: settings.Transport == netconf.Auto && initial.Transport == netconf.TCP, ready: ready}
}

func (a *autoTransport) network() netconf.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// switchToWSS returns true exactly once, and only before the first TCP client
// became ready. The proxy pointer is unchanged.
func (a *autoTransport) switchToWSS() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.eligible {
		return false
	}
	select {
	case <-a.ready:
		return false
	default:
	}
	a.eligible = false
	a.switched = true
	a.settings.Transport = netconf.WSS
	return true
}

// switchOnFailure also handles a failed reconnect after the first ready state.
func (a *autoTransport) switchOnFailure() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.eligible {
		return false
	}
	a.eligible = false
	a.switched = true
	a.settings.Transport = netconf.WSS
	return true
}

func (a *autoTransport) watch(ctx context.Context, timeout <-chan time.Time, switchClient func()) {
	select {
	case <-ctx.Done():
	case <-a.ready:
	case <-timeout:
		if a.switchToWSS() {
			switchClient()
		}
	}
}

func (a *autoTransport) hasSwitched() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.switched
}
