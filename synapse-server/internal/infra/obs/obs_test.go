package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var hex32Re = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestSanitizeRequestIDAndContextPropagation(t *testing.T) {
	t.Parallel()

	t.Run("valid IDs preserved", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{"req-123", "trace.abc_01", strings.Repeat("a", 64)} {
			if got := SanitizeRequestID(raw); got != raw {
				t.Errorf("SanitizeRequestID(%q) = %q, want unchanged", raw, got)
			}
		}
	})

	t.Run("invalid or injection IDs replaced with 32-hex", func(t *testing.T) {
		t.Parallel()
		invalid := []string{
			"",
			"req\r\ninjected: true",
			"has space",
			"bad/slash",
			strings.Repeat("x", 65),
		}
		for _, raw := range invalid {
			got := SanitizeRequestID(raw)
			if !hex32Re.MatchString(got) {
				t.Errorf("SanitizeRequestID(%q) = %q, want 32-char lowercase hex", raw, got)
			}
		}
	})

	t.Run("ContextHandler injects request_id company_id user_id and CopyMeta preserves them", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		logger, closeFn, err := NewLogger(LoggerConfig{
			Level:  "debug",
			Format: "json",
			Output: &buf,
		})
		if err != nil {
			t.Fatalf("NewLogger() error = %v", err)
		}
		defer func() { _ = closeFn() }()

		src := WithRequestID(context.Background(), "req-42")
		src = WithCompanyID(src, "acme")
		src = WithUserID(src, "user-99")

		dst := CopyMeta(src, context.Background())
		if RequestIDFrom(dst) != "req-42" || CompanyIDFrom(dst) != "acme" || UserIDFrom(dst) != "user-99" {
			t.Fatalf("CopyMeta lost metadata: req=%q comp=%q user=%q", RequestIDFrom(dst), CompanyIDFrom(dst), UserIDFrom(dst))
		}

		logger.InfoContext(dst, "hello world")

		var entry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("unmarshal log json: %v (raw=%s)", err, buf.String())
		}
		if entry[AttrRequestID] != "req-42" || entry[AttrCompanyID] != "acme" || entry[AttrUserID] != "user-99" {
			t.Fatalf("log entry missing context attrs: %v", entry)
		}
	})
}

func TestRedactingHandlerAndRedactDSN(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, closeFn, err := NewLogger(LoggerConfig{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer func() { _ = closeFn() }()

	// Pre-bound sensitive attrs via With and grouped attrs via WithGroup must also be redacted
	child := logger.With("api_key", "sk-prebound-secret").WithGroup("nested")
	child.Info("sensitive test",
		"password", "p@ssw0rd123",
		"Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.payload",
		"secret_cipher", "v2:acme:ciphertext",
		"Set-Cookie", "SYNAPSE_SESSION=topsecret",
		slog.Group("auth",
			slog.String("token", "tok-abc-123"),
			slog.String("user", "alice"),
		),
		"header_dump", "Header: Bearer eyJhbGciOiJIUzI1NiJ9.inlineToken",
		"db_url", "postgres://synapse:mysecretpwd@127.0.0.1:5432/synapse?sslmode=disable",
		"err", errors.New("dial postgres://synapse:errpwd999@127.0.0.1:5432/synapse with Bearer eyJhbGciOiJIUzI1NiJ9.errToken"),
	)

	out := buf.String()
	forbiddenPlaintexts := []string{
		"sk-prebound-secret",
		"p@ssw0rd123",
		"eyJhbGciOiJIUzI1NiJ9",
		"v2:acme:ciphertext",
		"topsecret",
		"tok-abc-123",
		"mysecretpwd",
		"errpwd999",
		"errToken",
	}
	for _, secret := range forbiddenPlaintexts {
		if strings.Contains(out, secret) {
			t.Errorf("plaintext secret %q leaked into log output:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, RedactedValue) {
		t.Errorf("expected %q marker in log output:\n%s", RedactedValue, out)
	}
	if !strings.Contains(out, "postgres://synapse:***@127.0.0.1:5432/synapse") {
		t.Errorf("expected masked DSN in log output:\n%s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("expected non-sensitive field 'alice' to remain intact:\n%s", out)
	}

	t.Run("RedactDSN masks password cleanly", func(t *testing.T) {
		t.Parallel()
		got := RedactDSN("postgres://synapse:supersecret@10.0.0.5:5432/synapse?sslmode=require")
		if strings.Contains(got, "supersecret") {
			t.Fatalf("RedactDSN leaked password: %q", got)
		}
		if !strings.Contains(got, "synapse:") || !strings.Contains(got, "@10.0.0.5:5432/synapse") {
			t.Fatalf("RedactDSN corrupted structure: %q", got)
		}
		noPwd := "postgres://synapse@127.0.0.1:5432/synapse"
		if got := RedactDSN(noPwd); got != noPwd {
			t.Fatalf("RedactDSN(no password) = %q, want %q", got, noPwd)
		}
	})
}

func TestNewLoggerFileDualWriteAndValidation(t *testing.T) {
	t.Parallel()

	logFile := filepath.Join(t.TempDir(), "nested", "logs", "synapse.log")
	var buf bytes.Buffer

	logger, closeFn, err := NewLogger(LoggerConfig{
		Level:  "warn",
		Format: "text",
		File:   logFile,
		Output: &buf,
	})
	if err != nil {
		t.Fatalf("NewLogger(file) error = %v", err)
	}

	logger.Info("filtered out info")
	logger.Warn("visible warning", "token", "must-hide")

	if err := closeFn(); err != nil {
		t.Fatalf("closeFn() error = %v", err)
	}

	if strings.Contains(buf.String(), "filtered out info") {
		t.Fatalf("info log should be filtered at warn level")
	}
	if !strings.Contains(buf.String(), "visible warning") || strings.Contains(buf.String(), "must-hide") {
		t.Fatalf("unexpected buffer log content: %s", buf.String())
	}

	fileBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(fileBytes), "visible warning") || strings.Contains(string(fileBytes), "must-hide") {
		t.Fatalf("unexpected file log content: %s", string(fileBytes))
	}
	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0o040 && perm != 0o640 {
		// Allow umask tightening (e.g., 0600 or 0640), forbid world-readable or group-writable.
		if perm&0o027 != 0 {
			t.Fatalf("log file mode %o is too permissive (want <= 0640)", perm)
		}
	}

	if _, _, err := NewLogger(LoggerConfig{Level: "invalid"}); err == nil {
		t.Fatalf("expected error for invalid log level")
	}
	if _, _, err := NewLogger(LoggerConfig{Format: "yaml"}); err == nil {
		t.Fatalf("expected error for invalid log format")
	}
}

func TestMetricsIsolatedRegistriesAndCommonLabel(t *testing.T) {
	t.Parallel()

	m1, err := NewMetrics("synapse-server")
	if err != nil {
		t.Fatalf("first NewMetrics() error = %v", err)
	}
	// Second creation must never panic or collide with global default registry
	m2, err := NewMetrics("synapse-server")
	if err != nil {
		t.Fatalf("second NewMetrics() error = %v", err)
	}
	if m1.Registerer() == nil || m2.Gatherer() == nil {
		t.Fatalf("Registerer/Gatherer must not be nil")
	}

	// Touch all 6 superagent_* business metrics + http_server_requests_seconds
	m1.ChatTurnsTotal.WithLabelValues("acme", "claude", "done").Inc()
	m1.TaskRunsTotal.WithLabelValues("acme", "coding", "done").Inc()
	m1.TaskQueueDepth.WithLabelValues("acme", "pending").Set(3)
	m1.TokensTotal.WithLabelValues("acme", "claude", "claude-sonnet-4", "input").Add(1200)
	m1.CLIWarmPoolSize.Set(5)
	m1.IMConnected.WithLabelValues("acme", "feishu").Set(1)
	m1.HTTPRequestsSecs.WithLabelValues("GET", "/api/v1/meta", "200").Observe(0.015)

	families, err := m1.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	wantMetrics := map[string]bool{
		"superagent_chat_turns_total":   false,
		"superagent_task_runs_total":    false,
		"superagent_task_queue_depth":   false,
		"superagent_tokens_total":       false,
		"superagent_cli_warm_pool_size": false,
		"superagent_im_connected":       false,
		"http_server_requests_seconds":  false,
	}

	for _, mf := range families {
		name := mf.GetName()
		if _, tracked := wantMetrics[name]; !tracked {
			continue
		}
		wantMetrics[name] = true
		for _, m := range mf.GetMetric() {
			foundAppLabel := false
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "application" && lp.GetValue() == "synapse-server" {
					foundAppLabel = true
					break
				}
			}
			if !foundAppLabel {
				t.Errorf("metric %q missing constant label application=\"synapse-server\"", name)
			}
		}
	}

	for name, seen := range wantMetrics {
		if !seen {
			t.Errorf("expected metric %q in Gather() output", name)
		}
	}
}
