// Package lifecycle provides panic-isolated background goroutine management and
// staged 30-second reverse-order graceful shutdown orchestration.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Loop represents a named long-running background service managed by BackgroundGroup.
type Loop interface {
	Name() string
	Run(ctx context.Context) error
}

// BackgroundGroup manages background goroutines with panic isolation, active
// tracking, post-stop rejection, and bounded timeout diagnostics on shutdown.
type BackgroundGroup struct {
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	active  atomic.Int64
	logger  *slog.Logger
	onPanic func(name string, recovered any, stack []byte)

	mu      sync.Mutex
	stopped bool
	running map[string]int
	errs    []error
}

// GroupOption customizes a BackgroundGroup during construction.
type GroupOption func(*BackgroundGroup)

// WithLogger sets the structured logger used by BackgroundGroup.
func WithLogger(l *slog.Logger) GroupOption {
	return func(g *BackgroundGroup) {
		if l != nil {
			g.logger = l
		}
	}
}

// WithOnPanic sets an optional callback invoked whenever a managed goroutine panics.
func WithOnPanic(fn func(name string, recovered any, stack []byte)) GroupOption {
	return func(g *BackgroundGroup) {
		g.onPanic = fn
	}
}

// NewBackgroundGroup constructs a BackgroundGroup derived from parent.
func NewBackgroundGroup(parent context.Context, opts ...GroupOption) *BackgroundGroup {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	g := &BackgroundGroup{
		ctx:     ctx,
		cancel:  cancel,
		logger:  slog.Default(),
		running: make(map[string]int),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g
}

// Context returns the group's shared cancellation context.
func (g *BackgroundGroup) Context() context.Context {
	return g.ctx
}

// Go spawns a named managed goroutine with panic recovery. Calls after Stop are rejected.
func (g *BackgroundGroup) Go(name string, fn func(ctx context.Context) error) {
	if fn == nil {
		return
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		g.logger.Warn("background task rejected after group stopped", "task", name)
		return
	}
	g.running[name]++
	g.mu.Unlock()

	g.active.Add(1)
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer g.active.Add(-1)
		defer func() {
			g.mu.Lock()
			if g.running[name] <= 1 {
				delete(g.running, name)
			} else {
				g.running[name]--
			}
			g.mu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				g.logger.ErrorContext(g.ctx, "background goroutine panicked",
					"task", name, "panic", r, "stack", string(stack))
				if g.onPanic != nil {
					g.onPanic(name, r, stack)
				}
			}
		}()

		g.logger.InfoContext(g.ctx, "background task started", "task", name)
		if err := fn(g.ctx); err != nil && !errors.Is(err, context.Canceled) {
			g.logger.ErrorContext(g.ctx, "background task exited with error", "task", name, "err", err)
			g.recordErr(fmt.Errorf("task %s: %w", name, err))
		} else {
			g.logger.InfoContext(g.ctx, "background task stopped", "task", name)
		}
	}()
}

// GoLoop launches a Loop implementation inside the BackgroundGroup.
func (g *BackgroundGroup) GoLoop(loop Loop) {
	if loop == nil {
		return
	}
	g.Go(loop.Name(), loop.Run)
}

// Active returns the current count of running managed goroutines.
func (g *BackgroundGroup) Active() int {
	return int(g.active.Load())
}

// Stop marks the group stopped, cancels its context, and waits for all tasks to
// exit until ctx expires. On timeout it returns an error wrapping ctx.Err() with
// the sorted list of tasks that did not exit in time.
func (g *BackgroundGroup) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()

	g.cancel()

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		g.mu.Lock()
		errs := slices.Clone(g.errs)
		g.mu.Unlock()
		return errors.Join(errs...)
	case <-ctx.Done():
		stuck := g.runningNames()
		return fmt.Errorf("background group stop timed out with remaining tasks [%s]: %w",
			strings.Join(stuck, ", "), ctx.Err())
	}
}

func (g *BackgroundGroup) recordErr(err error) {
	g.mu.Lock()
	g.errs = append(g.errs, err)
	g.mu.Unlock()
}

func (g *BackgroundGroup) runningNames() []string {
	g.mu.Lock()
	names := make([]string, 0, len(g.running))
	for name := range g.running {
		names = append(names, name)
	}
	g.mu.Unlock()
	slices.Sort(names)
	return names
}
