package memcache

import (
	"container/list"
	"hash/maphash"
	"sync"
	"time"
)

const (
	defaultLimiterShards  = 32
	defaultLimiterMaxKeys = 100_000
)

// LimiterOptions configures a SlidingWindowLimiter.
type LimiterOptions struct {
	Limit   int
	Window  time.Duration
	Shards  int
	MaxKeys int
}

// Decision describes the outcome of a rate-limit check or recording call.
type Decision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
	ResetAt    time.Time
}

type windowEntry struct {
	key         string
	prevCount   int
	currCount   int
	windowStart time.Time
	lastSeen    time.Time
	elem        *list.Element
}

type limiterShard struct {
	mu      sync.Mutex
	entries map[string]*windowEntry
	lru     *list.List
}

// SlidingWindowLimiter is a pure in-process sharded O(1) two-window weighted
// sliding-window rate limiter designed for login anti-brute-force protection
// with a hard MaxKeys cap against memory exhaustion.
type SlidingWindowLimiter struct {
	shards      []*limiterShard
	mask        uint64
	seed        maphash.Seed
	limit       int
	window      time.Duration
	maxPerShard int
	nowFn       func() time.Time
}

// NewSlidingWindowLimiter constructs a SlidingWindowLimiter from opts.
func NewSlidingWindowLimiter(opts LimiterOptions) *SlidingWindowLimiter {
	limit := opts.Limit
	if limit <= 0 {
		limit = 1
	}
	window := opts.Window
	if window <= 0 {
		window = time.Minute
	}
	shardCount := opts.Shards
	if shardCount <= 0 {
		shardCount = defaultLimiterShards
	}
	shardCount = nextPow2(shardCount)

	maxKeys := opts.MaxKeys
	if maxKeys <= 0 {
		maxKeys = defaultLimiterMaxKeys
	}
	for shardCount > 1 && maxKeys < shardCount {
		shardCount >>= 1
	}
	maxPerShard := maxKeys / shardCount
	if maxPerShard < 1 {
		maxPerShard = 1
	}

	shards := make([]*limiterShard, shardCount)
	for i := range shards {
		shards[i] = &limiterShard{
			entries: make(map[string]*windowEntry),
			lru:     list.New(),
		}
	}
	return &SlidingWindowLimiter{
		shards:      shards,
		mask:        uint64(shardCount - 1),
		seed:        maphash.MakeSeed(),
		limit:       limit,
		window:      window,
		maxPerShard: maxPerShard,
		nowFn:       time.Now,
	}
}

// Allow checks and consumes 1 event for key.
func (l *SlidingWindowLimiter) Allow(key string) Decision {
	return l.AllowN(key, 1)
}

// AllowN checks and consumes n events for key when permitted.
func (l *SlidingWindowLimiter) AllowN(key string, n int) Decision {
	if n < 0 {
		n = 0
	}
	return l.evaluate(l.shardFor(key), key, l.nowFn(), n)
}

// Peek performs a read-only check of whether key is currently locked without
// incrementing its counter.
func (l *SlidingWindowLimiter) Peek(key string) Decision {
	return l.evaluate(l.shardFor(key), key, l.nowFn(), 0)
}

// RecordFailure records a single authentication failure for key.
func (l *SlidingWindowLimiter) RecordFailure(key string) Decision {
	return l.AllowN(key, 1)
}

// Reset clears the failure counter for key immediately (e.g., on successful login).
func (l *SlidingWindowLimiter) Reset(key string) {
	s := l.shardFor(key)
	s.mu.Lock()
	if ent, ok := s.entries[key]; ok {
		if ent.elem != nil {
			s.lru.Remove(ent.elem)
		}
		delete(s.entries, key)
	}
	s.mu.Unlock()
}

// PurgeExpired removes entries whose counters have completely expired (>= 2*window).
func (l *SlidingWindowLimiter) PurgeExpired() int {
	now := l.nowFn()
	removed := 0
	cutoff := 2 * l.window
	for _, s := range l.shards {
		s.mu.Lock()
		for k, ent := range s.entries {
			if now.Sub(ent.windowStart) >= cutoff {
				if ent.elem != nil {
					s.lru.Remove(ent.elem)
				}
				delete(s.entries, k)
				removed++
			}
		}
		s.mu.Unlock()
	}
	return removed
}

func (l *SlidingWindowLimiter) shardFor(key string) *limiterShard {
	h := maphash.String(l.seed, key)
	return l.shards[h&l.mask]
}

func (l *SlidingWindowLimiter) evaluate(s *limiterShard, key string, now time.Time, add int) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()

	ent, ok := s.entries[key]
	if !ok {
		if add == 0 {
			return Decision{
				Allowed:   true,
				Remaining: l.limit,
				ResetAt:   now.Add(l.window),
			}
		}
		if l.maxPerShard > 0 && len(s.entries) >= l.maxPerShard {
			s.evictOldestLocked()
		}
		ent = &windowEntry{key: key, windowStart: now, lastSeen: now}
		ent.elem = s.lru.PushFront(ent)
		s.entries[key] = ent
	}

	elapsed := now.Sub(ent.windowStart)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed >= 2*l.window {
		ent.prevCount = 0
		ent.currCount = 0
		ent.windowStart = now
		elapsed = 0
	} else if elapsed >= l.window {
		ent.prevCount = ent.currCount
		ent.currCount = 0
		ent.windowStart = ent.windowStart.Add(l.window)
		elapsed = now.Sub(ent.windowStart)
	}

	weight := 1.0 - float64(elapsed)/float64(l.window)
	if weight < 0 {
		weight = 0
	}
	effective := float64(ent.prevCount)*weight + float64(ent.currCount)

	blocked := (add == 0 && effective >= float64(l.limit)) ||
		(add > 0 && effective+float64(add) > float64(l.limit))
	if blocked {
		if add > 0 {
			ent.lastSeen = now
			if ent.elem != nil {
				s.lru.MoveToFront(ent.elem)
			}
		}
		retryAfter := l.window - elapsed
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return Decision{
			Allowed:    false,
			Remaining:  0,
			RetryAfter: retryAfter,
			ResetAt:    ent.windowStart.Add(l.window),
		}
	}

	if add > 0 {
		ent.currCount += add
		ent.lastSeen = now
		if ent.elem != nil {
			s.lru.MoveToFront(ent.elem)
		}
		effective += float64(add)
	}
	rem := l.limit - int(effective)
	if rem < 0 {
		rem = 0
	}
	return Decision{
		Allowed:   true,
		Remaining: rem,
		ResetAt:   ent.windowStart.Add(l.window),
	}
}

func (s *limiterShard) evictOldestLocked() {
	if oldest := s.lru.Back(); oldest != nil {
		evicted := oldest.Value.(*windowEntry)
		s.lru.Remove(oldest)
		delete(s.entries, evicted.key)
	}
}
