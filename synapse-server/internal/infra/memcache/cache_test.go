package memcache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestShardedCacheTTLEvictionDeleteFuncAndSingleflight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	getNow := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}

	c := NewShardedCache[string, string](CacheOptions{
		Shards:     8,
		MaxEntries: 16,
	})
	c.nowFn = getNow

	// Set, Get, TTL expiry
	c.Set("acme:k1", "v1", 5*time.Second)
	if v, ok := c.Get("acme:k1"); !ok || v != "v1" {
		t.Fatalf("Get(acme:k1) = (%q, %v), want (v1, true)", v, ok)
	}
	advance(6 * time.Second)
	if _, ok := c.Get("acme:k1"); ok {
		t.Fatalf("expected expired key acme:k1 to return miss")
	}

	// MaxEntries eviction keeps Len() <= MaxEntries
	for i := range 100 {
		c.Set(fmt.Sprintf("acme:item-%d", i), "val", time.Minute)
	}
	if got := c.Len(); got > 16 {
		t.Fatalf("Len() = %d, want <= MaxEntries (16)", got)
	}
	if c.Stats().Evictions == 0 {
		t.Fatalf("expected non-zero Evictions after inserting 100 items into MaxEntries=16 cache")
	}

	// DeleteFunc by tenant prefix
	c.Set("beta:only", "keep", time.Minute)
	removed := c.DeleteFunc(func(k string) bool { return strings.HasPrefix(k, "acme:") })
	if removed == 0 {
		t.Fatalf("expected DeleteFunc to remove acme: keys")
	}
	if _, ok := c.Get("beta:only"); !ok {
		t.Fatalf("beta:only should remain after acme: DeleteFunc")
	}

	// 32 goroutines calling GetOrLoad concurrently on same key must invoke loader exactly once
	var loadCalls atomic.Int32
	const workers = 32
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.GetOrLoad(context.Background(), "shared:key", time.Minute, func(_ context.Context) (string, error) {
				loadCalls.Add(1)
				time.Sleep(25 * time.Millisecond)
				return "computed-once", nil
			})
			if err != nil || v != "computed-once" {
				t.Errorf("GetOrLoad = (%q, %v), want (computed-once, nil)", v, err)
			}
		}()
	}
	wg.Wait()
	if got := loadCalls.Load(); got != 1 {
		t.Fatalf("singleflight loader executed %d times, want 1", got)
	}

	// Loader errors must NOT be cached
	wantErr := errors.New("upstream failed")
	if _, err := c.GetOrLoad(context.Background(), "err:key", time.Minute, func(_ context.Context) (string, error) {
		return "", wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("expected loader error, got %v", err)
	}
	if _, ok := c.Get("err:key"); ok {
		t.Fatalf("failed loader result must not be cached")
	}

	// StartJanitor exits cleanly on context cancellation
	jCtx, jCancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.StartJanitor(jCtx, 10*time.Millisecond)
	}()
	time.Sleep(25 * time.Millisecond)
	jCancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("StartJanitor error = %v", err)
	}

	// Update existing key in place, Delete, PurgeExpired, and default/non-string key cache
	c.Set("beta:only", "updated", 5*time.Second)
	if v, ok := c.Get("beta:only"); !ok || v != "updated" {
		t.Fatalf("updated key = (%q, %v), want (updated, true)", v, ok)
	}
	c.Delete("beta:only")
	if _, ok := c.Get("beta:only"); ok {
		t.Fatalf("deleted key still present")
	}
	c.Set("exp:1", "v", 2*time.Second)
	advance(3 * time.Second)
	if purged := c.PurgeExpired(); purged == 0 {
		t.Fatalf("expected PurgeExpired > 0")
	}

	intCache := NewShardedCache[int, string](CacheOptions{MaxEntries: 4})
	if v, err := intCache.GetOrLoad(context.Background(), 42, time.Minute, func(_ context.Context) (string, error) {
		return "forty-two", nil
	}); err != nil || v != "forty-two" {
		t.Fatalf("intCache.GetOrLoad = (%q, %v)", v, err)
	}
	// Fast-path cache hit on GetOrLoad
	if v, err := intCache.GetOrLoad(context.Background(), 42, time.Minute, nil); err != nil || v != "forty-two" {
		t.Fatalf("intCache.GetOrLoad(hit) = (%q, %v)", v, err)
	}
	// Canceled caller waiting on GetOrLoad
	canceledCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	if _, err := intCache.GetOrLoad(canceledCtx, 99, time.Minute, func(_ context.Context) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "slow", nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetOrLoad(canceled) = %v, want context.Canceled", err)
	}
}

func TestShardedCacheGetOrLoadCallerCancelDoesNotPoisonPeer(t *testing.T) {
	t.Parallel()

	c := NewShardedCache[string, string](CacheOptions{Shards: 4, MaxEntries: 16})

	loaderStarted := make(chan struct{})
	releaseLoader := make(chan struct{})
	var calls atomic.Int32

	loader := func(ctx context.Context) (string, error) {
		calls.Add(1)
		close(loaderStarted)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-releaseLoader:
			return "shared-value", nil
		}
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	errChA := make(chan error, 1)
	go func() {
		_, err := c.GetOrLoad(ctxA, "oauth:acme:user1", time.Minute, loader)
		errChA <- err
	}()

	<-loaderStarted

	type resB struct {
		val string
		err error
	}
	resChB := make(chan resB, 1)
	go func() {
		v, err := c.GetOrLoad(context.Background(), "oauth:acme:user1", time.Minute, loader)
		resChB <- resB{val: v, err: err}
	}()

	// Give caller B a moment to attach to the same singleflight call, then cancel A.
	time.Sleep(15 * time.Millisecond)
	cancelA()

	if err := <-errChA; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller A err = %v, want context.Canceled", err)
	}

	close(releaseLoader)

	gotB := <-resChB
	if gotB.err != nil || gotB.val != "shared-value" {
		t.Fatalf("caller B = (%q, %v), want (shared-value, nil)", gotB.val, gotB.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", calls.Load())
	}
	if v, ok := c.Get("oauth:acme:user1"); !ok || v != "shared-value" {
		t.Fatalf("cached value = (%q, %v), want (shared-value, true)", v, ok)
	}
	if v, err := c.GetOrLoad(nil, "nil-ctx-key", time.Minute, func(_ context.Context) (string, error) {
		return "ok", nil
	}); err != nil || v != "ok" {
		t.Fatalf("GetOrLoad(nil) = (%q, %v), want (ok, nil)", v, err)
	}
}
