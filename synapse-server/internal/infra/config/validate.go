package config

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Validate performs fail-fast startup validation and aggregates all detected
// configuration errors in a single return value.
func (c *Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Server.Listen) == "" {
		errs = append(errs, errors.New("server.listen must not be empty"))
	}
	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server.shutdown_timeout must be > 0"))
	}
	dsn := strings.TrimSpace(c.Storage.Postgres.DSN)
	if dsn == "" || (!strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://")) {
		errs = append(errs, errors.New("storage.postgres.dsn must start with postgres:// or postgresql://"))
	}
	if c.Storage.Postgres.MinConns < 1 || c.Storage.Postgres.MaxConns < c.Storage.Postgres.MinConns {
		errs = append(errs, fmt.Errorf("invalid postgres pool bounds: min=%d max=%d",
			c.Storage.Postgres.MinConns, c.Storage.Postgres.MaxConns))
	}
	if k := strings.TrimSpace(c.Secret.Key); k != "" {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(raw) != 32 {
			errs = append(errs, errors.New("secret.key must be 32-byte standard base64"))
		}
	}
	proxyFields := []struct {
		name string
		raw  string
	}{
		{"network.http_proxy", c.Network.HTTPProxy},
		{"network.https_proxy", c.Network.HTTPSProxy},
	}
	for _, pf := range proxyFields {
		if pf.raw != "" {
			u, err := url.Parse(pf.raw)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				errs = append(errs, fmt.Errorf("%s is not a valid http(s) proxy url: %q", pf.name, pf.raw))
			}
		}
	}
	if c.Network.CABundleFile != "" {
		pemBytes, err := os.ReadFile(c.Network.CABundleFile)
		if err != nil {
			errs = append(errs, fmt.Errorf("read network.ca_bundle_file: %w", err))
		} else if !x509.NewCertPool().AppendCertsFromPEM(pemBytes) {
			errs = append(errs, fmt.Errorf("network.ca_bundle_file %q contains no valid PEM certificate", c.Network.CABundleFile))
		}
	}
	switch strings.ToLower(c.Observability.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("invalid observability.log_level: %q", c.Observability.LogLevel))
	}
	switch strings.ToLower(c.Observability.LogFormat) {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("invalid observability.log_format: %q", c.Observability.LogFormat))
	}
	if c.Files.UploadMaxMB <= 0 || c.Files.MaxMB < c.Files.UploadMaxMB {
		errs = append(errs, errors.New("files.upload_max_mb must be > 0 and <= files.max_mb"))
	}
	if c.Task.MaxParallel <= 0 {
		errs = append(errs, errors.New("task.max_parallel must be > 0"))
	}
	if _, err := time.LoadLocation(c.SuperAgent.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("invalid superagent.timezone %q: %w", c.SuperAgent.Timezone, err))
	}
	return errors.Join(errs...)
}
