package obs

import (
	"context"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

var (
	bearerRe      = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	dsnCredRe     = regexp.MustCompile(`(postgres(?:ql)?://[^:@/\s]+):([^@\s]+)@`)
	sensitiveKeys = []string{
		"password", "passwd", "secret", "api_key", "apikey", "token",
		"authorization", "cookie", "set_cookie", "private_key",
		"key_cipher", "secret_cipher", "data_key_cipher", "credential",
	}
)

type redactingHandler struct {
	next slog.Handler
}

// NewRedactingHandler wraps next to automatically redact sensitive keys, nested
// groups, inline Bearer/Basic tokens, and PostgreSQL DSN passwords.
func NewRedactingHandler(next slog.Handler) slog.Handler {
	return &redactingHandler{next: next}
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, redactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

// isSensitiveKey matches common secret key names case-insensitively across hyphen/underscore variants.
func isSensitiveKey(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, pat := range sensitiveKeys {
		if strings.Contains(k, pat) {
			return true
		}
	}
	return false
}

func redactString(s string) string {
	s = dsnCredRe.ReplaceAllString(s, "${1}:***@")
	s = bearerRe.ReplaceAllString(s, "${1} "+RedactedValue)
	return s
}

func redactAttr(a slog.Attr) slog.Attr {
	// Resolve LogValuer implementations before inspecting kind/value so custom types cannot leak secrets.
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		out := make([]slog.Attr, len(attrs))
		for i, sub := range attrs {
			out[i] = redactAttr(sub)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, RedactedValue)
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, redactString(a.Value.String()))
	}
	if errVal, ok := a.Value.Any().(error); ok && errVal != nil {
		return slog.String(a.Key, redactString(errVal.Error()))
	}
	return a
}

// RedactDSN masks the password segment in postgres:// / postgresql:// connection strings.
func RedactDSN(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return dsnCredRe.ReplaceAllString(raw, "${1}:***@")
	}
	if _, hasPwd := u.User.Password(); hasPwd {
		// Reconstruct manually to avoid url.URL.String() percent-encoding "***" or special characters unexpectedly.
		u.User = url.UserPassword(u.User.Username(), "***")
		return strings.Replace(u.String(), "%2A%2A%2A", "***", 1)
	}
	return u.String()
}
