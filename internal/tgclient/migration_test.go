package tgclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"
	"github.com/gotd/td/tgtest/cluster"
	"github.com/gotd/td/transport"
	"tg-cli/internal/netconf"
)

type memoryListener struct {
	incoming chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func (l *memoryListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *memoryListener) Close() error { l.once.Do(func() { close(l.closed) }); return nil }
func (*memoryListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (l *memoryListener) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.incoming <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

func TestMigrationAcrossDC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	options := []tg.DCOption{{ID: 2, IPAddress: "192.0.2.2", Port: 443}, {ID: 4, IPAddress: "192.0.2.4", Port: 443}}
	cl := cluster.NewCluster(cluster.Options{Config: tg.Config{DCOptions: options}})
	first, d2 := cl.DC(2, "first")
	second, d4 := cl.DC(4, "second")
	d2.HandleFunc(tg.AuthLogOutRequestTypeID, func(s *tgtest.Server, r *tgtest.Request) error { return s.SendErr(r, tgerr.New(303, "USER_MIGRATE_4")) })
	d4.Result(tg.AuthLogOutRequestTypeID, &tg.BoolTrue{})
	listeners := map[string]*memoryListener{"192.0.2.2:443": {incoming: make(chan net.Conn), closed: make(chan struct{})}, "192.0.2.4:443": {incoming: make(chan net.Conn), closed: make(chan struct{})}}
	done := make(chan error, 2)
	for i, server := range []*tgtest.Server{first, second} {
		addr := fmt.Sprintf("192.0.2.%d:443", []int{2, 4}[i])
		listener := listeners[addr]
		go func() { done <- server.Serve(ctx, transport.Listen(listener)) }()
	}
	defer func() {
		cancel()
		for _, l := range listeners {
			_ = l.Close()
		}
		for range 2 {
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("stop test server: %v", err)
			}
		}
	}()
	resolver := dcs.Plain(dcs.PlainOptions{Dial: func(ctx context.Context, _, addr string) (net.Conn, error) {
		l := listeners[addr]
		if l == nil {
			return nil, fmt.Errorf("unexpected DC address: %s", addr)
		}
		return l.dial(ctx)
	}})
	state := newConnection(netconf.Settings{})
	storage := &session.StorageMemory{}
	client := telegram.NewClient(telegram.TestAppID, telegram.TestAppHash, telegram.Options{DC: 2, DCList: dcs.List{Options: options}, Resolver: resolver, PublicKeys: cl.Keys(), SessionStorage: storage, NoUpdates: true, OnConnectionStateEvent: state.update, ReconnectionBackoff: func() backoff.BackOff { return backoff.NewConstantBackOff(time.Millisecond) }})
	err := client.Run(ctx, func(ctx context.Context) error {
		var result tg.BoolTrue
		if err := client.Invoke(ctx, &tg.AuthLogOutRequest{}, &result); err != nil {
			return err
		}
		return client.Invoke(ctx, &tg.AuthLogOutRequest{}, &result)
	})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	dc := state.info.DC
	state.mu.Unlock()
	if dc != 4 {
		t.Fatalf("primary DC = %d, want 4", dc)
	}
	data, err := (&session.Loader{Storage: storage}).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if data.DC != 4 {
		t.Fatalf("persisted DC = %d, want 4", data.DC)
	}
}

type notifyingStorage struct {
	session.StorageMemory
	saved chan struct{}
	once  sync.Once
}

func (s *notifyingStorage) StoreSession(ctx context.Context, data []byte) error {
	if err := s.StorageMemory.StoreSession(ctx, data); err != nil {
		return err
	}
	if len(data) > 0 {
		s.once.Do(func() { close(s.saved) })
	}
	return nil
}

func TestReconnectSameSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	options := []tg.DCOption{{ID: 2, IPAddress: "192.0.2.2", Port: 443}}
	cl := cluster.NewCluster(cluster.Options{Config: tg.Config{DCOptions: options}})
	server, d := cl.DC(2, "server")
	d.Result(tg.HelpGetNearestDCRequestTypeID, &tg.NearestDC{})
	listener := &memoryListener{incoming: make(chan net.Conn), closed: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, transport.Listen(listener)) }()
	defer func() {
		cancel()
		_ = listener.Close()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("stop test server: %v", err)
		}
	}()
	var mu sync.Mutex
	var conns []net.Conn
	resolver := dcs.Plain(dcs.PlainOptions{Dial: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != "192.0.2.2:443" {
			return nil, fmt.Errorf("unexpected address %s", address)
		}
		conn, err := listener.dial(ctx)
		if err == nil {
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
		return conn, err
	}})
	state := newConnection(netconf.Settings{})
	storage := &notifyingStorage{saved: make(chan struct{})}
	client := telegram.NewClient(telegram.TestAppID, telegram.TestAppHash, telegram.Options{DC: 2, DCList: dcs.List{Options: options}, Resolver: resolver, PublicKeys: cl.Keys(), SessionStorage: storage, NoUpdates: true, OnConnectionStateEvent: state.update, ReconnectionBackoff: func() backoff.BackOff { return backoff.NewConstantBackOff(time.Millisecond) }})
	err := client.Run(ctx, func(ctx context.Context) error {
		var result tg.NearestDC
		if err := client.Invoke(ctx, &tg.HelpGetNearestDCRequest{}, &result); err != nil {
			return err
		}
		select {
		case <-storage.saved:
		case <-ctx.Done():
			return ctx.Err()
		}
		before, err := (&session.Loader{Storage: storage}).Load(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		conn := conns[0]
		mu.Unlock()
		if err = conn.Close(); err != nil {
			return err
		}
		for {
			state.mu.Lock()
			info := state.info
			changed := state.changed
			state.mu.Unlock()
			if info.Connection == "ready" && info.Reconnects >= 1 {
				break
			}
			select {
			case <-changed:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err = client.Invoke(ctx, &tg.HelpGetNearestDCRequest{}, &result); err != nil {
			return err
		}
		after, err := (&session.Loader{Storage: storage}).Load(ctx)
		if err != nil {
			return err
		}
		if before.DC != after.DC || !bytes.Equal(before.AuthKey, after.AuthKey) {
			return errors.New("reconnection changed the session key or DC")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
