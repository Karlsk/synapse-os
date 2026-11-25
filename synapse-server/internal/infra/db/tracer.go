package db

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
)

type traceStartKey struct{}

type traceStart struct {
	at  time.Time
	sql string
}

type slowQueryTracer struct {
	threshold time.Duration
	logger    *slog.Logger
	slowTotal prometheus.Counter
	durHist   prometheus.Histogram
}

func newSlowQueryTracer(threshold time.Duration, logger *slog.Logger, reg prometheus.Registerer) *slowQueryTracer {
	if logger == nil {
		logger = slog.Default()
	}
	t := &slowQueryTracer{
		threshold: threshold,
		logger:    logger,
	}
	if reg != nil {
		slowCounter := prometheus.NewCounter(prometheus.CounterOpts{
			Name: "synapse_db_slow_queries_total",
			Help: "Total PostgreSQL queries exceeding the slow query threshold.",
		})
		if err := reg.Register(slowCounter); err == nil {
			t.slowTotal = slowCounter
		} else if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(prometheus.Counter); ok {
				t.slowTotal = existing
			}
		}

		durHist := prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "synapse_db_query_duration_seconds",
			Help:    "PostgreSQL query execution duration distribution in seconds.",
			Buckets: prometheus.DefBuckets,
		})
		if err := reg.Register(durHist); err == nil {
			t.durHist = durHist
		} else if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(prometheus.Histogram); ok {
				t.durHist = existing
			}
		}
	}
	return t
}

func (t *slowQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// Security invariant: NEVER log or store data.Args (prevents leaking passwords, tokens, or ciphertexts).
	return context.WithValue(ctx, traceStartKey{}, traceStart{
		at:  time.Now(),
		sql: compactSQL(data.SQL, 512),
	})
}

func (t *slowQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(traceStartKey{}).(traceStart)
	if !ok {
		return
	}
	elapsed := time.Since(st.at)
	if t.durHist != nil {
		t.durHist.Observe(elapsed.Seconds())
	}
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) && !errors.Is(data.Err, context.Canceled) {
		t.logger.ErrorContext(ctx, "postgres query failed",
			"duration_ms", elapsed.Milliseconds(), "sql", st.sql, "err", data.Err)
		return
	}
	if t.threshold > 0 && elapsed >= t.threshold {
		if t.slowTotal != nil {
			t.slowTotal.Inc()
		}
		t.logger.WarnContext(ctx, "slow postgres query",
			"duration_ms", elapsed.Milliseconds(), "threshold_ms", t.threshold.Milliseconds(),
			"rows", data.CommandTag.RowsAffected(), "sql", st.sql)
	}
}

func compactSQL(raw string, maxLen int) string {
	collapsed := strings.Join(strings.Fields(raw), " ")
	if maxLen > 0 && len(collapsed) > maxLen {
		return collapsed[:maxLen] + "..."
	}
	return collapsed
}
