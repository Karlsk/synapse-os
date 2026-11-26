package eventbus

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/tenant"
	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

type sampleEvent struct {
	ID string
}

func TestEventBusSyncOrderPanicIsolationAndUnsubscribe(t *testing.T) {
	t.Parallel()

	var panicEvent string
	var panicVal any
	b := New(WithOnPanic(func(eventName string, recovered any, stack []byte) {
		if len(stack) > 0 {
			panicEvent = eventName
			panicVal = recovered
		}
	}))

	var order []string
	unsub1 := Subscribe[sampleEvent](b, func(_ context.Context, ev sampleEvent) {
		order = append(order, "sub1:"+ev.ID)
		panic("subscriber-boom")
	})
	Subscribe[sampleEvent](b, func(_ context.Context, ev sampleEvent) {
		order = append(order, "sub2:"+ev.ID)
	})

	Publish(context.Background(), b, sampleEvent{ID: "e1"})
	if !slices.Equal(order, []string{"sub1:e1", "sub2:e1"}) {
		t.Fatalf("sync order = %v, want [sub1:e1 sub2:e1]", order)
	}
	if panicVal != "subscriber-boom" || panicEvent == "" {
		t.Fatalf("expected onPanic callback, got event=%q val=%v", panicEvent, panicVal)
	}

	// Unsubscribe sub1 and verify only sub2 fires
	unsub1()
	order = nil
	Publish(context.Background(), b, sampleEvent{ID: "e2"})
	if !slices.Equal(order, []string{"sub2:e2"}) {
		t.Fatalf("after unsubscribe order = %v, want [sub2:e2]", order)
	}
}

func TestEventBusPublishAsyncDetachConcurrencyCapAndDrain(t *testing.T) {
	t.Parallel()

	b := New(WithMaxAsyncConcurrency(2))

	var curActive atomic.Int32
	var maxActive atomic.Int32
	var mu sync.Mutex
	var seenTenant, seenReqID string
	var handled atomic.Int32

	for range 6 {
		Subscribe[sampleEvent](b, func(ctx context.Context, ev sampleEvent) {
			c := curActive.Add(1)
			for {
				prev := maxActive.Load()
				if c <= prev || maxActive.CompareAndSwap(prev, c) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)

			cid, err := tenant.Require(ctx)
			if err == nil {
				mu.Lock()
				seenTenant = cid
				seenReqID = obs.RequestIDFrom(ctx)
				mu.Unlock()
			}
			handled.Add(1)
			curActive.Add(-1)
		})
	}

	parent, cancel := context.WithCancel(context.Background())
	ctx, err := tenant.WithCompany(parent, "acme")
	if err != nil {
		t.Fatalf("WithCompany: %v", err)
	}
	ctx = obs.WithRequestID(ctx, "req-async-9")

	PublishAsync(ctx, b, sampleEvent{ID: "async-1"})
	cancel() // Cancel parent immediately after PublishAsync

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer drainCancel()
	if err := b.Drain(drainCtx); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}

	if handled.Load() != 6 {
		t.Fatalf("handled = %d, want 6", handled.Load())
	}
	if maxActive.Load() > 2 {
		t.Fatalf("MaxAsyncConcurrency violated: maxActive=%d > 2", maxActive.Load())
	}
	if seenTenant != "acme" || seenReqID != "req-async-9" {
		t.Fatalf("detached async context lost metadata: tenant=%q reqID=%q", seenTenant, seenReqID)
	}

	// Verify WithLogger, nested Publish (no deadlock), no-subscriber no-op, and Drain timeout
	type otherEvent struct{ N int }
	b2 := New(WithLogger(slog.Default()))
	Publish(context.Background(), b2, otherEvent{N: 1})
	PublishAsync(context.Background(), b2, otherEvent{N: 2})

	nestedFired := false
	Subscribe[sampleEvent](b2, func(ctx context.Context, _ sampleEvent) {
		Publish(ctx, b2, otherEvent{N: 99})
	})
	Subscribe[otherEvent](b2, func(_ context.Context, ev otherEvent) {
		if ev.N == 99 {
			nestedFired = true
		}
	})
	Publish(context.Background(), b2, sampleEvent{ID: "nested"})
	if !nestedFired {
		t.Fatalf("nested Publish inside subscriber did not fire")
	}

	blockCh := make(chan struct{})
	b3 := New()
	Subscribe[otherEvent](b3, func(_ context.Context, _ otherEvent) {
		<-blockCh
	})
	PublishAsync(context.Background(), b3, otherEvent{N: 1})
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer shortCancel()
	if err := b3.Drain(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain timeout = %v, want DeadlineExceeded", err)
	}
	close(blockCh)
	if err := b3.Drain(context.Background()); err != nil {
		t.Fatalf("Drain after unblock = %v", err)
	}
}

func TestEventBusConcurrentPublishAsyncNestedAsyncAndDrainNoRace(t *testing.T) {
	t.Parallel()

	type childEvent struct{ ID string }
	b := New(WithMaxAsyncConcurrency(8))

	var childCount atomic.Int32
	Subscribe[sampleEvent](b, func(ctx context.Context, ev sampleEvent) {
		PublishAsync(ctx, b, childEvent{ID: ev.ID})
	})
	Subscribe[childEvent](b, func(_ context.Context, _ childEvent) {
		childCount.Add(1)
	})

	const publishers = 8
	const perPub = 20
	var pubWG sync.WaitGroup
	var drainWG sync.WaitGroup

	stopDrain := make(chan struct{})
	for range 4 {
		drainWG.Add(1)
		go func() {
			defer drainWG.Done()
			for {
				select {
				case <-stopDrain:
					return
				default:
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
					_ = b.Drain(ctx)
					cancel()
				}
			}
		}()
	}

	for range publishers {
		pubWG.Add(1)
		go func() {
			defer pubWG.Done()
			for range perPub {
				PublishAsync(context.Background(), b, sampleEvent{ID: "x"})
			}
		}()
	}

	pubWG.Wait()
	close(stopDrain)
	drainWG.Wait()

	if err := b.Drain(nil); err != nil {
		t.Fatalf("final Drain(nil) error = %v", err)
	}
	if got := childCount.Load(); got != publishers*perPub {
		t.Fatalf("childCount = %d, want %d", got, publishers*perPub)
	}
}
