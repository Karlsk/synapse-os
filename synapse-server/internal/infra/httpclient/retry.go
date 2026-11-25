package httpclient

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker/v2"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

type retryRT struct {
	next         http.RoundTripper
	service      string
	policy       RetryPolicy
	retriesTotal *prometheus.CounterVec
}

func newRetryRT(next http.RoundTripper, service string, policy RetryPolicy, reg prometheus.Registerer) *retryRT {
	rt := &retryRT{
		next:    next,
		service: service,
		policy:  policy,
	}
	if reg != nil {
		cv := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "synapse_http_client_retries_total",
			Help: "Total outbound HTTP retry attempts partitioned by service and reason.",
		}, []string{"service", "reason"})
		if err := reg.Register(cv); err == nil {
			rt.retriesTotal = cv
		} else if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
				rt.retriesTotal = existing
			}
		}
	}
	return rt
}

func (rt *retryRT) RoundTrip(req *http.Request) (*http.Response, error) {
	canRetry := isRetryableRequest(req)
	maxAttempts := rt.policy.MaxAttempts
	if !canRetry {
		maxAttempts = 1
	}

	svc := serviceFromContext(req.Context(), rt.service)
	host := ""
	if req.URL != nil {
		host = req.URL.Host
	}

	var lastErr error
	var lastStatus int
	var lastRetryAfter time.Duration

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := resetRequestBody(req); err != nil {
				return nil, classifyError(svc, host, attempt, lastStatus, lastRetryAfter, err)
			}
		}
		resp, err := rt.next.RoundTrip(req)
		if err == nil {
			if !isRetryableStatus(resp.StatusCode) {
				return resp, nil
			}
			delay := rt.computeDelay(attempt, resp.Header.Get("Retry-After"))
			lastStatus = resp.StatusCode
			lastRetryAfter = delay

			if attempt == maxAttempts {
				// Single-attempt mode (non-idempotent / no retry): return raw response to caller.
				if maxAttempts == 1 {
					return resp, nil
				}
				// Retries exhausted on 429/502/503/504: close body and return structured *ClientError.
				drainAndClose(resp.Body)
				return nil, exhaustedStatusError(svc, host, attempt, resp.StatusCode, delay)
			}

			drainAndClose(resp.Body)
			rt.recordRetry(svc, strconv.Itoa(resp.StatusCode))
			if sleepErr := sleepWithContext(req.Context(), delay); sleepErr != nil {
				return nil, classifyError(svc, host, attempt, lastStatus, lastRetryAfter, sleepErr)
			}
			continue
		}

		if !isRetryableError(err) || attempt == maxAttempts {
			return nil, classifyError(svc, host, attempt, lastStatus, lastRetryAfter, err)
		}
		lastErr = err
		rt.recordRetry(svc, "network")
		delay := fullJitterBackoff(attempt, rt.policy.BaseBackoff, rt.policy.MaxBackoff)
		if sleepErr := sleepWithContext(req.Context(), delay); sleepErr != nil {
			return nil, classifyError(svc, host, attempt, lastStatus, lastRetryAfter, sleepErr)
		}
	}
	return nil, classifyError(svc, host, maxAttempts, lastStatus, lastRetryAfter, lastErr)
}

func (rt *retryRT) computeDelay(attempt int, retryAfterHeader string) time.Duration {
	if d, ok := parseRetryAfter(retryAfterHeader, time.Now(), rt.policy.MaxRetryAfter); ok {
		return d
	}
	return fullJitterBackoff(attempt, rt.policy.BaseBackoff, rt.policy.MaxBackoff)
}

func (rt *retryRT) recordRetry(service, reason string) {
	if rt.retriesTotal != nil {
		rt.retriesTotal.WithLabelValues(service, reason).Inc()
	}
}

func exhaustedStatusError(service, host string, attempts, status int, retryAfter time.Duration) *ClientError {
	if status == http.StatusTooManyRequests {
		return &ClientError{
			Kind:       KindRateLimited,
			Service:    service,
			Host:       host,
			StatusCode: status,
			RetryAfter: retryAfter,
			Attempts:   attempts,
			Err:        apperror.RateLimited(fmt.Sprintf("upstream %s rate limited (429)", service)),
		}
	}
	return &ClientError{
		Kind:       KindUpstream5xx,
		Service:    service,
		Host:       host,
		StatusCode: status,
		RetryAfter: retryAfter,
		Attempts:   attempts,
		Err:        apperror.ServiceUnavailable(fmt.Sprintf("upstream %s returned %d", service, status), nil),
	}
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64*1024))
	_ = body.Close()
}

func isRetryableRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	idempotent := isIdempotentContext(req.Context())
	if !idempotent {
		switch req.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
			idempotent = true
		}
	}
	if !idempotent {
		return false
	}
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func resetRequestBody(req *http.Request) error {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody == nil {
		return errors.New("request body is not replayable")
	}
	body, err := req.GetBody()
	if err != nil {
		return fmt.Errorf("reset request body: %w", err)
	}
	req.Body = body
	return nil
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, apperror.ErrEgressDenied) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, gobreaker.ErrOpenState) ||
		errors.Is(err, gobreaker.ErrTooManyRequests) {
		return false
	}
	if kind, ok := KindOf(err); ok {
		if kind == KindEgressDenied || kind == KindCircuitOpen || kind == KindCanceled || kind == KindResponseTooLarge {
			return false
		}
	}
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuth) || errors.As(err, &hostErr) || errors.As(err, &certInvalid) {
		return false
	}
	return true
}

// parseRetryAfter supports RFC 9110 delta-seconds and HTTP-Date formats, clamped to maxWait.
func parseRetryAfter(val string, now time.Time, maxWait time.Duration) (time.Duration, bool) {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(val); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		if maxWait > 0 && d > maxWait {
			d = maxWait
		}
		return d, true
	}
	if t, err := http.ParseTime(val); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		if maxWait > 0 && d > maxWait {
			d = maxWait
		}
		return d, true
	}
	return 0, false
}

func fullJitterBackoff(attempt int, base, maxBackoff time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	capDelay := base
	for i := 1; i < attempt; i++ {
		capDelay *= 2
		if maxBackoff > 0 && capDelay >= maxBackoff {
			capDelay = maxBackoff
			break
		}
	}
	if maxBackoff > 0 && capDelay > maxBackoff {
		capDelay = maxBackoff
	}
	if capDelay <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(capDelay) + 1))
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
