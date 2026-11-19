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
	audit05Recv(t, old.started, "old client start")
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
	newRun := audit05Recv(t, result, "replacement run")
	if err := audit05Recv(t, errch, "replacement result"); err != nil {
		t.Fatal(err)
	}
	audit05Recv(t, next.started, "replacement start")
	_, client, release, err = gate.acquire(ctx)
	if err != nil || client != next {
		t.Fatalf("replacement acquire: %v", err)
	}
	release()
	newRun.cancel()
	audit05Recv(t, newRun.done, "replacement run exit")
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
	audit05Recv(t, entered, "first restart start")
	// The second caller joins the running restart with a live context and is
	// canceled while it waits.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The context reports the moment the joined caller starts waiting on it.
	waitCtx := &audit05SignalContext{Context: ctx, waiting: make(chan struct{})}
	waiter := make(chan error, 1)
	secondStarted := make(chan struct{}, 1)
	go func() {
		waiter <- r.do(waitCtx, func() error { calls.Add(1); secondStarted <- struct{}{}; return nil })
	}()
	audit05Recv(t, waitCtx.waiting, "the second caller to wait for the running restart")
	select {
	case err := <-waiter:
		t.Fatalf("joining caller returned before the restart finished: %v", err)
	default:
	}
	cancel()
	if err := audit05Recv(t, waiter, "canceled waiter"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	select {
	case err := <-finished:
		t.Fatalf("waiter cancellation ended the running restart: %v", err)
	case <-secondStarted:
		t.Fatal("the second caller started its own restart instead of joining")
	case <-time.After(50 * time.Millisecond):
	}
	close(unblock)
	if err := audit05Recv(t, finished, "first restart result"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("performed %d restarts", calls.Load())
	}
}

func TestAudit05_RestarterCanceledCallerDoesNotStartRestart(t *testing.T) {
	var r clientRestarter
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	if err := r.do(ctx, func() error { calls.Add(1); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("a canceled caller started %d restarts", calls.Load())
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
	audit05Recv(t, old.started, "old client start")
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
	audit05Recv(t, old.entered, "write start")
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
	if err := audit05Recv(t, writeDone, "write result"); err != nil {
		t.Fatal(err)
	}
	newRun := audit05Recv(t, result, "replacement run")
	if err := audit05Recv(t, errch, "replacement result"); err != nil {
		t.Fatal(err)
	}
	if old.writes.Load() != 1 {
		t.Fatalf("write sent %d times", old.writes.Load())
	}
	newRun.cancel()
	audit05Recv(t, newRun.done, "replacement run exit")
}

func TestSupervisorDoesNotReportIntentionalStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	next := &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}
	gate := &telegramAdmission{client: old}
	// The exit watcher consults ignoreExit only for a run it treats as an
	// unexpected failure; a deliberate replacement must never get that far.
	var unexpected atomic.Int32
	ignoreExit := func() bool { unexpected.Add(1); return false }
	s := newClientSupervisor(ctx, gate, old, func(context.Context) (Telegram, error) { return next, nil }, func(Telegram) {}, ignoreExit, nil, nil, time.Second)
	audit05Recv(t, old.started, "old client start")
	if err := s.restart(ctx); err != nil {
		t.Fatal(err)
	}
	audit05Recv(t, next.started, "replacement start")
	// An intentional stop must leave the supervisor online and the gate
	// open for the replacement.
	if s.offline() || unexpected.Load() != 0 {
		t.Fatalf("intentional stop treated as a failure: offline=%v unexpected=%d", s.offline(), unexpected.Load())
	}
	_, client, release, err := gate.acquire(ctx)
	if err != nil || client != next {
		t.Fatalf("acquire after restart: client=%v err=%v", client, err)
	}
	release()
	cancel()
	audit05Recv(t, next.exited, "replacement exit")
}

func TestSupervisorAcquireReadyFollowsReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &audit05ReadyProbe{restartFake: &restartFake{fakeTelegram: newFake(), started: make(chan struct{}), exited: make(chan struct{})}, consulted: make(chan struct{})}
	next := newFake()
	gate := &telegramAdmission{client: old}
	s := newClientSupervisor(ctx, gate, old, func(context.Context) (Telegram, error) { return next, nil }, func(Telegram) {}, nil, nil, nil, time.Second)
	audit05Recv(t, old.started, "old client start")
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
	// Replace only after the caller has picked the old client and waits for
	// its readiness; the old client never becomes ready.
	audit05Recv(t, old.consulted, "acquireReady to wait on the old client")
	if err := s.restart(ctx); err != nil {
		t.Fatal(err)
	}
	if client := audit05Recv(t, got, "acquireReady result"); client != next {
		t.Fatal("did not follow replacement")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), audit05Timeout)
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
	deadline := time.After(audit05Timeout)
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
	audit05Recv(t, entered, "first restart start")
	go func() { second <- r.do(context.Background(), perform) }()
	select {
	case <-second:
		t.Fatal("second restart did not wait")
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	if err := audit05Recv(t, first, "first restart result"); err != nil {
		t.Fatal(err)
	}
	if err := audit05Recv(t, second, "second restart result"); err != nil {
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
	audit05Recv(t, first.started, "first client start")
	for i := 0; i < 20; i++ {
		if err := s.restart(ctx); err != nil {
			t.Fatal(err)
		}
		audit05Recv(t, current.started, "replacement start")
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
	audit05Recv(t, entered, "replacement start")
	cancel()
	select {
	case err := <-callerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(audit05Timeout):
		t.Fatal("canceled caller did not return")
	}
	close(finish)
	select {
	case <-workerDone:
	case <-time.After(audit05Timeout):
		t.Fatal("replacement did not finish")
	}
}
