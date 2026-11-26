package memcache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSlidingWindowLimiterAntiBruteForceClockAdvanceAndMaxKeys(t *testing.T) {
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

	lim := NewSlidingWindowLimiter(LimiterOptions{
		Limit:   5,
		Window:  15 * time.Minute,
		Shards:  8,
		MaxKeys: 32,
	})
	lim.nowFn = getNow

	// First 5 failures are allowed (recorded), 6th and Peek are blocked
	for i := range 5 {
		dec := lim.RecordFailure("acme:alice")
		if !dec.Allowed {
			t.Fatalf("failure #%d should be allowed before reaching limit 5", i+1)
		}
	}

	peek := lim.Peek("acme:alice")
	if peek.Allowed || peek.RetryAfter <= 0 {
		t.Fatalf("Peek after 5 failures = %+v, want Allowed=false & RetryAfter>0", peek)
	}
	sixth := lim.RecordFailure("acme:alice")
	if sixth.Allowed || sixth.RetryAfter <= 0 {
		t.Fatalf("6th RecordFailure = %+v, want Allowed=false & RetryAfter>0", sixth)
	}

	// Reset immediately unlocks
	lim.Reset("acme:alice")
	if dec := lim.Peek("acme:alice"); !dec.Allowed || dec.Remaining != 5 {
		t.Fatalf("Peek after Reset = %+v, want Allowed=true Remaining=5", dec)
	}

	// Lock again: at exact 15m boundary weighted sliding window still prevents burst (prevCount=5, weight=1.0),
	// then at +3m (18m total, weight=0.8 -> effective=4.0) unlocks automatically.
	for range 5 {
		lim.RecordFailure("acme:bob")
	}
	if lim.Peek("acme:bob").Allowed {
		t.Fatalf("expected bob locked after 5 failures")
	}
	advance(15 * time.Minute)
	if dec := lim.Allow("acme:bob"); dec.Allowed {
		t.Fatalf("weighted sliding window should still block burst at exact 15m boundary, got %+v", dec)
	}
	advance(3 * time.Minute)
	if dec := lim.Allow("acme:bob"); !dec.Allowed {
		t.Fatalf("expected bob unlocked once weighted window decays (18m), got %+v", dec)
	}

	// Flooding > MaxKeys distinct IPs must keep memory strictly capped
	for i := range 200 {
		lim.Allow(fmt.Sprintf("ip:198.51.100.%d", i))
	}
	totalTracked := 0
	for _, s := range lim.shards {
		s.mu.Lock()
		totalTracked += len(s.entries)
		s.mu.Unlock()
	}
	if totalTracked > 32 {
		t.Fatalf("tracked keys = %d, want <= MaxKeys (32)", totalTracked)
	}

	// PurgeExpired clears stale entries after 2x window
	advance(31 * time.Minute)
	purged := lim.PurgeExpired()
	if purged == 0 {
		t.Fatalf("expected PurgeExpired to remove expired entries")
	}

	// Default options, AllowN, full >=2*window reset, and stale-entry eviction path
	defLim := NewSlidingWindowLimiter(LimiterOptions{Limit: 4, Window: time.Minute, Shards: 1, MaxKeys: 2})
	defLim.nowFn = getNow
	if dec := defLim.AllowN("k1", 3); !dec.Allowed || dec.Remaining != 1 {
		t.Fatalf("AllowN(k1, 3) = %+v, want Allowed=true Remaining=1", dec)
	}
	if dec := defLim.AllowN("k2", -1); !dec.Allowed {
		t.Fatalf("AllowN(k2, -1) should be treated as 0, got %+v", dec)
	}
	defLim.Allow("k2")
	// Advance >= 2*window: k1/k2 become stale so inserting k3 evicts stale entries first
	advance(3 * time.Minute)
	if dec := defLim.Allow("k3"); !dec.Allowed || dec.Remaining != 3 {
		t.Fatalf("Allow(k3 after stale eviction) = %+v, want Allowed=true Remaining=3", dec)
	}
	// Reset existing entry after 2*window directly via evaluate
	advance(3 * time.Minute)
	if dec := defLim.Allow("k3"); !dec.Allowed || dec.Remaining != 3 {
		t.Fatalf("Allow(k3 after 2x window reset) = %+v, want Allowed=true Remaining=3", dec)
	}
}
