package db

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestValidateMigrationsDropTableGuard(t *testing.T) {
	t.Parallel()

	t.Run("rejects DROP TABLE in +goose Up", func(t *testing.T) {
		t.Parallel()
		memFS := fstest.MapFS{
			"migrations/00001_bad.sql": &fstest.MapFile{
				Data: []byte(`
-- +goose Up
CREATE TABLE foo (id TEXT PRIMARY KEY);
DROP TABLE foo;

-- +goose Down
DROP TABLE IF EXISTS foo;
`),
			},
		}
		err := ValidateMigrations(memFS, "migrations")
		if err == nil {
			t.Fatalf("expected error for DROP TABLE in +goose Up, got nil")
		}
		if !strings.Contains(err.Error(), "DROP TABLE") {
			t.Fatalf("expected DROP TABLE error message, got: %v", err)
		}
	})

	t.Run("allows DROP TABLE in +goose Down and in SQL comments", func(t *testing.T) {
		t.Parallel()
		memFS := fstest.MapFS{
			"migrations/00001_safe.sql": &fstest.MapFile{
				Data: []byte(`
-- +goose Up
-- Never run DROP TABLE in production migrations!
/* Also safe inside block comment: DROP TABLE foo; */
CREATE TABLE safe_table (
    company_id VARCHAR(64) NOT NULL,
    id         VARCHAR(64) NOT NULL,
    PRIMARY KEY (company_id, id)
);

-- +goose Down
DROP TABLE IF EXISTS safe_table;
`),
			},
			"migrations/README.txt": &fstest.MapFile{
				Data: []byte("DROP TABLE ignored because non-sql file"),
			},
		}
		if err := ValidateMigrations(memFS, "migrations"); err != nil {
			t.Fatalf("ValidateMigrations() unexpected error: %v", err)
		}
	})

	t.Run("detects DROP TABLE with mixed block and line comment syntax", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name string
			sql  string
		}{
			{
				name: "block comment containing double-dash before DROP TABLE",
				sql:  "-- +goose Up\n/* note -- */ DROP TABLE leak_a;\n-- +goose Down\nSELECT 1;\n",
			},
			{
				name: "line comment containing block-open before DROP TABLE",
				sql:  "-- +goose Up\n-- /* misleading comment\nDROP TABLE leak_b;\n-- */\n-- +goose Down\nSELECT 1;\n",
			},
			{
				name: "single-quoted literal containing /* before DROP TABLE",
				sql:  "-- +goose Up\nCREATE TABLE f (mime TEXT DEFAULT 'application/*');\nDROP TABLE leak_c;\n-- +goose Down\nSELECT 1;\n",
			},
		}
		for _, tc := range cases {
			memFS := fstest.MapFS{
				"migrations/00001_tricky.sql": &fstest.MapFile{Data: []byte(tc.sql)},
			}
			if err := ValidateMigrations(memFS, "migrations"); err == nil {
				t.Errorf("%s: expected DROP TABLE to be rejected, got nil", tc.name)
			}
		}

		// Single-quoted string literal mentioning DROP TABLE must NOT false-positive.
		safeLiteralFS := fstest.MapFS{
			"migrations/00001_literal.sql": &fstest.MapFile{
				Data: []byte("-- +goose Up\nINSERT INTO doc VALUES ('never run DROP TABLE in prod', 'it''s /* safe */');\n"),
			},
		}
		if err := ValidateMigrations(safeLiteralFS, "migrations"); err != nil {
			t.Errorf("unexpected false positive on string literal: %v", err)
		}
	})

	t.Run("validates testdata/migrations fixtures", func(t *testing.T) {
		t.Parallel()
		if err := ValidateMigrations(os.DirFS("testdata"), "migrations"); err != nil {
			t.Fatalf("ValidateMigrations(testdata/migrations) error = %v", err)
		}
	})
}
