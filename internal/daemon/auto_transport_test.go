package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/netconf"
	"tg-cli/internal/rpc"
)

func TestAutoTransportTimeoutKeepsProxyAndSticks(t *testing.T) {
	proxy := &netconf.Proxy{Kind: netconf.SOCKS5, Host: "proxy", Port: 1234}
	ready := make(chan struct{})
	a := newAutoTransport(netconf.Settings{Transport: netconf.Auto, Proxy: proxy}, ready)
	if a.network().Transport != netconf.TCP {
		t.Fatal("initial transport is not TCP")
	}
	clock := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() { a.watch(context.Background(), clock, func() { close(done) }) }()
	clock <- time.Now()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout did not switch")
	}
	got := a.network()
	if got.Transport != netconf.WSS || got.Proxy != proxy {
		t.Fatalf("settings: %+v", got)
	}
	if a.switchToWSS() {
		t.Fatal("switched twice")
	}
}

func TestAutoTransportConnectionErrorAndReadiness(t *testing.T) {
	ready := make(chan struct{})
	a := newAutoTransport(netconf.Settings{Transport: netconf.Auto}, ready)
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.switchToWSS() {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if count != 1 || a.network().Transport != netconf.WSS {
		t.Fatalf("switches: %d", count)
	}
	ready2 := make(chan struct{})
	close(ready2)
	b := newAutoTransport(netconf.Settings{Transport: netconf.Auto}, ready2)
	if b.switchToWSS() || b.network().Transport != netconf.TCP {
		t.Fatal("timeout switched after readiness")
	}
	if !b.switchOnFailure() || b.network().Transport != netconf.WSS {
		t.Fatal("connection failure after readiness did not switch")
	}
	c := newAutoTransport(netconf.Settings{Transport: netconf.Auto, Proxy: &netconf.Proxy{Kind: netconf.HTTP}}, make(chan struct{}))
	if c.network().Transport != netconf.WSS || c.switchToWSS() {
		t.Fatal("HTTP proxy must start on WSS")
	}
}

type failedTCPClient struct{ *fakeTelegram }

func (f *failedTCPClient) Run(context.Context) error {
	return errors.New("synthetic connection failure")
}

func TestAutoTransportRestartsAfterConnectionFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy := &netconf.Proxy{Kind: netconf.SOCKS5, Host: "proxy", Port: 1}
	first := &failedTCPClient{fakeTelegram: newFake()}
	auto := newAutoTransport(netconf.Settings{Transport: netconf.Auto, Proxy: proxy}, first.Ready())
	gate := &telegramAdmission{client: first}
	next := newFake()
	ready := make(chan struct{})
	finished := make(chan error, 1)
	var supervisor *clientSupervisor
	onFailure := func() bool {
		if !auto.switchToWSS() {
			return false
		}
		go func() { <-ready; finished <- supervisor.restart(ctx) }()
		return true
	}
	supervisor = newClientSupervisor(ctx, gate, first, func(context.Context) (Telegram, error) {
		settings := auto.network()
		if settings.Transport != netconf.WSS || settings.Proxy != proxy {
			return nil, errors.New("wrong replacement network")
		}
		return next, nil
	}, func(Telegram) {}, nil, onFailure, nil, time.Second)
	close(ready)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("connection failure did not restart client")
	}
	select {
	case <-next.Ready():
	case <-time.After(time.Second):
		t.Fatal("replacement did not become ready")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := supervisor.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
}

func TestAutoTransportRestartsAfterReadinessTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	next := newFake()
	proxy := &netconf.Proxy{Kind: netconf.SOCKS5, Host: "proxy", Port: 1}
	auto := newAutoTransport(netconf.Settings{Transport: netconf.Auto, Proxy: proxy}, first.Ready())
	gate := &telegramAdmission{client: first}
	s := newClientSupervisor(ctx, gate, first, func(context.Context) (Telegram, error) {
		if got := auto.network(); got.Transport != netconf.WSS || got.Proxy != proxy {
			return nil, errors.New("wrong replacement network")
		}
		return next, nil
	}, func(Telegram) {}, nil, nil, nil, time.Second)
	<-first.started
	timer := make(chan time.Time, 1)
	finished := make(chan error, 1)
	go auto.watch(ctx, timer, func() { finished <- s.restart(ctx) })
	timer <- time.Now()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout did not restart client")
	}
	select {
	case <-first.exited:
	default:
		t.Fatal("old client still running")
	}
	select {
	case <-next.Ready():
	case <-time.After(time.Second):
		t.Fatal("replacement did not start")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := s.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
}

func TestAutoTransportReplacementReadinessPreventsFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	next := newFake()
	ready := make(chan struct{})
	var once sync.Once
	auto := newAutoTransport(netconf.Settings{Transport: netconf.Auto}, ready)
	gate := &telegramAdmission{client: first}
	s := newClientSupervisor(ctx, gate, first, func(context.Context) (Telegram, error) { return next, nil }, func(Telegram) {}, nil, nil, func() { once.Do(func() { close(ready) }) }, time.Second)
	<-first.started
	if err := s.restart(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("replacement readiness was not observed")
	}
	if auto.switchToWSS() {
		t.Fatal("switched after replacement became ready")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := s.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
}

type disconnectedTCPClient struct {
	*fakeTelegram
	sink func(rpc.ConnectionInfo)
}

func (f *disconnectedTCPClient) SetConnectionSink(sink func(rpc.ConnectionInfo)) { f.sink = sink }
func (f *disconnectedTCPClient) Run(ctx context.Context) error {
	f.sink(rpc.ConnectionInfo{Connection: "disconnected"})
	<-ctx.Done()
	return ctx.Err()
}

func TestAutoTransportRestartsOnDisconnectedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := &disconnectedTCPClient{fakeTelegram: newFake()}
	next := newFake()
	gate := &telegramAdmission{client: first}
	auto := newAutoTransport(netconf.Settings{Transport: netconf.Auto}, first.Ready())
	request := make(chan struct{}, 1)
	d := &daemon{log: slog.New(slog.DiscardHandler)}
	d.logConnection(ctx, first, func(state rpc.ConnectionInfo) {
		if state.Connection == "disconnected" && auto.switchOnFailure() {
			request <- struct{}{}
		}
	})
	s := newClientSupervisor(ctx, gate, first, func(context.Context) (Telegram, error) {
		if auto.network().Transport != netconf.WSS {
			return nil, errors.New("WSS was not selected")
		}
		return next, nil
	}, func(Telegram) {}, nil, nil, nil, time.Second)
	select {
	case <-request:
	case <-time.After(time.Second):
		t.Fatal("connection error did not request switch")
	}
	if err := s.restart(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-next.Ready():
	case <-time.After(time.Second):
		t.Fatal("replacement did not start")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := s.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
}

func TestAutoTransportSwitchesAfterReadyConnectionFailure(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	auto := newAutoTransport(netconf.Settings{Transport: netconf.Auto}, ready)
	first := &disconnectedTCPClient{fakeTelegram: newFake()}
	requested := make(chan struct{}, 1)
	d := &daemon{log: slog.New(slog.DiscardHandler)}
	d.logConnection(context.Background(), first, func(state rpc.ConnectionInfo) {
		if state.Connection == "disconnected" && auto.switchOnFailure() {
			requested <- struct{}{}
		}
	})
	first.sink(rpc.ConnectionInfo{Connection: "disconnected"})
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("failure after readiness did not request WSS")
	}
	if auto.network().Transport != netconf.WSS {
		t.Fatal("WSS was not selected")
	}
}
