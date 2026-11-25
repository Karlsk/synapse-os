// Package httpclient provides physically separated Trusted and Untrusted outbound
// HTTP clients with layered timeouts, SSE streaming mode, exponential backoff retries,
// per-host gobreaker/v2 circuit breakers, redacted logging, and Prometheus metrics.
// It classifies failures via *ClientError and NEVER embeds business fallback logic.
package httpclient

import (
	"context"
	"net/http"
	"strings"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

type ctxKey int

const (
	ctxKeyIdempotent ctxKey = iota
	ctxKeyService
)

// Client is a profile-bound outbound HTTP client.
type Client struct {
	http    *http.Client
	profile Profile
	service string
}

// Do executes req through the client's layered RoundTripper stack and normalizes
// transport/resilience/redirect errors into *ClientError.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, &ClientError{
			Kind:    KindNetwork,
			Service: c.service,
			Err:     apperror.InvalidArg("http request must not be nil"),
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		svc := serviceFromContext(req.Context(), c.service)
		host := ""
		if req.URL != nil {
			host = req.URL.Host
		}
		return nil, classifyError(svc, host, 1, 0, 0, err)
	}
	return resp, nil
}

// StdClient returns the underlying *http.Client (retaining the full RoundTripper
// and CheckRedirect chain) for integration with third-party SDKs (e.g. S3/Feishu).
func (c *Client) StdClient() *http.Client {
	return c.http
}

// Profile returns whether this client uses ProfileTrusted or ProfileUntrusted.
func (c *Client) Profile() Profile {
	return c.profile
}

// WithIdempotent marks a non-GET request (such as a query-style POST) as safe to retry.
func WithIdempotent(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKeyIdempotent, true)
}

func isIdempotentContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(ctxKeyIdempotent).(bool)
	return v
}

// WithService overrides the logical upstream service label on ctx for logging/metrics.
func WithService(ctx context.Context, service string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKeyService, strings.TrimSpace(service))
}

func serviceFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if v, ok := ctx.Value(ctxKeyService).(string); ok && v != "" {
			return v
		}
	}
	if fallback != "" {
		return fallback
	}
	return "default"
}
