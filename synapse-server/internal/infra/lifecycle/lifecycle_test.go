package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testLoop struct {
	name string
	run  func(ctx context.Context) error
}

func (l testLoop) Name() string                  { return l.name }
func (l testLoop) Run(ctx context.Context) error { return l.run(ctx) }

func TestBackgroundGroupPanicIsolationAndGracefulStop(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	panicCh := make(chan string, 1)
	g := NewBackgroundGroup(
		context.Background(),
		WithLogger(logger),
		WithOnPanic(func(name string, recovered any, stack []byte) {
			if recovered == "boom" && len(stack) > 0 {
				panicCh <- name
			}
		}),
	)

	siblingStarted := make(chan struct{})
	var siblingExited atomic.Bool

	g.Go("panicking-worker", func(ctx context.Context) error {
		panic("boom")
	})

	g.GoLoop(testLoop{
		name: "healthy-loop",
		run: func(ctx context.Context) error {
			close(siblingStarted)
			<-ctx.Done()
			siblingExited.Store(true)
			return ctx.Err()
		},
	})

	select {
	case name := <-panicCh:
		if name != "panicking-worker" {
			t.Fatalf("onPanic name = %q, want panicking-worker", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for panic recovery callback")
	}

	<-siblingStarted
	if g.Active() < 1 {
		t.Fatalf("expected healthy sibling to remain active, got %d", g.Active())
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := g.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() returned unexpected error: %v", err)
	}
	if !siblingExited.Load() {
		t.Fatalf("expected healthy-loop to exit cleanly on Stop")
	}
	if g.Active() != 0 {
		t.Fatalf("Active() after Stop = %d, want 0", g.Active())
	}

	// Submitting after Stop must be safely rejected
	var ranAfterStop atomic.Bool
	g.Go("late-worker", func(ctx context.Context) error {
		ranAfterStop.Store(true)
		return nil
	})
	if ranAfterStop.Load() {
		t.Fatalf("task submitted after Stop should not run")
	}
}

func TestBackgroundGroupStopTimeoutListsStuckTasks(t *testing.T) {
	t.Parallel()

	g := NewBackgroundGroup(context.Background())
	release := make(chan struct{})
	started := make(chan struct{})

	g.Go("stuck-worker", func(_ context.Context) error {
		close(started)
		<-release
		return nil
	})
	<-started

	stopCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	err := g.Stop(stopCtx)
	close(release) // clean up goroutine after asserting timeout

	if err == nil {
		t.Fatalf("expected timeout error from Stop(), got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected error wrapping DeadlineExceeded, got: %v", err)
	}
	if !strings.Contains(err.Error(), "stuck-worker") {
		t.Fatalf("expected error to list stuck task name, got: %v", err)
	}
}

func TestBackgroundGroupCollectsWorkerErrors(t *testing.T) {
	t.Parallel()

	g := NewBackgroundGroup(context.Background())
	wantErr := errors.New("worker failed")
	g.Go("failing-worker", func(ctx context.Context) error {
		return wantErr
	})

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := g.Stop(stopCtx)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want wrapped %v", err, wantErr)
	}
}

func TestShutdownManagerPhaseOrderAndIntraPhaseLIFO(t *testing.T) {
	t.Parallel()

	sm := NewShutdownManager(5*time.Second, slog.Default())

	var mu sync.Mutex
	var order []string
	record := func(name string) func(context.Context) error {
		return func(_ context.Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}
	}

	// Register out of phase order and with multiple hooks in the same phase
	sm.Register(PhaseFlush, "flush-1", time.Second, record("flush-1"))
	sm.Register(PhaseStorage, "pg-pool", time.Second, record("pg-pool"))
	sm.Register(PhaseBackground, "bg-first", time.Second, record("bg-first"))
	sm.Register(PhaseBackground, "bg-second", time.Second, record("bg-second"))
	sm.Register(PhaseHTTP, "http-server", time.Second, record("http-server"))
	sm.Register(PhaseConnections, "conn-first", time.Second, record("conn-first"))
	sm.Register(PhaseConnections, "conn-second", time.Second, record("conn-second"))

	if err := sm.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	want := []string{
		"http-server",
		"bg-second",
		"bg-first",
		"conn-second",
		"conn-first",
		"pg-pool",
		"flush-1",
	}
	if !slices.Equal(order, want) {
		t.Fatalf("shutdown order = %v, want %v", order, want)
	}
}

func TestShutdownManagerBudgetClampingAndExhaustion(t *testing.T) {
	t.Parallel()

	sm := NewShutdownManager(200*time.Millisecond, slog.Default())

	var slowExceeded atomic.Bool
	var skippedRan atomic.Bool

	sm.Register(PhaseHTTP, "slow-http", 5*time.Second, func(ctx context.Context) error {
		<-ctx.Done()
		slowExceeded.Store(true)
		return ctx.Err()
	})
	sm.Register(PhaseStorage, "never-reached-pg", time.Second, func(ctx context.Context) error {
		skippedRan.Store(true)
		return nil
	})

	start := time.Now()
	err := sm.Shutdown(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error on budget exhaustion, got nil")
	}
	if !slowExceeded.Load() {
		t.Fatalf("slow hook context was not clamped to total budget")
	}
	if elapsed > 600*time.Millisecond {
		t.Fatalf("Shutdown took %v, expected <= ~200ms total budget", elapsed)
	}
	if !strings.Contains(err.Error(), "slow-http") {
		t.Fatalf("expected error to mention slow-http, got: %v", err)
	}
	if !skippedRan.Load() && !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatalf("expected budget exhausted error for skipped hook, got: %v", err)
	}
}

func TestNotifySignalContextCancelsCleanly(t *testing.T) {
	t.Parallel()

	ctx, stop := NotifySignalContext(context.Background())
	defer stop()

	if ctx.Err() != nil {
		t.Fatalf("fresh signal context should not be canceled")
	}
	stop()
}

func TestBackgroundGroupConcurrentGoAndStopNoWaitGroupRace(t *testing.T) {
	t.Parallel()

	for range 50 {
		g := NewBackgroundGroup(context.Background())
		var callers sync.WaitGroup
		for range 8 {
			callers.Add(1)
			go func() {
				defer callers.Done()
				g.Go("racing-worker", func(ctx context.Context) error {
					<-ctx.Done()
					return ctx.Err()
				})
			}()
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := g.Stop(stopCtx)
		cancel()
		callers.Wait()
		if err != nil {
			t.Fatalf("Stop() returned unexpected error: %v", err)
		}
		if g.Active() != 0 {
			t.Fatalf("Active() after Stop = %d, want 0", g.Active())
		}
	}
}

func TestShutdownManagerRecoversPanicAndBoundsNonCooperativeHook(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sm := NewShutdownManager(2*time.Second, logger)

	release := make(chan struct{})
	defer close(release)

	var storageClosed atomic.Bool

	sm.Register(PhaseHTTP, "panicking-http", 200*time.Millisecond, func(_ context.Context) error {
		panic("unexpected-http-panic")
	})
	sm.Register(PhaseBackground, "cooperative-bg", 40*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("stuck tasks [worker-a]: " + ctx.Err().Error())
	})
	sm.Register(PhaseConnections, "stuck-conn", 60*time.Millisecond, func(_ context.Context) error {
		// Deliberately ignores ctx.Done()
		<-release
		return nil
	})
	sm.Register(PhaseStorage, "pg-pool", 200*time.Millisecond, func(_ context.Context) error {
		storageClosed.Store(true)
		return nil
	})

	start := time.Now()
	err := sm.Shutdown(context.Background())
	elapsed := time.Since(start)

	if !storageClosed.Load() {
		t.Fatalf("PhaseStorage hook did not run after earlier panic/timeout")
	}
	if elapsed > 600*time.Millisecond {
		t.Fatalf("Shutdown blocked too long on non-cooperative hook: %v", elapsed)
	}
	if err == nil ||
		!strings.Contains(err.Error(), "unexpected-http-panic") ||
		!strings.Contains(err.Error(), "stuck tasks [worker-a]") ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want panic + cooperative diagnostic + DeadlineExceeded", err)
	}
}
