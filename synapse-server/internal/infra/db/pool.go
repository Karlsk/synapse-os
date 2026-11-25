// Package db provides the L0 PostgreSQL 17 runtime foundation for Synapse-OS:
// tuned pgxpool management, context-propagating TxManager with nested SAVEPOINTs,
// embedded forward-only goose/v3 migrations, PG error mapping, slow-query tracing,
// zero-Redis PostgreSQL AdvisoryLocks, and isolated test harnesses.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Config defines PostgreSQL 17 connection pool and tracing parameters.
type Config struct {
	DSN                string
	MaxConns           int32
	MinConns           int32
	MaxConnLifetime    time.Duration
	MaxConnIdleTime    time.Duration
	HealthCheckPeriod  time.Duration
	ConnectTimeout     time.Duration
	StatementTimeout   time.Duration
	SlowQueryThreshold time.Duration
	ApplicationName    string
}

// PoolStats captures a point-in-time snapshot of pgxpool utilization metrics.
type PoolStats struct {
	AcquireCount         int64
	AcquireDuration      time.Duration
	AcquiredConns        int32
	CanceledAcquireCount int64
	ConstructingConns    int32
	EmptyAcquireCount    int64
	IdleConns            int32
	MaxConns             int32
	TotalConns           int32
}

type openOptions struct {
	logger     *slog.Logger
	reg        prometheus.Registerer
	searchPath string
}

// Option customizes Open behavior.
type Option func(*openOptions)

// WithLogger configures the structured logger used by slow-query tracing and pool diagnostics.
func WithLogger(l *slog.Logger) Option {
	return func(o *openOptions) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithRegisterer registers pool and query Prometheus metrics against r.
func WithRegisterer(r prometheus.Registerer) Option {
	return func(o *openOptions) {
		o.reg = r
	}
}

// WithSearchPath pins every pooled connection's search_path to "<schema>,public".
func WithSearchPath(schema string) Option {
	return func(o *openOptions) {
		o.searchPath = strings.TrimSpace(schema)
	}
}

// DB wraps a tuned pgxpool.Pool with context-based transactions and advisory locking.
type DB struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	locker *AdvisoryLocker
}

func applyDefaults(cfg *Config) {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 16
	}
	if cfg.MinConns <= 0 {
		cfg.MinConns = 2
	}
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	if cfg.MaxConnLifetime <= 0 {
		cfg.MaxConnLifetime = 30 * time.Minute
	}
	if cfg.MaxConnIdleTime <= 0 {
		cfg.MaxConnIdleTime = 5 * time.Minute
	}
	if cfg.HealthCheckPeriod <= 0 {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.StatementTimeout <= 0 {
		cfg.StatementTimeout = 30 * time.Second
	}
	if cfg.SlowQueryThreshold <= 0 {
		cfg.SlowQueryThreshold = 200 * time.Millisecond
	}
	if strings.TrimSpace(cfg.ApplicationName) == "" {
		cfg.ApplicationName = "synapse-server"
	}
}

// Open initializes a tuned pgxpool.Pool enforcing TimeZone=UTC, verifies connectivity
// with an initial Ping, and optionally registers Prometheus pool/query metrics.
func Open(ctx context.Context, cfg Config, opts ...Option) (*DB, error) {
	o := openOptions{logger: slog.Default()}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	applyDefaults(&cfg)

	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	pcfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	if pcfg.ConnConfig.RuntimeParams == nil {
		pcfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	pcfg.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	pcfg.ConnConfig.RuntimeParams["application_name"] = cfg.ApplicationName
	if cfg.StatementTimeout > 0 {
		pcfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.StatementTimeout.Milliseconds(), 10)
	}
	if o.searchPath != "" {
		pcfg.ConnConfig.RuntimeParams["search_path"] = o.searchPath + ",public"
	}

	pcfg.ConnConfig.Tracer = newSlowQueryTracer(cfg.SlowQueryThreshold, o.logger, o.reg)

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pgxpool: %w", err)
	}
	d := &DB{
		pool:   pool,
		logger: o.logger,
		locker: NewAdvisoryLocker(pool),
	}
	if _, err := d.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("initial postgres ping failed: %w", err)
	}
	if o.reg != nil {
		registerPoolMetrics(o.reg, pool)
	}
	return d, nil
}

// Pool returns the underlying *pgxpool.Pool.
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Ping verifies PostgreSQL connectivity, applying a default 2-second timeout when
// ctx has no deadline, and returns the measured latency.
func (d *DB) Ping(ctx context.Context) (time.Duration, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	start := time.Now()
	err := d.pool.Ping(ctx)
	return time.Since(start), err
}

// Stats returns a snapshot of the current pgxpool connection statistics.
func (d *DB) Stats() PoolStats {
	s := d.pool.Stat()
	return PoolStats{
		AcquireCount:         s.AcquireCount(),
		AcquireDuration:      s.AcquireDuration(),
		AcquiredConns:        s.AcquiredConns(),
		CanceledAcquireCount: s.CanceledAcquireCount(),
		ConstructingConns:    s.ConstructingConns(),
		EmptyAcquireCount:    s.EmptyAcquireCount(),
		IdleConns:            s.IdleConns(),
		MaxConns:             s.MaxConns(),
		TotalConns:           s.TotalConns(),
	}
}

// Locker returns the PostgreSQL native AdvisoryLocker bound to this DB's pool.
func (d *DB) Locker() *AdvisoryLocker {
	return d.locker
}

// Close releases all pooled connections.
func (d *DB) Close() {
	if d != nil && d.pool != nil {
		d.pool.Close()
	}
}

func registerPoolMetrics(reg prometheus.Registerer, pool *pgxpool.Pool) {
	collectors := []prometheus.Collector{
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "synapse_db_pool_acquired_conns",
			Help: "Number of currently acquired connections in the PostgreSQL pool.",
		}, func() float64 { return float64(pool.Stat().AcquiredConns()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "synapse_db_pool_idle_conns",
			Help: "Number of currently idle connections in the PostgreSQL pool.",
		}, func() float64 { return float64(pool.Stat().IdleConns()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "synapse_db_pool_total_conns",
			Help: "Total number of connections currently in the PostgreSQL pool.",
		}, func() float64 { return float64(pool.Stat().TotalConns()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "synapse_db_pool_max_conns",
			Help: "Configured maximum number of connections in the PostgreSQL pool.",
		}, func() float64 { return float64(pool.Stat().MaxConns()) }),
	}
	for _, c := range collectors {
		_ = reg.Register(c)
	}
}
