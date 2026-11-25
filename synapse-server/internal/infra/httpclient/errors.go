package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

// ErrorKind classifies outbound HTTP transport/resilience failures so business
// callers can decide fallback policies without httpclient embedding business logic.
type ErrorKind string

const (
	KindEgressDenied     ErrorKind = "egress_denied"
	KindCircuitOpen      ErrorKind = "circuit_open"
	KindTimeout          ErrorKind = "timeout"
	KindRateLimited      ErrorKind = "rate_limited"
	KindUpstream5xx      ErrorKind = "upstream_5xx"
	KindResponseTooLarge ErrorKind = "response_too_large"
	KindNetwork          ErrorKind = "network"
	KindCanceled         ErrorKind = "canceled"
)

// ClientError carries structured failure metadata for outbound HTTP calls.
type ClientError struct {
	Kind       ErrorKind
	Service    string
	Host       string
	StatusCode int
	RetryAfter time.Duration
	Attempts   int
	Err        error
}

// Error returns a concise diagnostic description of the outbound HTTP failure.
func (e *ClientError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("httpclient %s (%s/%s): %v", e.Kind, e.Service, e.Host, e.Err)
	}
	return fmt.Sprintf("httpclient %s (%s/%s, status=%d)", e.Kind, e.Service, e.Host, e.StatusCode)
}

// Unwrap exposes the underlying cause so errors.Is/As work (e.g. apperror.ErrEgressDenied, apperror.CodeTooLarge).
func (e *ClientError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// KindOf extracts the ErrorKind from err if it wraps a *ClientError.
func KindOf(err error) (ErrorKind, bool) {
	var ce *ClientError
	if errors.As(err, &ce) && ce != nil {
		return ce.Kind, true
	}
	return "", false
}

func classifyError(service, host string, attempts, status int, retryAfter time.Duration, err error) *ClientError {
	if err == nil {
		return nil
	}
	var existing *ClientError
	if errors.As(err, &existing) && existing != nil {
		cp := *existing
		if cp.Service == "" {
			cp.Service = service
		}
		if cp.Host == "" {
			cp.Host = host
		}
		if cp.Attempts == 0 && attempts > 0 {
			cp.Attempts = attempts
		}
		return &cp
	}

	// Unwrap *url.Error emitted by net/http.Client while keeping original err if needed.
	inner := err
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		inner = urlErr.Err
	}

	kind := KindNetwork
	switch {
	case errors.Is(inner, apperror.ErrEgressDenied):
		kind = KindEgressDenied
	case errors.Is(inner, gobreaker.ErrOpenState), errors.Is(inner, gobreaker.ErrTooManyRequests):
		kind = KindCircuitOpen
	case errors.Is(inner, context.Canceled):
		kind = KindCanceled
	case errors.Is(inner, context.DeadlineExceeded):
		kind = KindTimeout
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			kind = KindTimeout
		}
	}

	return &ClientError{
		Kind:       kind,
		Service:    service,
		Host:       host,
		StatusCode: status,
		RetryAfter: retryAfter,
		Attempts:   attempts,
		Err:        inner,
	}
}
