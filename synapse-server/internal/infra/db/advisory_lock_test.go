package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct {
	val bool
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 1 {
		if b, ok := dest[0].(*bool); ok {
			*b = r.val
		}
	}
	return nil
}

type fakeLockConn struct {
	row         fakeRow
	closedCount int
	releaseCnt  int
}

func (f *fakeLockConn) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	if ctx == nil {
		return fakeRow{err: errors.New("nil context")}
	}
	if err := ctx.Err(); err != nil {
		return fakeRow{err: err}
	}
	return f.row
}

func (f *fakeLockConn) CloseUnderlying(_ context.Context) error {
	f.closedCount++
	return nil
}

func (f *fakeLockConn) Release() {
	f.releaseCnt++
}

func TestLockKeyIDDeterminismAndNamespaceIsolation(t *testing.T) {
	t.Parallel()

	id1 := LockKeyID("cron", "automation-daily")
	id2 := LockKeyID("cron", "automation-daily")
	if id1 != id2 {
		t.Fatalf("LockKeyID is not deterministic: %d != %d", id1, id2)
	}

	cronA := LockKeyID("cron", "a")
	migrateA := LockKeyID("migrate", "a")
	if cronA == migrateA {
		t.Fatalf("expected distinct LockKeyID across namespaces, both = %d", cronA)
	}

	cronB := LockKeyID("cron", "b")
	if cronA == cronB {
		t.Fatalf("expected distinct LockKeyID across keys, both = %d", cronA)
	}
}

func TestLockLeaseReleaseClosesDirtyConnOnUnlockFailure(t *testing.T) {
	t.Parallel()

	t.Run("healthy unlock returns connection without closing underlying conn", func(t *testing.T) {
		t.Parallel()
		fc := &fakeLockConn{row: fakeRow{val: true}}
		lease := &LockLease{conn: fc, keyID: 42}

		if err := lease.Release(context.Background()); err != nil {
			t.Fatalf("Release() unexpected error: %v", err)
		}
		// Second call must be a no-op.
		if err := lease.Release(context.Background()); err != nil {
			t.Fatalf("second Release() unexpected error: %v", err)
		}
		if fc.closedCount != 0 {
			t.Fatalf("closedCount = %d, want 0", fc.closedCount)
		}
		if fc.releaseCnt != 1 {
			t.Fatalf("releaseCnt = %d, want 1", fc.releaseCnt)
		}
	})

	t.Run("unlock query error closes underlying conn before pool release", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("connection reset by peer")
		fc := &fakeLockConn{row: fakeRow{err: wantErr}}
		lease := &LockLease{conn: fc, keyID: 42}

		err := lease.Release(context.Background())
		if !errors.Is(err, wantErr) {
			t.Fatalf("Release() err = %v, want wrapped %v", err, wantErr)
		}
		if fc.closedCount != 1 {
			t.Fatalf("closedCount = %d, want 1", fc.closedCount)
		}
		if fc.releaseCnt != 1 {
			t.Fatalf("releaseCnt = %d, want 1", fc.releaseCnt)
		}
	})

	t.Run("unlock returning false closes underlying conn before pool release", func(t *testing.T) {
		t.Parallel()
		fc := &fakeLockConn{row: fakeRow{val: false}}
		lease := &LockLease{conn: fc, keyID: 42}

		if err := lease.Release(context.Background()); err == nil {
			t.Fatalf("Release() expected error when pg_advisory_unlock returns false")
		}
		if fc.closedCount != 1 {
			t.Fatalf("closedCount = %d, want 1", fc.closedCount)
		}
		if fc.releaseCnt != 1 {
			t.Fatalf("releaseCnt = %d, want 1", fc.releaseCnt)
		}
	})

	t.Run("canceled or nil caller ctx detaches clean timeout without destroying conn", func(t *testing.T) {
		t.Parallel()
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		fc1 := &fakeLockConn{row: fakeRow{val: true}}
		if err := (&LockLease{conn: fc1, keyID: 7}).Release(canceledCtx); err != nil {
			t.Fatalf("Release(canceledCtx) error = %v", err)
		}
		if fc1.closedCount != 0 || fc1.releaseCnt != 1 {
			t.Fatalf("canceledCtx: closed=%d release=%d, want 0/1", fc1.closedCount, fc1.releaseCnt)
		}

		fc2 := &fakeLockConn{row: fakeRow{val: true}}
		if err := (&LockLease{conn: fc2, keyID: 8}).Release(nil); err != nil {
			t.Fatalf("Release(nil) error = %v", err)
		}
		if fc2.closedCount != 0 || fc2.releaseCnt != 1 {
			t.Fatalf("nil ctx: closed=%d release=%d, want 0/1", fc2.closedCount, fc2.releaseCnt)
		}
	})
}
