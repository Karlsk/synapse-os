package obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LoggerConfig controls structured logger level, output format, optional log file
// dual-writing (0640), and writer override for testing.
type LoggerConfig struct {
	Level  string
	Format string
	File   string
	Output io.Writer
}

type contextHandler struct {
	next slog.Handler
}

// NewContextHandler wraps next to automatically attach non-empty request_id,
// company_id, and user_id attributes extracted from ctx.
func NewContextHandler(next slog.Handler) slog.Handler {
	return &contextHandler{next: next}
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		var extra [3]slog.Attr
		n := 0
		if id := RequestIDFrom(ctx); id != "" {
			extra[n] = slog.String(AttrRequestID, id)
			n++
		}
		if id := CompanyIDFrom(ctx); id != "" {
			extra[n] = slog.String(AttrCompanyID, id)
			n++
		}
		if id := UserIDFrom(ctx); id != "" {
			extra[n] = slog.String(AttrUserID, id)
			n++
		}
		if n > 0 {
			r = r.Clone()
			r.AddAttrs(extra[:n]...)
		}
	}
	return h.next.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}

// NewLogger constructs a slog.Logger wired as ContextHandler -> RedactingHandler -> Text/JSONHandler.
// When cfg.File is non-empty, output is dual-written to the file (mode 0640) and closeFn closes it.
func NewLogger(cfg LoggerConfig) (logger *slog.Logger, closeFn func() error, err error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}
	format := strings.ToLower(strings.TrimSpace(cfg.Format))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return nil, nil, fmt.Errorf("invalid log format %q (expected text or json)", cfg.Format)
	}

	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}

	closeFn = func() error { return nil }
	if filePath := strings.TrimSpace(cfg.File); filePath != "" {
		if dir := filepath.Dir(filePath); dir != "" && dir != "." {
			if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
				return nil, nil, fmt.Errorf("create log dir %q: %w", dir, mkErr)
			}
		}
		f, openErr := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if openErr != nil {
			return nil, nil, fmt.Errorf("open log file %q: %w", filePath, openErr)
		}
		out = io.MultiWriter(out, f)
		closeFn = f.Close
	}

	opts := &slog.HandlerOptions{Level: level}
	var base slog.Handler
	if format == "json" {
		base = slog.NewJSONHandler(out, opts)
	} else {
		base = slog.NewTextHandler(out, opts)
	}

	handler := NewContextHandler(NewRedactingHandler(base))
	return slog.New(handler), closeFn, nil
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q (expected debug, info, warn, or error)", raw)
	}
}
