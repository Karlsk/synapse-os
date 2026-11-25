package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Overrides carries highest-priority CLI flag values that override env and YAML.
type Overrides struct {
	Listen string
	UIDir  string
}

// Load reads an optional YAML configuration file (if path is non-empty), applies
// environment variable overrides and CLI Overrides, and runs fail-fast Validate().
func Load(path string, ov Overrides) (*Config, error) {
	var data []byte
	if p := strings.TrimSpace(path); p != "" {
		var err error
		data, err = os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read config file %q: %w", p, err)
		}
	}
	return LoadFromBytes(data, os.LookupEnv, ov)
}

// LoadFromBytes parses YAML with KnownFields(true), applies env and CLI overrides
// in strict CLI > Env > YAML > Default order, and executes Validate().
func LoadFromBytes(yamlData []byte, lookupEnv func(string) (string, bool), ov Overrides) (*Config, error) {
	cfg := Default()
	if len(bytes.TrimSpace(yamlData)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(yamlData))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("parse config yaml: %w", err)
		}
	}
	if lookupEnv != nil {
		if err := applyEnvOverrides(cfg, lookupEnv); err != nil {
			return nil, err
		}
	}
	if ov.Listen != "" {
		cfg.Server.Listen = ov.Listen
	}
	if ov.UIDir != "" {
		cfg.Server.UIDir = ov.UIDir
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func firstEnv(lookup func(string) (string, bool), keys ...string) (string, string, bool) {
	for _, k := range keys {
		if v, ok := lookup(k); ok {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return k, trimmed, true
			}
		}
	}
	return "", "", false
}

func applyEnvOverrides(cfg *Config, lookup func(string) (string, bool)) error {
	if k, v, ok := firstEnv(lookup, "SYNAPSE_LISTEN", "SERVER_PORT"); ok {
		if k == "SERVER_PORT" && !strings.Contains(v, ":") {
			v = ":" + v
		}
		cfg.Server.Listen = v
	}
	if k, v, ok := firstEnv(lookup, "SYNAPSE_SHUTDOWN_TIMEOUT"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("parse env %s=%q: %w", k, v, err)
		}
		cfg.Server.ShutdownTimeout = d
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_UI_DIR"); ok {
		cfg.Server.UIDir = v
	}
	if k, v, ok := firstEnv(lookup, "SYNAPSE_COOKIE_SECURE"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("parse env %s=%q: %w", k, v, err)
		}
		cfg.Server.SessionCookieSecure = b
	}
	if k, v, ok := firstEnv(lookup, "SYNAPSE_TRUST_PROXY"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("parse env %s=%q: %w", k, v, err)
		}
		cfg.Server.TrustProxyHeaders = b
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_BASE_URL", "SUPERAGENT_BASE_URL"); ok {
		cfg.App.BaseURL = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_PG_DSN", "DATABASE_URL"); ok {
		cfg.Storage.Postgres.DSN = v
	}
	if k, v, ok := firstEnv(lookup, "SYNAPSE_PG_MAX_CONNS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("parse env %s=%q: %w", k, v, err)
		}
		cfg.Storage.Postgres.MaxConns = n
	}
	if k, v, ok := firstEnv(lookup, "SYNAPSE_AUTO_MIGRATE"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("parse env %s=%q: %w", k, v, err)
		}
		cfg.Storage.AutoMigrate = b
	}
	if _, v, ok := firstEnv(lookup, "SECRET_KEY", "SUPERAGENT_SECRET_KEY"); ok {
		cfg.Secret.Key = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_SECRET_KEY_FILE"); ok {
		cfg.Secret.KeyFile = v
	}
	if _, v, ok := firstEnv(lookup, "HTTP_PROXY", "http_proxy"); ok {
		cfg.Network.HTTPProxy = v
	}
	if _, v, ok := firstEnv(lookup, "HTTPS_PROXY", "https_proxy"); ok {
		cfg.Network.HTTPSProxy = v
	}
	if _, v, ok := firstEnv(lookup, "NO_PROXY", "no_proxy"); ok {
		cfg.Network.NoProxy = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_CA_BUNDLE_FILE", "NODE_EXTRA_CA_CERTS"); ok {
		cfg.Network.CABundleFile = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_NODE_DIR"); ok {
		cfg.Runtime.NodeDir = v
	}
	if _, v, ok := firstEnv(lookup, "NPM_CONFIG_REGISTRY"); ok {
		cfg.Runtime.NPMRegistry = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_NODE_DIST_MIRROR"); ok {
		cfg.Runtime.NodeDistMirror = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_DATA_DIR"); ok {
		cfg.Paths = PathsConfig{
			Workspaces:  filepath.Join(v, "workspaces"),
			Tasks:       filepath.Join(v, "tasks"),
			Files:       filepath.Join(v, "files"),
			Attachments: filepath.Join(v, "attachments"),
			Avatars:     filepath.Join(v, "avatars"),
			Branding:    filepath.Join(v, "branding"),
			Skills:      filepath.Join(v, "skills"),
		}
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_LOG_LEVEL"); ok {
		cfg.Observability.LogLevel = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_LOG_FORMAT"); ok {
		cfg.Observability.LogFormat = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_LOG_FILE"); ok {
		cfg.Observability.LogFile = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_PROMETHEUS_TOKEN"); ok {
		cfg.Observability.PrometheusToken = v
	}
	if _, v, ok := firstEnv(lookup, "SYNAPSE_SUPER_ADMIN_TOKEN"); ok {
		cfg.Platform.SuperAdminToken = v
	}
	return nil
}
