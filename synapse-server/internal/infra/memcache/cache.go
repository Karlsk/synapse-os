// Package memcache provides pure in-process sharded TTL/LRU caching (with
// singleflight stampede protection) and a bounded O(1) weighted sliding-window
// rate limiter. Zero Redis dependencies.
package memcache

import (
	"container/list"
	"context"
	"fmt"
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const defaultCacheShards = 32

// CacheOptions configures a ShardedCache instance.
type CacheOptions struct {
	Shards          int
	MaxEntries      int
	CleanupInterval time.Duration
}

// CacheStats reports point-in-time counts for a ShardedCache.
type CacheStats struct {
	Entries   int
	Hits      uint64
	Misses    uint64
	Evictions uint64
}

type cacheItem[K comparable, V any] struct {
	key       K
	val       V
	expiresAt time.Time
}

type cacheShard[K comparable, V any] struct {
	mu    sync.Mutex
	items map[K]*list.Element
	order *list.List // front = MRU, back = LRU
}

// ShardedCache is a concurrent, pure in-process sharded TTL/LRU cache with
// singleflight deduplication on cache misses.
type ShardedCache[K comparable, V any] struct {
	shards          []*cacheShard[K, V]
	mask            uint64
	seed            maphash.Seed
	maxPerShard     int
	cleanupInterval time.Duration
	nowFn           func() time.Time

	hits      atomic.Uint64
	misses    atomic.Uint64
	evictions atomic.Uint64

	sf singleflight.Group
}

// NewShardedCache constructs a ShardedCache with shard count rounded up to a
// power of two (default 32). When MaxEntries > 0, each shard enforces a strict
// LRU capacity so Len() never exceeds MaxEntries.
func NewShardedCache[K comparable, V any](opts CacheOptions) *ShardedCache[K, V] {
	shardCount := opts.Shards
	if shardCount <= 0 {
		shardCount = defaultCacheShards
	}
	shardCount = nextPow2(shardCount)

	maxPerShard := 0
	if opts.MaxEntries > 0 {
		// Ensure shardCount <= MaxEntries so maxPerShard >= 1 and total <= MaxEntries.
		for shardCount > 1 && opts.MaxEntries < shardCount {
			shardCount >>= 1
		}
		maxPerShard = opts.MaxEntries / shardCount
		if maxPerShard < 1 {
			maxPerShard = 1
		}
	}

	shards := make([]*cacheShard[K, V], shardCount)
	for i := range shards {
		shards[i] = &cacheShard[K, V]{
			items: make(map[K]*list.Element),
			order: list.New(),
		}
	}
	return &ShardedCache[K, V]{
		shards:          shards,
		mask:            uint64(shardCount - 1),
		seed:            maphash.MakeSeed(),
		maxPerShard:     maxPerShard,
		cleanupInterval: opts.CleanupInterval,
		nowFn:           time.Now,
	}
}

// Get retrieves key if present and unexpired, updating LRU recency.
func (c *ShardedCache[K, V]) Get(key K) (V, bool) {
	var zero V
	s := c.shardFor(key)
	now := c.nowFn()

	s.mu.Lock()
	elem, ok := s.items[key]
	if !ok {
		s.mu.Unlock()
		c.misses.Add(1)
		return zero, false
	}
	it := elem.Value.(*cacheItem[K, V])
	if !it.expiresAt.IsZero() && !now.Before(it.expiresAt) {
		s.order.Remove(elem)
		delete(s.items, key)
		s.mu.Unlock()
		c.misses.Add(1)
		c.evictions.Add(1)
		return zero, false
	}
	s.order.MoveToFront(elem)
	val := it.val
	s.mu.Unlock()

	c.hits.Add(1)
	return val, true
}

// Set inserts or updates key with ttl (ttl <= 0 stores without expiration).
// When the shard reaches maxPerShard, expired entries are purged first before
// evicting the least-recently-used entry.
func (c *ShardedCache[K, V]) Set(key K, val V, ttl time.Duration) {
	s := c.shardFor(key)
	now := c.nowFn()
	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, ok := s.items[key]; ok {
		it := elem.Value.(*cacheItem[K, V])
		it.val = val
		it.expiresAt = exp
		s.order.MoveToFront(elem)
		return
	}

	if c.maxPerShard > 0 && len(s.items) >= c.maxPerShard {
		evicted := c.purgeShardExpiredLocked(s, now)
		for len(s.items) >= c.maxPerShard {
			oldest := s.order.Back()
			if oldest == nil {
				break
			}
			oldItem := oldest.Value.(*cacheItem[K, V])
			s.order.Remove(oldest)
			delete(s.items, oldItem.key)
			evicted++
		}
		if evicted > 0 {
			c.evictions.Add(uint64(evicted))
		}
	}

	elem := s.order.PushFront(&cacheItem[K, V]{
		key:       key,
		val:       val,
		expiresAt: exp,
	})
	s.items[key] = elem
}

// GetOrLoad returns the cached value for key or invokes loader exactly once
// across concurrent callers via singleflight. Loader errors are never cached.
func (c *ShardedCache[K, V]) GetOrLoad(
	ctx context.Context,
	key K,
	ttl time.Duration,
	loader func(ctx context.Context) (V, error),
) (V, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	var zero V
	if loader == nil {
		return zero, fmt.Errorf("memcache: nil loader")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	sfKey := singleflightKey(key)
	// Detach single-caller cancellation so one caller aborting mid-flight does not
	// fail sibling callers coalesced onto the same singleflight execution, while
	// preserving context values (tenant/request metadata).
	loadCtx := context.WithoutCancel(ctx)
	ch := c.sf.DoChan(sfKey, func() (any, error) {
		if v, ok := c.Get(key); ok {
			return v, nil
		}
		val, err := loader(loadCtx)
		if err != nil {
			return zero, err
		}
		c.Set(key, val, ttl)
		return val, nil
	})

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return zero, res.Err
		}
		return res.Val.(V), nil
	}
}

// Delete removes key from the cache if present.
func (c *ShardedCache[K, V]) Delete(key K) {
	s := c.shardFor(key)
	s.mu.Lock()
	if elem, ok := s.items[key]; ok {
		s.order.Remove(elem)
		delete(s.items, key)
	}
	s.mu.Unlock()
}

// DeleteFunc removes every entry whose key satisfies predicate and returns the
// total number of removed entries (useful for per-tenant prefix invalidation).
func (c *ShardedCache[K, V]) DeleteFunc(predicate func(key K) bool) int {
	if predicate == nil {
		return 0
	}
	removed := 0
	for _, s := range c.shards {
		s.mu.Lock()
		for k, elem := range s.items {
			if predicate(k) {
				s.order.Remove(elem)
				delete(s.items, k)
				removed++
			}
		}
		s.mu.Unlock()
	}
	return removed
}

// PurgeExpired removes all expired entries across all shards and returns the
// count evicted.
func (c *ShardedCache[K, V]) PurgeExpired() int {
	now := c.nowFn()
	total := 0
	for _, s := range c.shards {
		s.mu.Lock()
		evicted := c.purgeShardExpiredLocked(s, now)
		s.mu.Unlock()
		total += evicted
	}
	if total > 0 {
		c.evictions.Add(uint64(total))
	}
	return total
}

// StartJanitor periodically calls PurgeExpired until ctx is cancelled.
// Its signature is compatible with lifecycle.BackgroundGroup loops.
func (c *ShardedCache[K, V]) StartJanitor(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = c.cleanupInterval
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.PurgeExpired()
		}
	}
}

// Len returns the current total number of stored entries across all shards.
func (c *ShardedCache[K, V]) Len() int {
	total := 0
	for _, s := range c.shards {
		s.mu.Lock()
		total += len(s.items)
		s.mu.Unlock()
	}
	return total
}

// Stats returns a snapshot of cache size, hits, misses, and evictions.
func (c *ShardedCache[K, V]) Stats() CacheStats {
	return CacheStats{
		Entries:   c.Len(),
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
	}
}

func (c *ShardedCache[K, V]) shardFor(key K) *cacheShard[K, V] {
	h := maphash.Comparable(c.seed, key)
	return c.shards[h&c.mask]
}

func (c *ShardedCache[K, V]) purgeShardExpiredLocked(s *cacheShard[K, V], now time.Time) int {
	evicted := 0
	for k, elem := range s.items {
		it := elem.Value.(*cacheItem[K, V])
		if !it.expiresAt.IsZero() && !now.Before(it.expiresAt) {
			s.order.Remove(elem)
			delete(s.items, k)
			evicted++
		}
	}
	return evicted
}

func singleflightKey[K comparable](key K) string {
	if s, ok := any(key).(string); ok {
		return s
	}
	return fmt.Sprintf("%T:%v", key, key)
}

func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
