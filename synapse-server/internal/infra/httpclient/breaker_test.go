package httpclient

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

func TestCircuitBreakerHostIsolationHalfOpenRecoveryAndExclusions(t *testing.T) {
	t.Parallel()

	var failA atomic.Bool
	failA.Store(true)
	var hitsA atomic.Int32
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsA.Add(1)
		if failA.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srvB.Close()

	client, err := NewTrusted(Options{
		Service: "llm",
		Retry:   RetryPolicy{MaxAttempts: 1},
		Breaker: BreakerPolicy{
			Enabled:         true,
			Interval:        time.Minute,
			Timeout:         120 * time.Millisecond,
			MaxRequests:     1,
			MinRequests:     5,
			FailureRatio:    0.6,
			ConsecutiveFail: 5,
			MaxHostEntries:  2,
		},
	})
	if err != nil {
		t.Fatalf("NewTrusted() error = %v", err)
	}

	// 5 consecutive 503s trip Host A open
	for i := range 5 {
		req, _ := http.NewRequest(http.MethodGet, srvA.URL+"/chat", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("call %d unexpected err: %v", i+1, err)
		}
		_ = resp.Body.Close()
	}

	// 6th call to Host A must fail fast with KindCircuitOpen without reaching srvA
	hitsBeforeOpen := hitsA.Load()
	reqOpen, _ := http.NewRequest(http.MethodGet, srvA.URL+"/chat", nil)
	_, err = client.Do(reqOpen)
	if k, ok := KindOf(err); !ok || k != KindCircuitOpen {
		t.Fatalf("expected KindCircuitOpen on 6th call, got (%q, %v) err=%v", k, ok, err)
	}
	if hitsA.Load() != hitsBeforeOpen {
		t.Fatalf("circuit open call must not hit srvA")
	}

	// Host B remains completely unaffected
	reqB, _ := http.NewRequest(http.MethodGet, srvB.URL+"/ok", nil)
	respB, err := client.Do(reqB)
	if err != nil || respB.StatusCode != http.StatusOK {
		t.Fatalf("Host B should remain healthy: resp=%v err=%v", respB, err)
	}
	_ = respB.Body.Close()

	// Wait for Open -> HalfOpen cooldown, recover srvA, verify probe closes breaker
	failA.Store(false)
	time.Sleep(150 * time.Millisecond)

	reqProbe, _ := http.NewRequest(http.MethodGet, srvA.URL+"/chat", nil)
	respProbe, err := client.Do(reqProbe)
	if err != nil || respProbe.StatusCode != http.StatusOK {
		t.Fatalf("HalfOpen probe failed: resp=%v err=%v", respProbe, err)
	}
	_ = respProbe.Body.Close()

	reqAfterClose, _ := http.NewRequest(http.MethodGet, srvA.URL+"/chat", nil)
	respAfter, err := client.Do(reqAfterClose)
	if err != nil || respAfter.StatusCode != http.StatusOK {
		t.Fatalf("post-recovery call failed: resp=%v err=%v", respAfter, err)
	}
	_ = respAfter.Body.Close()
}

func TestCircuitBreakerIgnores404AndEgressDenied(t *testing.T) {
	t.Parallel()

	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv404.Close()

	trusted, _ := NewTrusted(Options{
		Service: "docs",
		Retry:   RetryPolicy{MaxAttempts: 1},
		Breaker: BreakerPolicy{
			Enabled:         true,
			MinRequests:     3,
			ConsecutiveFail: 3,
			FailureRatio:    0.5,
		},
	})
	for i := range 10 {
		req, _ := http.NewRequest(http.MethodGet, srv404.URL+"/missing", nil)
		resp, err := trusted.Do(req)
		if err != nil {
			t.Fatalf("404 #%d must not trip breaker, got err=%v", i+1, err)
		}
		_ = resp.Body.Close()
	}

	untrusted, _ := NewUntrustedEgress(Options{
		Service: "link_reader",
		Retry:   RetryPolicy{MaxAttempts: 1},
		Breaker: BreakerPolicy{
			Enabled:         true,
			MinRequests:     3,
			ConsecutiveFail: 3,
			FailureRatio:    0.5,
		},
	})
	for i := range 10 {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:8080/private", nil)
		_, err := untrusted.Do(req)
		if k, ok := KindOf(err); !ok || k != KindEgressDenied {
			t.Fatalf("EgressDenied #%d tripped breaker or wrong kind: (%q, %v) err=%v", i+1, k, ok, err)
		}
	}
}

func TestCircuitBreakerLRUEvictionAndSharedRegisterer(t *testing.T) {
	t.Parallel()

	metrics, err := obs.NewMetrics("synapse-server")
	if err != nil {
		t.Fatalf("obs.NewMetrics: %v", err)
	}
	// Create two clients sharing the same Registerer to verify AlreadyRegisteredError reuse
	_, _ = NewTrusted(Options{Service: "shared-a", Registerer: metrics.Registerer()})
	_, _ = NewTrusted(Options{Service: "shared-b", Registerer: metrics.Registerer()})

	brt := newBreakerRT(http.DefaultTransport, "lru", BreakerPolicy{
		Enabled:         true,
		MaxHostEntries:  2,
		ConsecutiveFail: 5,
		MinRequests:     5,
		FailureRatio:    0.6,
	}, metrics.Registerer())

	_ = brt.getOrCreate("lru", "h1.example")
	_ = brt.getOrCreate("lru", "h2.example")
	_ = brt.getOrCreate("lru", "h3.example") // evicts h1.example
	if len(brt.entries) != 2 {
		t.Fatalf("LRU entries = %d, want 2", len(brt.entries))
	}
	if _, exists := brt.entries["lru:h1.example"]; exists {
		t.Fatalf("expected oldest host h1.example to be evicted")
	}
}
