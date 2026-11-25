package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mapLookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestConfigPrecedenceAndEnvOverrides(t *testing.T) {
	t.Parallel()

	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))

	yamlData := []byte(`
server:
  listen: ":18081"
  ui_dir: "/yaml/ui"
  shutdown_timeout: 45s
storage:
  auto_migrate: false
  postgres:
    dsn: "postgres://yaml:yaml@127.0.0.1:5432/yaml?sslmode=disable&TimeZone=UTC"
    max_conns: 12
observability:
  log_level: "warn"
  log_format: "json"
`)

	t.Run("CLI overrides Env overrides YAML overrides Default", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SYNAPSE_LISTEN":            ":19000",
			"SERVER_PORT":               "19090",
			"SYNAPSE_UI_DIR":            "/env/ui",
			"SYNAPSE_SHUTDOWN_TIMEOUT":  "25s",
			"SYNAPSE_COOKIE_SECURE":     "true",
			"SYNAPSE_PG_DSN":            "postgres://env:env@127.0.0.1:5432/env?sslmode=disable&TimeZone=UTC",
			"SYNAPSE_PG_MAX_CONNS":      "32",
			"SYNAPSE_AUTO_MIGRATE":      "true",
			"SECRET_KEY":                validKey,
			"SYNAPSE_SECRET_KEY_FILE":   "/var/lib/synapse/secret.key",
			"HTTP_PROXY":                "http://proxy.corp:8080",
			"HTTPS_PROXY":               "https://proxy.corp:8443",
			"NO_PROXY":                  "corp.local",
			"NPM_CONFIG_REGISTRY":       "https://registry.npmmirror.com",
			"SYNAPSE_NODE_DIST_MIRROR":  "https://npmmirror.com/mirrors/node",
			"SYNAPSE_DATA_DIR":          "/srv/synapse-data",
			"SYNAPSE_LOG_LEVEL":         "debug",
			"SYNAPSE_LOG_FORMAT":        "text",
			"SYNAPSE_LOG_FILE":          "/var/log/synapse.log",
			"SYNAPSE_BASE_URL":          "https://synapse.corp.example",
			"SYNAPSE_PROMETHEUS_TOKEN":  "prom-secret",
			"SYNAPSE_SUPER_ADMIN_TOKEN": "admin-secret",
		}
		ov := Overrides{
			Listen: ":20000",
			UIDir:  "/cli/ui",
		}

		cfg, err := LoadFromBytes(yamlData, mapLookup(env), ov)
		if err != nil {
			t.Fatalf("LoadFromBytes() error = %v", err)
		}

		if cfg.Server.Listen != ":20000" {
			t.Fatalf("Server.Listen = %q, want :20000", cfg.Server.Listen)
		}
		if cfg.Server.UIDir != "/cli/ui" {
			t.Fatalf("Server.UIDir = %q, want /cli/ui", cfg.Server.UIDir)
		}
		if cfg.Server.ShutdownTimeout != 25*time.Second {
			t.Fatalf("ShutdownTimeout = %v, want 25s", cfg.Server.ShutdownTimeout)
		}
		if !cfg.Server.SessionCookieSecure {
			t.Fatalf("SessionCookieSecure = false, want true")
		}
		if cfg.Storage.Postgres.DSN != env["SYNAPSE_PG_DSN"] {
			t.Fatalf("Postgres.DSN = %q", cfg.Storage.Postgres.DSN)
		}
		if cfg.Storage.Postgres.MaxConns != 32 {
			t.Fatalf("Postgres.MaxConns = %d, want 32", cfg.Storage.Postgres.MaxConns)
		}
		if !cfg.Storage.AutoMigrate {
			t.Fatalf("Storage.AutoMigrate = false, want true")
		}
		if cfg.Secret.Key != validKey {
			t.Fatalf("Secret.Key mismatch")
		}
		if cfg.Secret.KeyFile != "/var/lib/synapse/secret.key" {
			t.Fatalf("Secret.KeyFile = %q", cfg.Secret.KeyFile)
		}
		if cfg.Paths.Workspaces != filepath.Join("/srv/synapse-data", "workspaces") ||
			cfg.Paths.Skills != filepath.Join("/srv/synapse-data", "skills") ||
			cfg.Paths.Branding != filepath.Join("/srv/synapse-data", "branding") {
			t.Fatalf("Paths not relocated by SYNAPSE_DATA_DIR: %+v", cfg.Paths)
		}
		if cfg.Observability.LogLevel != "debug" || cfg.Observability.LogFormat != "text" || cfg.Observability.LogFile != "/var/log/synapse.log" {
			t.Fatalf("Observability env overrides mismatch: %+v", cfg.Observability)
		}
		if cfg.App.BaseURL != "https://synapse.corp.example" ||
			cfg.Observability.PrometheusToken != "prom-secret" ||
			cfg.Platform.SuperAdminToken != "admin-secret" {
			t.Fatalf("App/Observability/Platform env overrides mismatch")
		}
	})

	t.Run("SERVER_PORT bare port converts to colon prefix", func(t *testing.T) {
		t.Parallel()
		cfg, err := LoadFromBytes(nil, mapLookup(map[string]string{
			"SERVER_PORT": "19090",
		}), Overrides{})
		if err != nil {
			t.Fatalf("LoadFromBytes() error = %v", err)
		}
		if cfg.Server.Listen != ":19090" {
			t.Fatalf("Server.Listen = %q, want :19090", cfg.Server.Listen)
		}
	})

	t.Run("fallback env aliases work", func(t *testing.T) {
		t.Parallel()
		cfg, err := LoadFromBytes(nil, mapLookup(map[string]string{
			"DATABASE_URL":          "postgresql://u:p@db:5432/app?TimeZone=UTC",
			"SUPERAGENT_SECRET_KEY": validKey,
			"http_proxy":            "http://low-proxy:8080",
			"https_proxy":           "http://low-https:8080",
			"no_proxy":              "internal.local",
		}), Overrides{})
		if err != nil {
			t.Fatalf("LoadFromBytes() error = %v", err)
		}
		if cfg.Storage.Postgres.DSN != "postgresql://u:p@db:5432/app?TimeZone=UTC" {
			t.Fatalf("DATABASE_URL fallback failed: %q", cfg.Storage.Postgres.DSN)
		}
		if cfg.Secret.Key != validKey {
			t.Fatalf("SUPERAGENT_SECRET_KEY fallback failed")
		}
		if cfg.Network.HTTPProxy != "http://low-proxy:8080" || cfg.Network.HTTPSProxy != "http://low-https:8080" {
			t.Fatalf("lowercase proxy env fallback failed: %+v", cfg.Network)
		}
	})

	t.Run("unknown YAML field fails fast", func(t *testing.T) {
		t.Parallel()
		badYAML := []byte("server:\n  listne: \":18080\"\n")
		_, err := LoadFromBytes(badYAML, mapLookup(nil), Overrides{})
		if err == nil {
			t.Fatalf("expected error for unknown YAML field, got nil")
		}
		if !strings.Contains(err.Error(), "listne") {
			t.Fatalf("expected error to mention unknown field 'listne', got: %v", err)
		}
	})

	t.Run("invalid typed env var fails fast", func(t *testing.T) {
		t.Parallel()
		_, err := LoadFromBytes(nil, mapLookup(map[string]string{
			"SYNAPSE_PG_MAX_CONNS": "not-an-int",
		}), Overrides{})
		if err == nil {
			t.Fatalf("expected error for invalid SYNAPSE_PG_MAX_CONNS")
		}
	})
}

func TestValidateAggregatesAllErrors(t *testing.T) {
	t.Parallel()

	badCA := filepath.Join(t.TempDir(), "bad-ca.pem")
	if err := os.WriteFile(badCA, []byte("not-a-valid-pem"), 0o600); err != nil {
		t.Fatalf("write temp ca: %v", err)
	}

	cfg := Default()
	cfg.Server.Listen = "   "
	cfg.Server.ShutdownTimeout = 0
	cfg.Storage.Postgres.DSN = "mysql://root:pass@127.0.0.1:3306/db"
	cfg.Storage.Postgres.MinConns = 10
	cfg.Storage.Postgres.MaxConns = 4
	cfg.Secret.Key = base64.StdEncoding.EncodeToString([]byte("too-short"))
	cfg.Network.HTTPProxy = "ftp://proxy.local:21"
	cfg.Network.HTTPSProxy = "http://"
	cfg.Network.CABundleFile = badCA
	cfg.Observability.LogLevel = "verbose"
	cfg.Observability.LogFormat = "xml"
	cfg.Files.UploadMaxMB = 100
	cfg.Files.MaxMB = 50
	cfg.Task.MaxParallel = 0
	cfg.SuperAgent.Timezone = "Mars/Olympus_Mons"

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected aggregated validation error, got nil")
	}

	msg := err.Error()
	expectedSubstrings := []string{
		"server.listen",
		"server.shutdown_timeout",
		"storage.postgres.dsn",
		"invalid postgres pool bounds",
		"secret.key",
		"network.http_proxy",
		"network.https_proxy",
		"network.ca_bundle_file",
		"observability.log_level",
		"observability.log_format",
		"files.upload_max_mb",
		"task.max_parallel",
		"superagent.timezone",
	}
	for _, want := range expectedSubstrings {
		if !strings.Contains(msg, want) {
			t.Errorf("aggregated Validate() error missing %q; full error:\n%s", want, msg)
		}
	}
}

func TestLoadExampleConfigFileAndZeroRedis(t *testing.T) {
	t.Parallel()

	examplePath := filepath.Join("..", "..", "..", "configs", "config.example.yaml")
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read configs/config.example.yaml: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "redis") {
		t.Fatalf("configs/config.example.yaml MUST NOT contain any Redis configuration")
	}

	cfg, err := Load(examplePath, Overrides{})
	if err != nil {
		t.Fatalf("Load(config.example.yaml) failed: %v", err)
	}
	if cfg.Server.Listen != ":18080" {
		t.Fatalf("example listen = %q, want :18080", cfg.Server.Listen)
	}
	if !strings.Contains(cfg.Storage.Postgres.DSN, "TimeZone=UTC") {
		t.Fatalf("example postgres DSN must include TimeZone=UTC: %q", cfg.Storage.Postgres.DSN)
	}
	if cfg.Storage.Postgres.MaxConns != 16 || cfg.Storage.Postgres.MinConns != 2 {
		t.Fatalf("unexpected postgres pool defaults: min=%d max=%d", cfg.Storage.Postgres.MinConns, cfg.Storage.Postgres.MaxConns)
	}
	if cfg.Storage.Postgres.MaxConnIdleTime != 5*time.Minute || cfg.Storage.Postgres.SlowQueryThreshold != 200*time.Millisecond {
		t.Fatalf("unexpected postgres idle/slow defaults: idle=%v slow=%v", cfg.Storage.Postgres.MaxConnIdleTime, cfg.Storage.Postgres.SlowQueryThreshold)
	}
}

func TestLoadEmptyPathUsesDefaultsAndMissingPathFails(t *testing.T) {
	t.Parallel()

	cfg, err := Load("", Overrides{})
	if err != nil {
		t.Fatalf("Load(\"\") failed: %v", err)
	}
	if cfg.Server.Listen != ":18080" {
		t.Fatalf("default listen = %q, want :18080", cfg.Server.Listen)
	}

	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"), Overrides{}); err == nil {
		t.Fatalf("expected error for nonexistent config file")
	}
}
