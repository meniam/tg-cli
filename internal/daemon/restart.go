package daemon

import (
	"context"
	"errors"
	"sync"
	"time"
)

// clientRun owns one invocation of Telegram.Run. A stopped client cannot be
// started again; done closes only after Run and its cleanup have returned.
type clientRun struct {
	client Telegram
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func startClientRun(ctx context.Context, client Telegram) *clientRun {
	ctx, cancel := context.WithCancel(ctx)
	r := &clientRun{client: client, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.err = client.Run(ctx)
	}()
	return r
}

type restartCall struct {
	done chan struct{}
	err  error
}

// clientRestarter coalesces concurrent restart requests. The owner supplies
// the daemon context so the lifecycle is independent of an individual caller.
type clientRestarter struct {
	mu      sync.Mutex
	current *restartCall
}

func (r *clientRestarter) do(ctx context.Context, perform func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	call := r.current
	if call == nil {
		call = &restartCall{done: make(chan struct{})}
		r.current = call
		go func() {
			err := perform()
			r.mu.Lock()
			call.err = err
			r.current = nil
			close(call.done)
			r.mu.Unlock()
		}()
	}
	r.mu.Unlock()
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

var errClientFactoryUnavailable = errors.New("telegram client factory unavailable")

// replaceClient blocks admissions and joins the old Run before the factory can
// open the same session. A failed or canceled replacement remains offline.
func replaceClient(ctx context.Context, gate *telegramAdmission, old *clientRun, grace time.Duration, makeClient func(context.Context) (Telegram, error), prepare func(Telegram)) (*clientRun, error) {
	if err := gate.drain(ctx, grace); err != nil {
		return nil, err
	}
	if old != nil {
		old.cancel()
		select {
		case <-old.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if makeClient == nil {
		return nil, errClientFactoryUnavailable
	}
	client, err := makeClient(ctx)
	if err != nil {
		// Factory errors can contain credentials; do not expose their text.
		return nil, errors.New("create telegram client failed")
	}
	if client == nil {
		return nil, errors.New("create telegram client failed")
	}
	prepare(client)
	run := startClientRun(ctx, client)
	gate.unblock(client)
	return run, nil
}

// clientSupervisor owns the active Run and serializes replacement with its
// completion notification. A completion from a deliberately stopped run must
// not be mistaken for an unexpected daemon failure.
type clientSupervisor struct {
	mu         sync.Mutex
	ctx        context.Context
	gate       *telegramAdmission
	factory    func(context.Context) (Telegram, error)
	prepare    func(Telegram)
	ignoreExit func() bool
	onFailure  func() bool
	onReady    func()
	grace      time.Duration
	current    *clientRun
	initial    *clientRun
	active     Telegram
	changed    chan struct{}
	restarting bool
	exits      chan error
	restarts   clientRestarter
	watchers   sync.WaitGroup
}

func newClientSupervisor(ctx context.Context, gate *telegramAdmission, initial Telegram, factory func(context.Context) (Telegram, error), prepare func(Telegram), ignoreExit func() bool, onFailure func() bool, onReady func(), grace time.Duration) *clientSupervisor {
	s := &clientSupervisor{ctx: ctx, gate: gate, factory: factory, prepare: prepare, ignoreExit: ignoreExit, onFailure: onFailure, onReady: onReady, grace: grace, exits: make(chan error, 1), active: initial, changed: make(chan struct{})}
	s.current = startClientRun(ctx, initial)
	s.initial = s.current
	s.watch(s.current)
	return s
}

func (s *clientSupervisor) watch(run *clientRun) {
	if s.onReady != nil {
		s.watchers.Add(1)
		go func() {
			defer s.watchers.Done()
			select {
			case <-run.client.Ready():
				s.onReady()
			case <-run.done:
				select {
				case <-run.client.Ready():
					s.onReady()
				default:
				}
			case <-s.ctx.Done():
			}
		}()
	}
	s.watchers.Add(1)
	go func() {
		defer s.watchers.Done()
		<-run.done
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.current != run || s.restarting || s.ctx.Err() != nil || s.ignoreExit != nil && s.ignoreExit() {
			return
		}
		if run == s.initial && run.err != nil && !errors.Is(run.err, context.Canceled) && s.onFailure != nil && s.onFailure() {
			return
		}
		// The RPC server remains available after an unexpected client exit.
		// Keep the gate closed until an explicit replacement succeeds.
		s.gate.block()
		s.current = nil
		s.active = nil
		close(s.changed)
		s.changed = make(chan struct{})
	}()
}

func (s *clientSupervisor) restart(ctx context.Context) error {
	return s.restarts.do(ctx, func() error {
		s.mu.Lock()
		s.restarting = true
		old := s.current
		s.mu.Unlock()
		next, err := replaceClient(s.ctx, s.gate, old, s.grace, s.factory, s.prepare)
		s.mu.Lock()
		s.current = next
		if next == nil {
			s.active = nil
		} else {
			s.gate.mu.Lock()
			s.active = s.gate.client
			s.gate.mu.Unlock()
		}
		close(s.changed)
		s.changed = make(chan struct{})
		s.restarting = false
		s.mu.Unlock()
		if err == nil {
			s.watch(next)
		}
		return err
	})
}

func (s *clientSupervisor) wait(ctx context.Context) error {
	s.restarts.mu.Lock()
	call := s.restarts.current
	s.restarts.mu.Unlock()
	if call != nil {
		select {
		case <-call.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	run := s.current
	s.mu.Unlock()
	if run == nil {
		s.watchers.Wait()
		return nil
	}
	select {
	case <-run.done:
		s.watchers.Wait()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// acquireReady follows replacements while waiting for the first usable client.
func (s *clientSupervisor) acquireReady(ctx context.Context) (context.Context, Telegram, func(), error) {
	for {
		s.mu.Lock()
		client, changed := s.active, s.changed
		s.mu.Unlock()
		if client == nil {
			select {
			case <-ctx.Done():
				return nil, nil, nil, ctx.Err()
			case <-changed:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, nil, nil, ctx.Err()
		case <-changed:
			continue
		case <-client.Ready():
		}
		leaseCtx, leased, release, err := s.gate.acquire(ctx)
		if err == nil && leased == client {
			return leaseCtx, leased, release, nil
		}
		if err == nil {
			release()
		}
		select {
		case <-ctx.Done():
			return nil, nil, nil, ctx.Err()
		case <-changed:
		}
	}
}

func (s *clientSupervisor) offline() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current == nil && !s.restarting
}
