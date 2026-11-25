package httpclient

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

func TestRetryPolicy429503IdempotencyAndNoRetryCases(t *testing.T) {
	t.Parallel()

	t.Run("GET retries 429 and 503 and succeeds on 3rd attempt", func(t *testing.T) {
		t.Parallel()
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			switch n {
			case 1:
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
			case 2:
				w.WriteHeader(http.StatusServiceUnavailable)
			default:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("recovered"))
			}
		}))
		defer srv.Close()

		client, err := NewTrusted(Options{
			Service: "feishu",
			Retry: RetryPolicy{
				MaxAttempts:   3,
				BaseBackoff:   5 * time.Millisecond,
				MaxBackoff:    20 * time.Millisecond,
				MaxRetryAfter: 1 * time.Second,
			},
			Breaker: BreakerPolicy{Enabled: false, MaxHostEntries: 16},
		})
		if err != nil {
			t.Fatalf("NewTrusted() error = %v", err)
		}

		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/data", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK || attempts.Load() != 3 {
			t.Fatalf("status=%d attempts=%d, want 200 and 3", resp.StatusCode, attempts.Load())
		}
	})

	t.Run("exhausted 429 retries returns ClientError KindRateLimited", func(t *testing.T) {
		t.Parallel()
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		client, _ := NewTrusted(Options{
			Service: "llm",
			Retry: RetryPolicy{
				MaxAttempts: 3,
				BaseBackoff: 2 * time.Millisecond,
				MaxBackoff:  10 * time.Millisecond,
			},
			Breaker: BreakerPolicy{Enabled: false, MaxHostEntries: 16},
		})

		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/rate", nil)
		_, err := client.Do(req)
		if err == nil {
			t.Fatalf("expected error on exhausted 429 retries")
		}
		var ce *ClientError
		if !errors.As(err, &ce) || ce.Kind != KindRateLimited || ce.Attempts != 3 || ce.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("got %+v, want ClientError{KindRateLimited, Attempts:3, Status:429}", ce)
		}
	})

	t.Run("non-idempotent POST does not retry; WithIdempotent POST with GetBody retries", func(t *testing.T) {
		t.Parallel()
		var postAttempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := postAttempts.Add(1)
			body, _ := io.ReadAll(r.Body)
			if string(body) != "payload" {
				t.Errorf("attempt %d body = %q, want payload", n, string(body))
			}
			if n < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		client, _ := NewTrusted(Options{
			Service: "dingtalk",
			Retry: RetryPolicy{
				MaxAttempts: 3,
				BaseBackoff: 2 * time.Millisecond,
				MaxBackoff:  10 * time.Millisecond,
			},
			Breaker: BreakerPolicy{Enabled: false, MaxHostEntries: 16},
		})

		// Plain POST: must only attempt once
		req1, _ := http.NewRequest(http.MethodPost, srv.URL+"/create", bytes.NewReader([]byte("payload")))
		resp1, err := client.Do(req1)
		if err != nil {
			t.Fatalf("plain POST error = %v", err)
		}
		_ = resp1.Body.Close()
		if resp1.StatusCode != http.StatusServiceUnavailable || postAttempts.Load() != 1 {
			t.Fatalf("plain POST status=%d attempts=%d, want 503 and 1", resp1.StatusCode, postAttempts.Load())
		}

		// Idempotent POST with GetBody: retries and succeeds
		postAttempts.Store(0)
		ctx := WithIdempotent(context.Background())
		req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/query", bytes.NewReader([]byte("payload")))
		resp2, err := client.Do(req2)
		if err != nil {
			t.Fatalf("idempotent POST error = %v", err)
		}
		_ = resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK || postAttempts.Load() != 3 {
			t.Fatalf("idempotent POST status=%d attempts=%d, want 200 and 3", resp2.StatusCode, postAttempts.Load())
		}
	})

	t.Run("EgressDenied never retries and context cancel aborts retry wait", func(t *testing.T) {
		t.Parallel()
		untrusted, _ := NewUntrustedEgress(Options{
			Service: "link_reader",
			Retry:   RetryPolicy{MaxAttempts: 3, BaseBackoff: 5 * time.Millisecond},
		})
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:8080/x", nil)
		_, err := untrusted.Do(req)
		var ce *ClientError
		if !errors.As(err, &ce) || ce.Kind != KindEgressDenied || ce.Attempts != 1 || !errors.Is(err, apperror.ErrEgressDenied) {
			t.Fatalf("EgressDenied retry check failed: ce=%+v err=%v", ce, err)
		}

		// Context cancel during Retry-After wait
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		client, _ := NewTrusted(Options{
			Service: "cancel-test",
			Retry:   RetryPolicy{MaxAttempts: 3, MaxRetryAfter: 10 * time.Second},
			Breaker: BreakerPolicy{Enabled: false, MaxHostEntries: 16},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		cReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/slow-retry", nil)
		_, err = client.Do(cReq)
		if k, ok := KindOf(err); !ok || (k != KindTimeout && k != KindCanceled) {
			t.Fatalf("expected timeout/canceled on aborted retry wait, got (%q, %v) err=%v", k, ok, err)
		}
	})
}

func TestParseRetryAfterSecondsAndHTTPDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	d, ok := parseRetryAfter("5", now, 30*time.Second)
	if !ok || d != 5*time.Second {
		t.Fatalf("parseRetryAfter('5') = (%v, %v), want (5s, true)", d, ok)
	}

	d, ok = parseRetryAfter("120", now, 30*time.Second)
	if !ok || d != 30*time.Second {
		t.Fatalf("parseRetryAfter('120' clamped) = (%v, %v), want (30s, true)", d, ok)
	}

	futureDate := now.Add(12 * time.Second).Format(http.TimeFormat)
	d, ok = parseRetryAfter(futureDate, now, 30*time.Second)
	if !ok || d != 12*time.Second {
		t.Fatalf("parseRetryAfter(HTTP-Date) = (%v, %v), want (12s, true)", d, ok)
	}

	if _, ok := parseRetryAfter("not-a-date", now, 30*time.Second); ok {
		t.Fatalf("expected parseRetryAfter(invalid) == false")
	}
}

func TestRetryExhausted503AndNonRetryableErrorTypes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client, _ := NewTrusted(Options{
		Service: "catalog",
		Retry: RetryPolicy{
			MaxAttempts: 2,
			BaseBackoff: 2 * time.Millisecond,
			MaxBackoff:  5 * time.Millisecond,
		},
		Breaker: BreakerPolicy{Enabled: false, MaxHostEntries: 8},
	})

	// nil request safety
	if _, err := client.Do(nil); err == nil {
		t.Fatalf("Do(nil) must return error")
	}

	// Exhausted 503 retries -> KindUpstream5xx
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/503", nil)
	_, err := client.Do(req)
	var ce *ClientError
	if !errors.As(err, &ce) || ce.Kind != KindUpstream5xx || ce.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected KindUpstream5xx on exhausted 503, got %+v (%v)", ce, err)
	}
	if !strings.Contains(ce.Error(), "upstream_5xx") {
		t.Fatalf("ClientError.Error() = %q, expected upstream_5xx", ce.Error())
	}

	// TLS certificate errors, EgressDenied, CircuitOpen, Canceled must not be retried; transient net error is retryable
	if isRetryableError(x509.UnknownAuthorityError{}) {
		t.Errorf("x509.UnknownAuthorityError must not be retried")
	}
	if isRetryableError(&ClientError{Kind: KindCircuitOpen}) {
		t.Errorf("KindCircuitOpen must not be retried")
	}
	if !isRetryableError(errors.New("temporary reset")) {
		t.Errorf("transient network error should be retryable")
	}
}
