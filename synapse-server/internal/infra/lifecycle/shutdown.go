package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"runtime/debug"
	"slices"
	"sync"
	"syscall"
	"time"
)

// ShutdownPhase orders graceful shutdown stages from lowest value to highest.
type ShutdownPhase int

const (
	PhaseHTTP        ShutdownPhase = 10 // Drain HTTP requests and cancel SSE streams (<=15s)
	PhaseBackground  ShutdownPhase = 20 // Stop background scheduler loops and async work (<=8s)
	PhaseConnections ShutdownPhase = 30 // Close IM WebSockets and CLI warm process pool (<=5s)
	PhaseStorage     ShutdownPhase = 40 // Close PostgreSQL connection pool (<=2s)
	PhaseFlush       ShutdownPhase = 50 // Flush log writers and final resources (<=1s)
)

const defaultShutdownBudget = 30 * time.Second

type shutdownHook struct {
	phase  ShutdownPhase
	seq    int
	name   string
	budget time.Duration
	fn     func(ctx context.Context) error
}

// ShutdownManager executes registered shutdown hooks in strict ascending phase
// order and LIFO order within each phase, bounded by totalBudget.
type ShutdownManager struct {
	totalBudget time.Duration
	logger      *slog.Logger

	mu      sync.Mutex
	nextSeq int
	hooks   []shutdownHook
}

// NewShutdownManager creates a ShutdownManager with the specified total budget
// (defaulting to 30s if <= 0) and logger (defaulting to slog.Default() if nil).
func NewShutdownManager(totalBudget time.Duration, logger *slog.Logger) *ShutdownManager {
	if totalBudget <= 0 {
		totalBudget = defaultShutdownBudget
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ShutdownManager{
		totalBudget: totalBudget,
		logger:      logger,
	}
}

// Register adds a shutdown hook to the given phase with a per-hook timeout budget.
func (m *ShutdownManager) Register(phase ShutdownPhase, name string, budget time.Duration, fn func(ctx context.Context) error) {
	if fn == nil {
		return
	}
	m.mu.Lock()
	m.nextSeq++
	m.hooks = append(m.hooks, shutdownHook{
		phase:  phase,
		seq:    m.nextSeq,
		name:   name,
		budget: budget,
		fn:     fn,
	})
	m.mu.Unlock()
}

// Shutdown runs all registered hooks in ascending phase order and intra-phase LIFO
// order, clamping each hook's timeout to the remaining total budget.
func (m *ShutdownManager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	deadline := time.Now().Add(m.totalBudget)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	m.mu.Lock()
	hooks := slices.Clone(m.hooks)
	m.mu.Unlock()

	slices.SortStableFunc(hooks, func(a, b shutdownHook) int {
		if a.phase != b.phase {
			return int(a.phase) - int(b.phase)
		}
		return b.seq - a.seq
	})

	var errs []error
	for _, h := range hooks {
		rem := time.Until(deadline)
		if rem <= 0 {
			errs = append(errs, fmt.Errorf("shutdown budget exhausted before hook %q", h.name))
			break
		}
		budget := h.budget
		if budget <= 0 || budget > rem {
			budget = rem
		}
		phaseCtx, cancel := context.WithTimeout(context.Background(), budget)
		start := time.Now()
		err := m.runHook(phaseCtx, h)
		cancel()
		if err != nil {
			m.logger.Warn("shutdown hook returned error",
				"phase", h.phase, "hook", h.name, "elapsed_ms", time.Since(start).Milliseconds(), "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", h.name, err))
		} else {
			m.logger.Info("shutdown hook completed",
				"phase", h.phase, "hook", h.name, "elapsed_ms", time.Since(start).Milliseconds())
		}
	}
	return errors.Join(errs...)
}

func (m *ShutdownManager) runHook(ctx context.Context, h shutdownHook) error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("shutdown hook panicked",
					"phase", h.phase, "hook", h.name, "panic", r, "stack", string(debug.Stack()))
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		done <- h.fn(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Give cooperative hooks that wake on <-ctx.Done() (e.g., BackgroundGroup.Stop
		// formatting stuck task names or http.Server.Shutdown closing idle conns) a brief
		// grace window to return their own error before abandoning a deadlocked goroutine.
		grace := time.NewTimer(25 * time.Millisecond)
		defer grace.Stop()
		select {
		case err := <-done:
			return err
		case <-grace.C:
			return ctx.Err()
		}
	}
}

// NotifySignalContext returns a context canceled upon receiving SIGINT or SIGTERM.
func NotifySignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
}
