// Package eventbus provides a compile-time type-safe in-process synchronous and
// asynchronous event bus with single-subscriber panic isolation, detached
// tenant context propagation, bounded async concurrency, and graceful draining.
package eventbus

import (
	"context"
	"log/slog"
	"reflect"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/Karlsk/synapse-os/internal/core/tenant"
)

const defaultMaxAsyncConcurrency = 64

// Option configures a Bus constructed via New.
type Option func(*Bus)

// WithLogger sets the structured logger used when a subscriber panics.
func WithLogger(l *slog.Logger) Option {
	return func(b *Bus) {
		if l != nil {
			b.logger = l
		}
	}
}

// WithMaxAsyncConcurrency caps concurrent asynchronous subscriber executions.
// Values <= 0 are ignored and the default (64) is preserved.
func WithMaxAsyncConcurrency(n int) Option {
	return func(b *Bus) {
		if n > 0 {
			b.maxAsync = n
		}
	}
}

// WithOnPanic registers an optional hook invoked whenever a subscriber panics.
func WithOnPanic(fn func(eventName string, recovered any, stack []byte)) Option {
	return func(b *Bus) {
		b.onPanic = fn
	}
}

type subEntry struct {
	id uint64
	fn func(ctx context.Context, raw any)
}

// Bus dispatches strongly typed domain events to in-process subscribers.
type Bus struct {
	mu       sync.RWMutex
	subs     map[reflect.Type][]subEntry
	nextID   atomic.Uint64
	maxAsync int
	sem      chan struct{}
	asyncMu  sync.Mutex
	inFlight int
	idleCh   chan struct{}
	logger   *slog.Logger
	onPanic  func(eventName string, recovered any, stack []byte)
}

// New constructs an in-process Bus with optional configuration.
func New(opts ...Option) *Bus {
	b := &Bus{
		subs:     make(map[reflect.Type][]subEntry),
		maxAsync: defaultMaxAsyncConcurrency,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	b.sem = make(chan struct{}, b.maxAsync)
	return b
}

// Subscribe registers fn for events of type T and returns an idempotent
// unsubscribe function.
func Subscribe[T any](b *Bus, fn func(ctx context.Context, ev T)) (unsubscribe func()) {
	if b == nil || fn == nil {
		return func() {}
	}
	typ := reflect.TypeFor[T]()
	id := b.nextID.Add(1)
	entry := subEntry{
		id: id,
		fn: func(ctx context.Context, raw any) {
			fn(ctx, raw.(T))
		},
	}

	b.mu.Lock()
	b.subs[typ] = append(b.subs[typ], entry)
	b.mu.Unlock()

	return func() {
		b.unsubscribe(typ, id)
	}
}

// Publish synchronously dispatches ev to all subscribers of type T in
// registration order. Handlers run outside the bus lock so nested Publish calls
// cannot deadlock, and panics in one subscriber never block sibling handlers.
func Publish[T any](ctx context.Context, b *Bus, ev T) {
	if b == nil {
		return
	}
	typ := reflect.TypeFor[T]()
	handlers := b.snapshot(typ)
	for _, h := range handlers {
		b.invokeSafe(ctx, typ.String(), h.fn, ev)
	}
}

// PublishAsync detaches ctx via tenant.Detach (stripping HTTP cancellation and
// DB transaction handles while keeping company_id/request_id/user_id) and
// dispatches ev to all subscribers of type T under bounded concurrency.
func PublishAsync[T any](ctx context.Context, b *Bus, ev T) {
	if b == nil {
		return
	}
	typ := reflect.TypeFor[T]()
	handlers := b.snapshot(typ)
	if len(handlers) == 0 {
		return
	}
	detached := tenant.Detach(ctx)
	evName := typ.String()
	for _, h := range handlers {
		entry := h
		b.beginAsync()
		go func() {
			defer b.endAsync()
			b.sem <- struct{}{}
			defer func() { <-b.sem }()
			b.invokeSafe(detached, evName, entry.fn, ev)
		}()
	}
}

// Drain blocks until all in-flight asynchronous event handlers complete or ctx
// expires. Safe to call concurrently with PublishAsync and repeatedly.
func (b *Bus) Drain(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		b.asyncMu.Lock()
		if b.inFlight == 0 {
			b.asyncMu.Unlock()
			return nil
		}
		ch := b.idleCh
		b.asyncMu.Unlock()

		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (b *Bus) beginAsync() {
	b.asyncMu.Lock()
	b.inFlight++
	if b.inFlight == 1 {
		b.idleCh = make(chan struct{})
	}
	b.asyncMu.Unlock()
}

func (b *Bus) endAsync() {
	b.asyncMu.Lock()
	b.inFlight--
	if b.inFlight == 0 && b.idleCh != nil {
		close(b.idleCh)
		b.idleCh = nil
	}
	b.asyncMu.Unlock()
}

func (b *Bus) snapshot(typ reflect.Type) []subEntry {
	b.mu.RLock()
	orig := b.subs[typ]
	if len(orig) == 0 {
		b.mu.RUnlock()
		return nil
	}
	cp := make([]subEntry, len(orig))
	copy(cp, orig)
	b.mu.RUnlock()
	return cp
}

func (b *Bus) unsubscribe(typ reflect.Type, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[typ]
	for i, e := range list {
		if e.id == id {
			copy(list[i:], list[i+1:])
			list[len(list)-1] = subEntry{}
			list = list[:len(list)-1]
			if len(list) == 0 {
				delete(b.subs, typ)
			} else {
				b.subs[typ] = list
			}
			return
		}
	}
}

func (b *Bus) invokeSafe(ctx context.Context, evName string, fn func(context.Context, any), ev any) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			if b.logger != nil {
				b.logger.ErrorContext(ctx, "eventbus subscriber panicked",
					"event", evName, "panic", r, "stack", string(stack))
			}
			if b.onPanic != nil {
				b.onPanic(evName, r, stack)
			}
		}
	}()
	fn(ctx, ev)
}
