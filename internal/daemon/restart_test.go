package daemon

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type restartFake struct {
	*fakeTelegram
	started chan struct{}
	exited  chan struct{}
}

func (f *restartFake) Run(ctx context.Context) error {
	close(f.started)
	<-ctx.Done()
	close(f.exited)
	return ctx.Err()
}

func TestReplaceClientDrainsAndJoins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &restartFake{fakeTelegram: &fakeTelegram{}, started: make(chan struct{}), exited: make(chan struct{})}
	next := &restartFake{fakeTelegram: &fakeTelegram{}, started: make(chan struct{}), exited: make(chan struct{})}
	gate := &telegramAdmission{client: old}
	run := startClientRun(ctx, old)
	<-old.started
	_, client, release, err := gate.acquire(ctx)
	if err != nil || client != old {
		t.Fatalf("acquire: %v", err)
	}
	result := make(chan *clientRun, 1)
	errch := make(chan error, 1)
	go func() {
		r, e := replaceClient(ctx, gate, run, time.Second, func(context.Context) (Telegram, error) {
			select {
			case <-old.exited:
			default:
				t.Error("factory called before old client exited")
			}
			return next, nil
		}, func(Telegram) {})
		result <- r
		errch <- e
	}()
	deadline := time.Now().Add(time.Second)
	for {
		_, _, releaseProbe, e := gate.acquire(ctx)
		if e != nil {
			break
		}
		releaseProbe()
		if time.Now().After(deadline) {
			t.Fatal("restart did not block admissions")
		}
	}
	select {
	case <-old.exited:
		t.Fatal("client stopped with active lease")
	default:
	}
	release()
	newRun := <-result
	if err := <-errch; err != nil {
		t.Fatal(err)
	}
	<-next.started
	_, client, release, err = gate.acquire(ctx)
	if err != nil || client != next {
		t.Fatalf("replacement acquire: %v", err)
	}
	release()
	newRun.cancel()
	<-newRun.done
}

func TestRestarterCoalescesAndWaiterCancels(t *testing.T) {
	var r clientRestarter
	entered := make(chan struct{})
	unblock := make(chan struct{})
	finished := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		finished <- r.do(context.Background(), func() error { calls.Add(1); close(entered); <-unblock; return nil })
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.do(ctx, func() error { calls.Add(1); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	close(unblock)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("performed %d restarts", calls.Load())
	}
}

func TestStatusDuringClientDrain(t *testing.T) {
	d, client, _, _ := accessTestDaemon(t)
	d.login = newLoginManager(context.Background(), client)
	d.opts.NewTelegram = func(context.Context, TelegramConfig) (Telegram, error) { return client, nil }
	d.admission.unblock(client)
	d.admission.block()
	value, err := d.status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	status := value.(rpc.StatusResult)
	if status.ConnectionInfo == nil || status.Connection != "restarting" || status.Connected {
		t.Fatalf("status: %+v", status)
	}
	if err := d.guardRequest(rpc.Request{Method: rpc.MethodSearch}); err != nil {
		t.Fatalf("local search: %v", err)
	}
	var rpcErr *rpc.Error
	if err := d.guardRequest(rpc.Request{Method: rpc.MethodChatList}); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeOffline {
		t.Fatalf("chat list: %v", err)
	}
}

type restartWriteFake struct {
	*restartFake
	entered chan struct{}
	allow   chan struct{}
	writes  atomic.Int32
}

func (f *restartWriteFake) Send(ctx context.Context, _ chatref.Ref, _ rpc.SendParams) (rpc.SendResult, error) {
	f.writes.Add(1)
	close(f.entered)
	select {
	case <-f.allow:
		return rpc.SendResult{ID: 73}, nil
	case <-ctx.Done():
		return rpc.SendResult{}, ctx.Err()
	}
}

func TestReplaceClientWaitsForWriteWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &restartWriteFake{restartFake: &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}, entered: make(chan struct{}), allow: make(chan struct{})}
	next := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	gate := &telegramAdmission{client: old}
	run := startClientRun(ctx, old)
	<-old.started
	writeDone := make(chan error, 1)
	go func() {
		leaseCtx, client, release, err := gate.acquire(ctx)
		if err != nil {
			writeDone <- err
			return
		}
		defer release()
		result, err := client.Send(leaseCtx, chatref.Ref{}, rpc.SendParams{})
		if err == nil && result.ID != 73 {
			err = errors.New("write result lost")
		}
		writeDone <- err
	}()
	<-old.entered
	result := make(chan *clientRun, 1)
	errch := make(chan error, 1)
	go func() {
		r, err := replaceClient(ctx, gate, run, time.Second, func(context.Context) (Telegram, error) { return next, nil }, func(Telegram) {})
		result <- r
		errch <- err
	}()
	// A blocked gate proves replacement has begun before the write completes.
	deadline := time.Now().Add(time.Second)
	for {
		_, _, release, err := gate.acquire(ctx)
		if err != nil {
			break
		}
		release()
		if time.Now().After(deadline) {
			t.Fatal("restart did not block admissions")
		}
	}
	select {
	case <-old.exited:
		t.Fatal("client stopped before write finished")
	default:
	}
	close(old.allow)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	newRun := <-result
	if err := <-errch; err != nil {
		t.Fatal(err)
	}
	if old.writes.Load() != 1 {
		t.Fatalf("write sent %d times", old.writes.Load())
	}
	newRun.cancel()
	<-newRun.done
}

func TestSupervisorDoesNotReportIntentionalStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	next := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	gate := &telegramAdmission{client: old}
	s := newClientSupervisor(ctx, gate, old, func(context.Context) (Telegram, error) { return next, nil }, func(Telegram) {}, nil, nil, nil, time.Second)
	<-old.started
	if err := s.restart(ctx); err != nil {
		t.Fatal(err)
	}
	<-next.started
	select {
	case err := <-s.exits:
		t.Fatalf("intentional stop reported as failure: %v", err)
	default:
	}
	cancel()
	<-next.exited
}

func TestSupervisorAcquireReadyFollowsReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	next := newFake()
	gate := &telegramAdmission{client: old}
	s := newClientSupervisor(ctx, gate, old, func(context.Context) (Telegram, error) { return next, nil }, func(Telegram) {}, nil, nil, nil, time.Second)
	<-old.started
	got := make(chan Telegram, 1)
	go func() {
		_, client, release, err := s.acquireReady(ctx)
		if err != nil {
			got <- nil
			return
		}
		defer release()
		got <- client
	}()
	if err := s.restart(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case client := <-got:
		if client != next {
			t.Fatal("did not follow replacement")
		}
	case <-time.After(time.Second):
		t.Fatal("did not observe replacement")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := s.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorUnexpectedExitGoesOffline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := &failedTCPClient{fakeTelegram: newFake()}
	gate := &telegramAdmission{client: first}
	s := newClientSupervisor(ctx, gate, first, nil, func(Telegram) {}, nil, nil, nil, time.Second)
	deadline := time.After(time.Second)
	for !s.offline() {
		select {
		case <-deadline:
			t.Fatal("client did not go offline")
		default:
			runtime.Gosched()
		}
	}
	if _, _, _, err := gate.acquire(ctx); err == nil {
		t.Fatal("offline client admitted a request")
	}
}

func TestConcurrentRestartWaitsForSameResult(t *testing.T) {
	var r clientRestarter
	var calls atomic.Int32
	entered := make(chan struct{})
	proceed := make(chan struct{})
	perform := func() error { calls.Add(1); close(entered); <-proceed; return nil }
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- r.do(context.Background(), perform) }()
	<-entered
	go func() { second <- r.do(context.Background(), perform) }()
	select {
	case <-second:
		t.Fatal("second restart did not wait")
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("performed %d restarts", calls.Load())
	}
}

func TestSupervisorRepeatedRestartsJoinEveryRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	current := first
	gate := &telegramAdmission{client: first}
	count := 0
	factory := func(context.Context) (Telegram, error) {
		select {
		case <-current.exited:
		default:
			return nil, errors.New("old run still active")
		}
		current = &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
		count++
		return current, nil
	}
	s := newClientSupervisor(ctx, gate, first, factory, func(Telegram) {}, nil, nil, nil, time.Second)
	<-first.started
	for i := 0; i < 20; i++ {
		if err := s.restart(ctx); err != nil {
			t.Fatal(err)
		}
		<-current.started
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := s.wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-current.exited:
	default:
		t.Fatal("last run did not exit")
	}
	if count != 20 {
		t.Fatalf("created %d replacements", count)
	}
}

func TestReplacementFactoryErrorIsRedacted(t *testing.T) {
	ctx := context.Background()
	gate := &telegramAdmission{client: newFake()}
	_, err := replaceClient(ctx, gate, nil, time.Second, func(context.Context) (Telegram, error) {
		return nil, errors.New("synthetic-sensitive-marker")
	}, func(Telegram) {})
	if err == nil || strings.Contains(err.Error(), "synthetic-sensitive-marker") {
		t.Fatalf("unsafe factory error: %v", err)
	}
	if _, _, _, acquireErr := gate.acquire(ctx); acquireErr == nil {
		t.Fatal("failed factory left gate open")
	}
}

func TestRestartCallerCancellationDoesNotAbortReplacement(t *testing.T) {
	var r clientRestarter
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	finish := make(chan struct{})
	workerDone := make(chan struct{})
	callerDone := make(chan error, 1)
	go func() {
		callerDone <- r.do(ctx, func() error { close(entered); <-finish; close(workerDone); return nil })
	}()
	<-entered
	cancel()
	select {
	case err := <-callerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller did not return")
	}
	close(finish)
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("replacement did not finish")
	}
}
