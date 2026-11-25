package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewTestSchemaDB provisions an isolated temporary PostgreSQL schema (test_<16hex>)
// for concurrent t.Parallel() integration tests, optionally runs embedded migrations,
// and drops the schema automatically during t.Cleanup. When TEST_POSTGRES_DSN is unset,
// the test is skipped cleanly so offline go test ./... stays fast and green.
func NewTestSchemaDB(t testing.TB, migrationsFS fs.FS, dir string) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping postgres integration test")
	}

	schema := "test_" + randomHex(8)
	ctx := context.Background()

	adminDB, err := Open(ctx, Config{DSN: dsn, MaxConns: 2, MinConns: 1})
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	safeSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminDB.Pool().Exec(ctx, "CREATE SCHEMA "+safeSchema); err != nil {
		adminDB.Close()
		t.Fatalf("create test schema %s: %v", schema, err)
	}

	testDB, err := Open(ctx, Config{DSN: dsn, MaxConns: 8, MinConns: 1}, WithSearchPath(schema))
	if err != nil {
		_, _ = adminDB.Pool().Exec(ctx, "DROP SCHEMA IF EXISTS "+safeSchema+" CASCADE")
		adminDB.Close()
		t.Fatalf("open schema db: %v", err)
	}
	if migrationsFS != nil {
		if _, err := MigrateUp(ctx, testDB.Pool(), migrationsFS, dir); err != nil {
			testDB.Close()
			_, _ = adminDB.Pool().Exec(ctx, "DROP SCHEMA IF EXISTS "+safeSchema+" CASCADE")
			adminDB.Close()
			t.Fatalf("migrate test schema %s: %v", schema, err)
		}
	}

	t.Cleanup(func() {
		testDB.Close()
		_, _ = adminDB.Pool().Exec(context.Background(), "DROP SCHEMA IF EXISTS "+safeSchema+" CASCADE")
		adminDB.Close()
	})
	return testDB
}

// RunInRollbackTx executes fn inside a transaction that is unconditionally rolled
// back on return; nested WithinTx calls inside fn automatically use SAVEPOINTs.
func RunInRollbackTx(t testing.TB, d *DB, fn func(txCtx context.Context)) {
	t.Helper()
	ctx := context.Background()
	tx, err := d.Pool().BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin rollback test tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(context.WithValue(ctx, txCtxKey{}, tx))
}
