package db

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// MigrationResult summarizes a single applied or reverted goose migration.
type MigrationResult struct {
	Version  int64
	Source   string
	Duration time.Duration
}

var (
	dropTableRe = regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)
	gooseUpRe   = regexp.MustCompile(`(?i)^\s*--\s*\+goose\s+up\b`)
	gooseDownRe = regexp.MustCompile(`(?i)^\s*--\s*\+goose\s+down\b`)
)

func subMigrationsFS(migrationsFS fs.FS, dir string) (fs.FS, error) {
	if migrationsFS == nil {
		return nil, fmt.Errorf("migrations fs must not be nil")
	}
	cleanDir := strings.TrimSpace(dir)
	if cleanDir == "" || cleanDir == "." {
		return migrationsFS, nil
	}
	sub, err := fs.Sub(migrationsFS, cleanDir)
	if err != nil {
		return nil, fmt.Errorf("sub migrations fs %q: %w", cleanDir, err)
	}
	return sub, nil
}

// ValidateMigrations statically scans every .sql migration file and rejects any
// DROP TABLE statement inside the -- +goose Up section (ignoring comments and -- +goose Down).
func ValidateMigrations(migrationsFS fs.FS, dir string) error {
	subFS, err := subMigrationsFS(migrationsFS, dir)
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(subFS, ".")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			continue
		}
		data, err := fs.ReadFile(subFS, e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		upSQL := extractGooseUpSQL(string(data))
		if dropTableRe.MatchString(upSQL) {
			return fmt.Errorf("forbidden DROP TABLE in migration %s (+goose Up)", e.Name())
		}
	}
	return nil
}

// extractGooseUpSQL returns only the executable SQL between -- +goose Up and -- +goose Down
// with both block (/* ... */) and line (-- ...) comments stripped in a single lexical pass.
func extractGooseUpSQL(content string) string {
	lines := strings.Split(content, "\n")
	var upLines []string
	inUp := false
	for _, line := range lines {
		if gooseUpRe.MatchString(line) {
			inUp = true
			continue
		}
		if gooseDownRe.MatchString(line) {
			inUp = false
			continue
		}
		if inUp {
			upLines = append(upLines, line)
		}
	}
	return stripSQLComments(strings.Join(upLines, "\n"))
}

func stripSQLComments(sql string) string {
	var out strings.Builder
	out.Grow(len(sql))
	for i := 0; i < len(sql); {
		// Replace single-quoted string literal contents with '' so 'application/*'
		// or '--' inside literals cannot open fake comments or trigger false positives.
		if sql[i] == '\'' {
			out.WriteString("''")
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					i++
					if i < len(sql) && sql[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		}
		if sql[i] == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			i += 2
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if sql[i] == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < len(sql) {
				i += 2
			} else {
				i = len(sql)
			}
			out.WriteByte(' ')
			continue
		}
		out.WriteByte(sql[i])
		i++
	}
	return out.String()
}

// MigrateUp validates all forward migrations, acquires a blocking PostgreSQL advisory
// lock to serialize concurrent cold starts, and applies all pending goose migrations.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) ([]MigrationResult, error) {
	if err := ValidateMigrations(migrationsFS, dir); err != nil {
		return nil, err
	}
	subFS, err := subMigrationsFS(migrationsFS, dir)
	if err != nil {
		return nil, err
	}

	locker := NewAdvisoryLocker(pool)
	var out []MigrationResult
	err = locker.withLockBlocking(ctx, "synapse", "goose_migrate", func(lockCtx context.Context) error {
		sqlDB := stdlib.OpenDBFromPool(pool)
		defer func() { _ = sqlDB.Close() }()

		provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, subFS)
		if err != nil {
			return fmt.Errorf("init goose provider: %w", err)
		}
		res, err := provider.Up(lockCtx)
		if err != nil {
			return fmt.Errorf("goose up: %w", err)
		}
		for _, r := range res {
			if r == nil || r.Source == nil {
				continue
			}
			out = append(out, MigrationResult{
				Version:  r.Source.Version,
				Source:   r.Source.Path,
				Duration: r.Duration,
			})
		}
		return nil
	})
	return out, err
}

// MigrateDownOne rolls back the most recently applied migration under the migration advisory lock.
func MigrateDownOne(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) (*MigrationResult, error) {
	subFS, err := subMigrationsFS(migrationsFS, dir)
	if err != nil {
		return nil, err
	}

	locker := NewAdvisoryLocker(pool)
	var result *MigrationResult
	err = locker.withLockBlocking(ctx, "synapse", "goose_migrate", func(lockCtx context.Context) error {
		sqlDB := stdlib.OpenDBFromPool(pool)
		defer func() { _ = sqlDB.Close() }()

		provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, subFS)
		if err != nil {
			return fmt.Errorf("init goose provider: %w", err)
		}
		r, err := provider.Down(lockCtx)
		if err != nil {
			return fmt.Errorf("goose down: %w", err)
		}
		if r != nil && r.Source != nil {
			result = &MigrationResult{
				Version:  r.Source.Version,
				Source:   r.Source.Path,
				Duration: r.Duration,
			}
		}
		return nil
	})
	return result, err
}

// MigrateVersion returns the highest migration version currently recorded in goose_db_version.
func MigrateVersion(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) (int64, error) {
	subFS, err := subMigrationsFS(migrationsFS, dir)
	if err != nil {
		return 0, err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, subFS)
	if err != nil {
		return 0, fmt.Errorf("init goose provider: %w", err)
	}
	v, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("goose get db version: %w", err)
	}
	return v, nil
}
