package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier unifies *pgxpool.Pool and pgx.Tx so store implementations automatically
// participate in outer context-propagated transactions.
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// TxManager manages atomic transaction boundaries carried implicitly via context.Context.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
	WithinReadOnlyTx(ctx context.Context, fn func(ctx context.Context) error) error
}

var (
	_ Querier   = (*pgxpool.Pool)(nil)
	_ Querier   = (pgx.Tx)(nil)
	_ TxManager = (*DB)(nil)
)

type txCtxKey struct{}

// Querier returns the active pgx.Tx from ctx if present, otherwise the underlying pool.
func (d *DB) Querier(ctx context.Context) Querier {
	if ctx != nil {
		if tx, ok := ctx.Value(txCtxKey{}).(pgx.Tx); ok && tx != nil {
			return tx
		}
	}
	return d.pool
}

// InTx reports whether ctx currently carries an active database transaction.
func InTx(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	tx, ok := ctx.Value(txCtxKey{}).(pgx.Tx)
	return ok && tx != nil
}

// WithoutTx returns a derived context with any active transaction handle stripped,
// allowing isolated pool-level writes (e.g. independent audit entries).
func WithoutTx(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, txCtxKey{}, (pgx.Tx)(nil))
}

// WithinTx executes fn inside a ReadCommitted transaction (or a PostgreSQL SAVEPOINT
// when ctx already carries an active transaction). Errors or panics roll back safely.
func (d *DB) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return d.withinTxOpts(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
}

// WithinReadOnlyTx executes fn inside a ReadCommitted read-only transaction (or a
// nested SAVEPOINT when already inside a transaction).
func (d *DB) WithinReadOnlyTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return d.withinTxOpts(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, fn)
}

func (d *DB) withinTxOpts(ctx context.Context, txOpts pgx.TxOptions, fn func(ctx context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var tx pgx.Tx
	var err error
	if parentTx, ok := ctx.Value(txCtxKey{}).(pgx.Tx); ok && parentTx != nil {
		// In pgx/v5, parentTx.Begin(ctx) automatically issues a PostgreSQL SAVEPOINT.
		tx, err = parentTx.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin savepoint: %w", err)
		}
	} else {
		tx, err = d.pool.BeginTx(ctx, txOpts)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
	}

	committed := false
	defer func() {
		if !committed {
			// Use a short detached timeout if caller ctx was canceled so ROLLBACK reaches PG cleanly.
			rbCtx := ctx
			if rbCtx.Err() != nil {
				var cancel context.CancelFunc
				rbCtx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
			}
			_ = tx.Rollback(rbCtx)
		}
	}()

	txCtx := context.WithValue(ctx, txCtxKey{}, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxClosed) {
			return err
		}
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}
