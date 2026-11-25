// Package obs provides context-aware structured logging with automatic secret
// redaction, request ID sanitization, and isolated Prometheus metrics registries.
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

const (
	RequestIDHeader = "X-Request-Id"
	AttrRequestID   = "request_id"
	AttrCompanyID   = "company_id"
	AttrUserID      = "user_id"
	RedactedValue   = "[REDACTED]"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyCompanyID
	ctxKeyUserID
)

var safeRequestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// WithRequestID attaches request_id to ctx for structured logging and tracing.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestIDFrom extracts request_id from ctx, returning "" when absent.
func RequestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// WithCompanyID attaches company_id to ctx strictly for observability metadata.
// Auth and tenant isolation MUST NEVER trust this value; use tenant.Require(ctx).
func WithCompanyID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyCompanyID, id)
}

// CompanyIDFrom extracts the observability company_id metadata from ctx.
func CompanyIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxKeyCompanyID).(string)
	return v
}

// WithUserID attaches user_id to ctx for structured logging and tracing.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}

// UserIDFrom extracts user_id from ctx, returning "" when absent.
func UserIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

// CopyMeta copies non-empty observability metadata (request_id, company_id, user_id)
// from src into dst, used by tenant.Detach when spawning detached background work.
func CopyMeta(src, dst context.Context) context.Context {
	if dst == nil {
		dst = context.Background()
	}
	if src == nil {
		return dst
	}
	if id := RequestIDFrom(src); id != "" {
		dst = WithRequestID(dst, id)
	}
	if id := CompanyIDFrom(src); id != "" {
		dst = WithCompanyID(dst, id)
	}
	if id := UserIDFrom(src); id != "" {
		dst = WithUserID(dst, id)
	}
	return dst
}

// SanitizeRequestID validates raw against ^[A-Za-z0-9._-]{1,64}$ to prevent log
// injection; invalid or empty values are replaced by a random 32-char lowercase hex ID.
func SanitizeRequestID(raw string) string {
	if safeRequestIDRe.MatchString(raw) {
		return raw
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
