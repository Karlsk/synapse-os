package db

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockConn abstracts the minimal physical connection lifecycle needed by LockLease
// so failed unlocks can destroy the dirty connection before returning it to the pool.
type lockConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CloseUnderlying(ctx context.Context) error
	Release()
}

type poolLockConn struct {
	conn *pgxpool.Conn
}

func (p poolLockConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.conn.QueryRow(ctx, sql, args...)
}

func (p poolLockConn) CloseUnderlying(ctx context.Context) error {
	if c := p.conn.Conn(); c != nil {
		return c.Close(ctx)
	}
	return nil
}

func (p poolLockConn) Release() {
	p.conn.Release()
}

// AdvisoryLocker coordinates cross-instance mutual exclusion using PostgreSQL
// native advisory locks without external dependencies such as Redis.
type AdvisoryLocker struct {
	pool *pgxpool.Pool
}

// LockLease holds an acquired session-level advisory lock bound to a single
// dedicated physical connection from pgxpool.
type LockLease struct {
	conn     lockConn
	keyID    int64
	released atomic.Bool
}

// LockKeyID deterministically hashes (namespace, key) to a 64-bit PostgreSQL advisory lock ID.
func LockKeyID(namespace, key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(namespace))
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64())
}

// NewAdvisoryLocker creates an AdvisoryLocker backed by pool.
func NewAdvisoryLocker(pool *pgxpool.Pool) *AdvisoryLocker {
	return &AdvisoryLocker{pool: pool}
}

// TryWithLock attempts a non-blocking session-level advisory lock. If another
// holder already owns the lock, the physical connection is immediately returned
// and (false, nil) is returned. Otherwise fn runs while holding the lock and
// Release always unlocks on the same connection.
func (l *AdvisoryLocker) TryWithLock(ctx context.Context, namespace, key string, fn func(ctx context.Context) error) (acquired bool, err error) {
	lease, ok, err := l.Acquire(ctx, namespace, key)
	if err != nil || !ok {
		return false, err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = lease.Release(releaseCtx)
	}()
	return true, fn(ctx)
}

// Acquire takes a dedicated physical connection from the pool and attempts
// pg_try_advisory_lock on that connection. On contention or error the connection
// is released immediately.
func (l *AdvisoryLocker) Acquire(ctx context.Context, namespace, key string) (*LockLease, bool, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire conn for advisory lock: %w", err)
	}
	keyID := LockKeyID(namespace, key)
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, keyID).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("pg_try_advisory_lock(%s:%s): %w", namespace, key, err)
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	return &LockLease{conn: poolLockConn{conn: conn}, keyID: keyID}, true, nil
}

// Release unlocks the session advisory lock on the exact same physical connection
// and returns the connection to the pool. If pg_advisory_unlock fails or reports
// false, the underlying physical connection is closed before Release so PostgreSQL
// immediately drops the session lock and pgxpool discards the dirty connection.
// Safe to call multiple times.
func (lease *LockLease) Release(ctx context.Context) error {
	if lease == nil || lease.conn == nil || !lease.released.CompareAndSwap(false, true) {
		return nil
	}
	defer lease.conn.Release()

	if ctx == nil || ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
	}

	var unlocked bool
	if err := lease.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, lease.keyID).Scan(&unlocked); err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = lease.conn.CloseUnderlying(closeCtx)
		return fmt.Errorf("pg_advisory_unlock: %w", err)
	}
	if !unlocked {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = lease.conn.CloseUnderlying(closeCtx)
		return fmt.Errorf("pg_advisory_unlock: lock %d was not held on connection", lease.keyID)
	}
	return nil
}

// TryLockTx attempts a non-blocking transaction-scoped advisory lock that releases
// automatically on transaction COMMIT or ROLLBACK without pinning an extra connection.
func TryLockTx(ctx context.Context, q Querier, namespace, key string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, LockKeyID(namespace, key)).Scan(&ok)
	return ok, err
}

// withLockBlocking retries Acquire with bounded backoff until the advisory lock
// is acquired or ctx is canceled, used by MigrateUp/MigrateDownOne.
func (l *AdvisoryLocker) withLockBlocking(ctx context.Context, namespace, key string, fn func(ctx context.Context) error) error {
	backoff := 20 * time.Millisecond
	const maxBackoff = 200 * time.Millisecond

	for {
		lease, acquired, err := l.Acquire(ctx, namespace, key)
		if err != nil {
			return err
		}
		if acquired {
			defer func() {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = lease.Release(releaseCtx)
			}()
			return fn(ctx)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}
