package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

func TestSlowQueryTracerThresholdRedactionAndMetrics(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger, closeFn, err := obs.NewLogger(obs.LoggerConfig{
		Level:  "debug",
		Format: "json",
		Output: &logBuf,
	})
	if err != nil {
		t.Fatalf("obs.NewLogger: %v", err)
	}
	defer func() { _ = closeFn() }()

	metrics, err := obs.NewMetrics("synapse-server")
	if err != nil {
		t.Fatalf("obs.NewMetrics: %v", err)
	}

	tracer := newSlowQueryTracer(100*time.Millisecond, logger, metrics.Registerer())

	ctx := obs.WithRequestID(context.Background(), "req-slow-1")
	ctx = obs.WithCompanyID(ctx, "acme")
	ctx = obs.WithUserID(ctx, "usr-7")

	secretArg := "super-secret-password-value-999"
	rawSQL := "SELECT id, name\n  FROM app_user\n WHERE company_id = $1 AND password_hash = $2"

	tracedCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{
		SQL:  rawSQL,
		Args: []any{"acme", secretArg},
	})
	// Simulate elapsed 250ms by backdating start timestamp directly inside same package.
	st := tracedCtx.Value(traceStartKey{}).(traceStart)
	st.at = time.Now().Add(-250 * time.Millisecond)
	tracedCtx = context.WithValue(tracedCtx, traceStartKey{}, st)

	tracer.TraceQueryEnd(tracedCtx, nil, pgx.TraceQueryEndData{
		CommandTag: pgconn.NewCommandTag("SELECT 1"),
	})

	out := logBuf.String()
	if strings.Contains(out, secretArg) {
		t.Fatalf("slow query log leaked bound parameter value:\n%s", out)
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuf.Bytes()), &entry); err != nil {
		t.Fatalf("unmarshal log json: %v (raw=%s)", err, out)
	}
	if entry["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", entry["level"])
	}
	if entry[obs.AttrRequestID] != "req-slow-1" || entry[obs.AttrCompanyID] != "acme" || entry[obs.AttrUserID] != "usr-7" {
		t.Errorf("missing context metadata in log entry: %v", entry)
	}
	if dur, ok := entry["duration_ms"].(float64); !ok || dur < 200 {
		t.Errorf("duration_ms = %v, want >= 200", entry["duration_ms"])
	}
	sqlVal, _ := entry["sql"].(string)
	if !strings.Contains(sqlVal, "SELECT id, name FROM app_user WHERE company_id = $1") {
		t.Errorf("expected compacted SQL, got %q", sqlVal)
	}

	// Verify Prometheus slow query counter incremented and duration histogram recorded
	families, err := metrics.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather(): %v", err)
	}
	var slowCount float64
	var durSeen bool
	for _, mf := range families {
		switch mf.GetName() {
		case "synapse_db_slow_queries_total":
			for _, m := range mf.GetMetric() {
				slowCount += m.GetCounter().GetValue()
			}
		case "synapse_db_query_duration_seconds":
			if len(mf.GetMetric()) > 0 && mf.GetMetric()[0].GetHistogram().GetSampleCount() >= 1 {
				durSeen = true
			}
		}
	}
	if slowCount != 1 {
		t.Errorf("synapse_db_slow_queries_total = %v, want 1", slowCount)
	}
	if !durSeen {
		t.Errorf("expected synapse_db_query_duration_seconds histogram sample")
	}
}

func TestSlowQueryTracerLogsRealErrorsAndSkipsNoRowsAndCanceled(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger, closeFn, err := obs.NewLogger(obs.LoggerConfig{
		Level:  "debug",
		Format: "json",
		Output: &logBuf,
	})
	if err != nil {
		t.Fatalf("obs.NewLogger: %v", err)
	}
	defer func() { _ = closeFn() }()

	tracer := newSlowQueryTracer(500*time.Millisecond, logger, nil)

	// ErrNoRows and context.Canceled should NOT log error
	for _, ignored := range []error{pgx.ErrNoRows, context.Canceled} {
		ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
			SQL:  "SELECT 1",
			Args: []any{"secret-arg"},
		})
		tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: ignored})
	}
	if logBuf.Len() != 0 {
		t.Fatalf("expected no log for ErrNoRows/Canceled, got: %s", logBuf.String())
	}

	// Real DB error should log ERROR without leaking Args
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL:  "INSERT INTO app_user VALUES ($1)",
		Args: []any{"secret-password-never-log"},
	})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("connection reset")})

	out := logBuf.String()
	if !strings.Contains(out, `"level":"ERROR"`) || !strings.Contains(out, "postgres query failed") {
		t.Fatalf("expected ERROR log for failed query, got: %s", out)
	}
	if strings.Contains(out, "secret-password-never-log") {
		t.Fatalf("error log leaked bound parameter value: %s", out)
	}
}

func TestCompactSQLTruncation(t *testing.T) {
	t.Parallel()

	longSQL := "SELECT " + strings.Repeat("a, ", 300) + "z"
	got := compactSQL(longSQL, 64)
	if len(got) > 67 { // 64 + "..."
		t.Fatalf("compactSQL length = %d, expected <= 67", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated SQL to end with '...', got %q", got)
	}
}
