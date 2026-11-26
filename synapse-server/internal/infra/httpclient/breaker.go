package httpclient

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker/v2"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

type upstreamStatusErr struct {
	status int
}

func (e *upstreamStatusErr) Error() string {
	return fmt.Sprintf("upstream status %d", e.status)
}

type breakerEntry struct {
	key     string
	service string
	host    string
	cb      *gobreaker.CircuitBreaker[*http.Response]
}

type breakerRT struct {
	next       http.RoundTripper
	service    string
	policy     BreakerPolicy
	stateGauge *prometheus.GaugeVec

	mu       sync.Mutex
	entries  map[string]*list.Element
	lruOrder *list.List
}

func newBreakerRT(next http.RoundTripper, service string, policy BreakerPolicy, reg prometheus.Registerer) *breakerRT {
	b := &breakerRT{
		next:     next,
		service:  service,
		policy:   policy,
		entries:  make(map[string]*list.Element),
		lruOrder: list.New(),
	}
	if reg != nil {
		gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "synapse_http_client_breaker_state",
			Help: "Circuit breaker state per service and host (0=closed, 1=half_open, 2=open).",
		}, []string{"service", "host"})
		if err := reg.Register(gv); err == nil {
			b.stateGauge = gv
		} else if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.GaugeVec); ok {
				b.stateGauge = existing
			}
		}
	}
	return b
}

func (b *breakerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	svc := serviceFromContext(req.Context(), b.service)
	host := ""
	if req.URL != nil {
		host = strings.ToLower(req.URL.Host)
	}
	cb := b.getOrCreate(svc, host)

	var capturedResp *http.Response
	resp, err := cb.Execute(func() (*http.Response, error) {
		r, err := b.next.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		// Only 5xx and 429 count as upstream health failures; client 4xx (400/401/403/404) do not trip.
		if r.StatusCode >= 500 || r.StatusCode == http.StatusTooManyRequests {
			capturedResp = r
			return r, &upstreamStatusErr{status: r.StatusCode}
		}
		return r, nil
	})

	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return nil, &ClientError{
			Kind:    KindCircuitOpen,
			Service: svc,
			Host:    host,
			Err:     apperror.ServiceUnavailable("upstream circuit breaker open: "+svc, err),
		}
	}
	var statusErr *upstreamStatusErr
	if errors.As(err, &statusErr) && capturedResp != nil {
		// Return original *http.Response so outer retryRT or business caller decides next action.
		return capturedResp, nil
	}
	return resp, err
}

func (b *breakerRT) getOrCreate(service, host string) *gobreaker.CircuitBreaker[*http.Response] {
	key := service + ":" + host

	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.entries[key]; ok {
		b.lruOrder.MoveToFront(elem)
		return elem.Value.(*breakerEntry).cb
	}

	if b.policy.MaxHostEntries > 0 && b.lruOrder.Len() >= b.policy.MaxHostEntries {
		if oldest := b.lruOrder.Back(); oldest != nil {
			evicted := oldest.Value.(*breakerEntry)
			delete(b.entries, evicted.key)
			b.lruOrder.Remove(oldest)
			if b.stateGauge != nil {
				b.stateGauge.DeleteLabelValues(evicted.service, evicted.host)
			}
		}
	}

	entry := &breakerEntry{key: key, service: service, host: host}
	st := gobreaker.Settings{
		Name:        key,
		MaxRequests: b.policy.MaxRequests,
		Interval:    b.policy.Interval,
		Timeout:     b.policy.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.ConsecutiveFailures >= b.policy.ConsecutiveFail {
				return true
			}
			if counts.Requests >= b.policy.MinRequests {
				ratio := float64(counts.TotalFailures) / float64(counts.Requests)
				return ratio >= b.policy.FailureRatio
			}
			return false
		},
		IsSuccessful: func(err error) bool {
			if err == nil {
				return true
			}
			// Security denials and caller cancellations must never trip the upstream circuit breaker.
			return errors.Is(err, apperror.ErrEgressDenied) || errors.Is(err, context.Canceled)
		},
		OnStateChange: func(_ string, _ gobreaker.State, to gobreaker.State) {
			b.mu.Lock()
			defer b.mu.Unlock()
			if elem, ok := b.entries[key]; ok && elem.Value == entry {
				b.recordStateLocked(service, host, to)
			}
		},
	}

	entry.cb = gobreaker.NewCircuitBreaker[*http.Response](st)
	b.recordStateLocked(service, host, gobreaker.StateClosed)

	elem := b.lruOrder.PushFront(entry)
	b.entries[key] = elem
	return entry.cb
}

func (b *breakerRT) recordStateLocked(service, host string, state gobreaker.State) {
	if b.stateGauge == nil {
		return
	}
	var val float64
	switch state {
	case gobreaker.StateHalfOpen:
		val = 1
	case gobreaker.StateOpen:
		val = 2
	default:
		val = 0
	}
	b.stateGauge.WithLabelValues(service, host).Set(val)
}
