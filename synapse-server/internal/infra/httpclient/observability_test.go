package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

func TestObservabilityRedactedLogsAndPrometheusMetrics(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

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

	client, err := NewTrusted(Options{
		Service: "feishu",
		Retry: RetryPolicy{
			MaxAttempts: 2,
			BaseBackoff: 2 * time.Millisecond,
			MaxBackoff:  10 * time.Millisecond,
		},
		Breaker: BreakerPolicy{
			Enabled:         true,
			ConsecutiveFail: 5,
			MinRequests:     5,
		},
		Logger:     logger,
		Registerer: metrics.Registerer(),
	})
	if err != nil {
		t.Fatalf("NewTrusted: %v", err)
	}

	ctx := obs.WithRequestID(context.Background(), "req-http-1")
	ctx = obs.WithCompanyID(ctx, "acme")
	ctx = WithService(ctx, "feishu")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1?access_token=topsecret&page=1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-12345678")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = resp.Body.Close()

	rawLog := logBuf.String()
	if strings.Contains(rawLog, "topsecret") || strings.Contains(rawLog, "sk-12345678") {
		t.Fatalf("sensitive query param or Authorization header leaked into log:\n%s", rawLog)
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuf.Bytes()), &entry); err != nil {
		t.Fatalf("unmarshal log json: %v (raw=%s)", err, rawLog)
	}
	if entry["service"] != "feishu" || entry["profile"] != string(ProfileTrusted) {
		t.Errorf("unexpected service/profile in log: %v", entry)
	}
	if entry[obs.AttrRequestID] != "req-http-1" || entry[obs.AttrCompanyID] != "acme" {
		t.Errorf("missing obs context attrs in log: %v", entry)
	}
	if status, ok := entry["status"].(float64); !ok || int(status) != http.StatusOK {
		t.Errorf("status = %v, want 200", entry["status"])
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Errorf("missing duration_ms in log: %v", entry)
	}
	urlSanitized, _ := entry["url_sanitized"].(string)
	if !strings.Contains(urlSanitized, "page=1") || strings.Contains(urlSanitized, "topsecret") {
		t.Errorf("url_sanitized = %q did not sanitize cleanly", urlSanitized)
	}

	// Verify Prometheus metrics
	families, err := metrics.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather(): %v", err)
	}
	want := map[string]bool{
		"synapse_http_client_requests_total":           false,
		"synapse_http_client_request_duration_seconds": false,
		"synapse_http_client_retries_total":            false,
		"synapse_http_client_breaker_state":            false,
	}
	for _, mf := range families {
		if _, ok := want[mf.GetName()]; ok && len(mf.GetMetric()) > 0 {
			want[mf.GetName()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected Prometheus metric %q in Gather() output", name)
		}
	}

	// Verify sanitizeURL strips userinfo passwords as well
	uWithCreds, _ := url.Parse("https://alice:hunter2@api.example/v1?api_key=xyz&limit=10")
	sanitized := sanitizeURL(uWithCreds)
	if strings.Contains(sanitized, "hunter2") || strings.Contains(sanitized, "xyz") || !strings.Contains(sanitized, "limit=10") {
		t.Fatalf("sanitizeURL failed to mask userinfo/api_key cleanly: %q", sanitized)
	}
}
