package db

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

func requireTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping postgres integration test")
	}
	return dsn
}

func TestIntegrationOpenPoolUTCPingAndMetrics(t *testing.T) {
	t.Parallel()
	dsn := requireTestDSN(t)

	metrics, err := obs.NewMetrics("synapse-server")
	if err != nil {
		t.Fatalf("obs.NewMetrics: %v", err)
	}

	ctx := context.Background()
	d, err := Open(ctx, Config{
		DSN:      dsn,
		MaxConns: 6,
		MinConns: 2,
	}, WithRegisterer(metrics.Registerer()))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer d.Close()

	var tz string
	if err := d.Querier(ctx).QueryRow(ctx, "SHOW TIME ZONE").Scan(&tz); err != nil {
		t.Fatalf("SHOW TIME ZONE error = %v", err)
	}
	if tz != "UTC" {
		t.Fatalf("SHOW TIME ZONE = %q, want UTC", tz)
	}

	latency, err := d.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if latency <= 0 {
		t.Fatalf("expected positive Ping latency, got %v", latency)
	}

	stats := d.Stats()
	if stats.MaxConns != 6 {
		t.Fatalf("Stats().MaxConns = %d, want 6", stats.MaxConns)
	}

	families, err := metrics.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	foundMaxConns := false
	for _, mf := range families {
		if mf.GetName() == "synapse_db_pool_max_conns" {
			foundMaxConns = true
			if got := mf.GetMetric()[0].GetGauge().GetValue(); got != 6 {
				t.Fatalf("synapse_db_pool_max_conns = %v, want 6", got)
			}
		}
	}
	if !foundMaxConns {
		t.Fatalf("synapse_db_pool_max_conns metric not found in Gather() output")
	}
}

func TestIntegrationTxCommitRollbackSavepointAndPanicSafety(t *testing.T) {
	t.Parallel()
	d := NewTestSchemaDB(t, os.DirFS("testdata"), "migrations")
	ctx := context.Background()

	// 1. Atomic commit + InTx / WithoutTx checks
	err := d.WithinTx(ctx, func(txCtx context.Context) error {
		if !InTx(txCtx) {
			t.Errorf("expected InTx(txCtx) == true")
		}
		if InTx(WithoutTx(txCtx)) {
			t.Errorf("expected InTx(WithoutTx(txCtx)) == false")
		}
		_, err := d.Querier(txCtx).Exec(txCtx, `INSERT INTO test_items (id, name, note) VALUES ($1, $2, $3)`, "i1", "item-1", "ok")
		return err
	})
	if err != nil {
		t.Fatalf("WithinTx commit error = %v", err)
	}

	// Verify via WithinReadOnlyTx
	var count int
	err = d.WithinReadOnlyTx(ctx, func(roCtx context.Context) error {
		return d.Querier(roCtx).QueryRow(roCtx, `SELECT COUNT(*) FROM test_items WHERE id = 'i1'`).Scan(&count)
	})
	if err != nil || count != 1 {
		t.Fatalf("WithinReadOnlyTx count=%d err=%v, want 1, nil", count, err)
	}

	// 2. Outer rollback on error
	boom := errors.New("abort outer")
	err = d.WithinTx(ctx, func(txCtx context.Context) error {
		if _, err := d.Querier(txCtx).Exec(txCtx, `INSERT INTO test_items (id, name) VALUES ('i2', 'item-2')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got %v", err)
	}
	if err := d.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM test_items WHERE id = 'i2'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expected i2 rolled back, count=%d err=%v", count, err)
	}

	// 3. Nested WithinTx SAVEPOINT: inner error rolls back only inner, outer commits
	err = d.WithinTx(ctx, func(outerCtx context.Context) error {
		if _, err := d.Querier(outerCtx).Exec(outerCtx, `INSERT INTO test_items (id, name) VALUES ('outer', 'outer-row')`); err != nil {
			return err
		}
		innerErr := d.WithinTx(outerCtx, func(innerCtx context.Context) error {
			if _, err := d.Querier(innerCtx).Exec(innerCtx, `INSERT INTO test_items (id, name) VALUES ('inner', 'inner-row')`); err != nil {
				return err
			}
			return errors.New("rollback inner only")
		})
		if innerErr == nil {
			t.Errorf("expected inner error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("outer commit after inner savepoint rollback failed: %v", err)
	}
	var outerCount, innerCount int
	_ = d.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM test_items WHERE id = 'outer'`).Scan(&outerCount)
	_ = d.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM test_items WHERE id = 'inner'`).Scan(&innerCount)
	if outerCount != 1 || innerCount != 0 {
		t.Fatalf("SAVEPOINT semantics failed: outer=%d (want 1), inner=%d (want 0)", outerCount, innerCount)
	}

	// 4. Panic inside WithinTx rolls back and does not leak connections
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = d.WithinTx(ctx, func(txCtx context.Context) error {
			_, _ = d.Querier(txCtx).Exec(txCtx, `INSERT INTO test_items (id, name) VALUES ('panic-row', 'panic-name')`)
			panic("unexpected callback panic")
		})
	}()
	if !panicked {
		t.Fatalf("expected panic to propagate")
	}
	if err := d.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM test_items WHERE id = 'panic-row'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expected panic-row rolled back, count=%d err=%v", count, err)
	}
	if d.Stats().AcquiredConns != 0 {
		t.Fatalf("connection leaked after panic rollback: acquired=%d", d.Stats().AcquiredConns)
	}
}

func TestIntegrationMigrationsConcurrentUpDownAndVersion(t *testing.T) {
	t.Parallel()
	d := NewTestSchemaDB(t, nil, "")
	ctx := context.Background()
	migFS := os.DirFS("testdata")

	// 8 goroutines concurrently calling MigrateUp must serialize via advisory lock without error
	const workers = 8
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	var appliedTotal atomic.Int64

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := MigrateUp(ctx, d.Pool(), migFS, "migrations")
			if err != nil {
				errCh <- err
				return
			}
			appliedTotal.Add(int64(len(res)))
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent MigrateUp error: %v", err)
	}
	if got := appliedTotal.Load(); got != 2 {
		t.Fatalf("expected exactly 2 migrations applied across all 8 concurrent callers, got %d", got)
	}

	ver, err := MigrateVersion(ctx, d.Pool(), migFS, "migrations")
	if err != nil || ver != 2 {
		t.Fatalf("MigrateVersion = (%d, %v), want (2, nil)", ver, err)
	}

	downRes, err := MigrateDownOne(ctx, d.Pool(), migFS, "migrations")
	if err != nil || downRes == nil || downRes.Version != 2 {
		t.Fatalf("MigrateDownOne = (%+v, %v), want version 2", downRes, err)
	}
	ver, err = MigrateVersion(ctx, d.Pool(), migFS, "migrations")
	if err != nil || ver != 1 {
		t.Fatalf("MigrateVersion after DownOne = (%d, %v), want (1, nil)", ver, err)
	}

	upAgain, err := MigrateUp(ctx, d.Pool(), migFS, "migrations")
	if err != nil || len(upAgain) != 1 || upAgain[0].Version != 2 {
		t.Fatalf("re-MigrateUp = (%+v, %v), want 1 migration (v2)", upAgain, err)
	}
}

func TestIntegrationAdvisoryLockMutualExclusionAndTxLock(t *testing.T) {
	t.Parallel()
	d := NewTestSchemaDB(t, nil, "")
	ctx := context.Background()
	locker := d.Locker()

	// 1. Acquire / contention / release lifecycle
	key := "cron-" + randomHex(4)
	lease, acquired, err := locker.Acquire(ctx, "cron", key)
	if err != nil || !acquired {
		t.Fatalf("Acquire() = (%v, %v), want acquired", acquired, err)
	}

	// Second caller must immediately receive (false, nil)
	ranB := false
	okB, err := locker.TryWithLock(ctx, "cron", key, func(_ context.Context) error {
		ranB = true
		return nil
	})
	if err != nil || okB || ranB {
		t.Fatalf("contending TryWithLock = (%v, %v, ran=%v), want (false, nil, false)", okB, err, ranB)
	}

	// Release is idempotent
	if err := lease.Release(ctx); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}

	// Now caller B must acquire immediately
	okB, err = locker.TryWithLock(ctx, "cron", key, func(_ context.Context) error {
		ranB = true
		return nil
	})
	if err != nil || !okB || !ranB {
		t.Fatalf("post-release TryWithLock = (%v, %v, ran=%v), want (true, nil, true)", okB, err, ranB)
	}

	// 2. 10 goroutines racing for same lock must never exceed 1 active holder simultaneously
	raceKey := "race-" + randomHex(4)
	var currentActive atomic.Int32
	var maxObserved atomic.Int32
	var totalAcquired atomic.Int32
	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				_, _ = locker.TryWithLock(ctx, "cron", raceKey, func(_ context.Context) error {
					cur := currentActive.Add(1)
					for {
						prev := maxObserved.Load()
						if cur <= prev || maxObserved.CompareAndSwap(prev, cur) {
							break
						}
					}
					totalAcquired.Add(1)
					time.Sleep(5 * time.Millisecond)
					currentActive.Add(-1)
					return nil
				})
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	if maxObserved.Load() > 1 {
		t.Fatalf("advisory lock violated mutual exclusion: maxObserved=%d", maxObserved.Load())
	}
	if totalAcquired.Load() < 1 {
		t.Fatalf("expected at least 1 lock acquisition")
	}

	// 3. Transaction-level TryLockTx releases on transaction completion
	txKey := "tx-" + randomHex(4)
	err = d.WithinTx(ctx, func(txCtx context.Context) error {
		ok, err := TryLockTx(txCtx, d.Querier(txCtx), "job", txKey)
		if err != nil || !ok {
			t.Fatalf("TryLockTx inside tx = (%v, %v), want (true, nil)", ok, err)
		}
		// Another session must see lock held
		otherOk, otherErr := locker.TryWithLock(ctx, "job", txKey, func(_ context.Context) error { return nil })
		if otherErr != nil || otherOk {
			t.Fatalf("expected tx lock to block other session, got (%v, %v)", otherOk, otherErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTx(TryLockTx) error = %v", err)
	}
	// After commit, lock must be free
	freeOk, err := locker.TryWithLock(ctx, "job", txKey, func(_ context.Context) error { return nil })
	if err != nil || !freeOk {
		t.Fatalf("expected tx lock released after commit, got (%v, %v)", freeOk, err)
	}
}

func TestIntegrationHarnessParallelSchemasAndRollbackTx(t *testing.T) {
	t.Parallel()
	dsn := requireTestDSN(t)

	// Subtests verify two parallel temporary schemas never collide & RunInRollbackTx cleans writes.
	var schemaA, schemaB string
	t.Run("parallel_schemas", func(t *testing.T) {
		t.Run("schema_A", func(t *testing.T) {
			t.Parallel()
			d := NewTestSchemaDB(t, os.DirFS("testdata"), "migrations")
			ctx := context.Background()
			_ = d.Querier(ctx).QueryRow(ctx, "SELECT current_schema()").Scan(&schemaA)

			if _, err := d.Querier(ctx).Exec(ctx, `INSERT INTO test_items (id, name) VALUES ('same-id', 'from-A')`); err != nil {
				t.Fatalf("insert schema A: %v", err)
			}

			// Verify RunInRollbackTx hides writes after closure ends
			RunInRollbackTx(t, d, func(txCtx context.Context) {
				if _, err := d.Querier(txCtx).Exec(txCtx, `INSERT INTO test_items (id, name) VALUES ('temp-rb', 'rb-name')`); err != nil {
					t.Fatalf("insert in rollback tx: %v", err)
				}
			})
			var cnt int
			if err := d.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM test_items WHERE id = 'temp-rb'`).Scan(&cnt); err != nil || cnt != 0 {
				t.Fatalf("RunInRollbackTx leaked row: cnt=%d err=%v", cnt, err)
			}
		})

		t.Run("schema_B", func(t *testing.T) {
			t.Parallel()
			d := NewTestSchemaDB(t, os.DirFS("testdata"), "migrations")
			ctx := context.Background()
			_ = d.Querier(ctx).QueryRow(ctx, "SELECT current_schema()").Scan(&schemaB)

			if _, err := d.Querier(ctx).Exec(ctx, `INSERT INTO test_items (id, name) VALUES ('same-id', 'from-B')`); err != nil {
				t.Fatalf("insert schema B: %v", err)
			}
		})
	})

	// After parallel subtests complete and their t.Cleanup ran, both schemas must be dropped.
	if schemaA == "" || schemaB == "" || schemaA == schemaB {
		t.Fatalf("expected two distinct schemas, got A=%q B=%q", schemaA, schemaB)
	}
	adminDB, err := Open(context.Background(), Config{DSN: dsn, MaxConns: 2, MinConns: 1})
	if err != nil {
		t.Fatalf("open admin verify db: %v", err)
	}
	defer adminDB.Close()

	var remaining int
	err = adminDB.Querier(context.Background()).QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name IN ($1, $2)`,
		schemaA, schemaB,
	).Scan(&remaining)
	if err != nil || remaining != 0 {
		t.Fatalf("temporary schemas not cleaned up: remaining=%d err=%v", remaining, err)
	}
}
