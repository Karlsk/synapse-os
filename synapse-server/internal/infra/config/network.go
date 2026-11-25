package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// NetworkRuntime bundles corporate proxy resolution, system + custom PEM CA trust
// pool, TLS 1.2+ config, and standardized child-process environment variables.
type NetworkRuntime struct {
	CertPool  *x509.CertPool
	TLSConfig *tls.Config
	ProxyFunc func(*http.Request) (*url.URL, error)
	ChildEnv  map[string]string
	NoProxy   string
}

// BuildNetworkRuntime constructs a unified NetworkRuntime for outbound HTTP clients
// (SA-03) and CLI / Node / npm child processes (SA-11).
func BuildNetworkRuntime(netCfg NetworkConfig, rtCfg RuntimeConfig) (*NetworkRuntime, error) {
	noProxy := ensureLocalNoProxy(netCfg.NoProxy)
	proxyCfg := &httpproxy.Config{
		HTTPProxy:  netCfg.HTTPProxy,
		HTTPSProxy: netCfg.HTTPSProxy,
		NoProxy:    noProxy,
		CGI:        false,
	}
	proxyURLFn := proxyCfg.ProxyFunc()
	proxyFunc := func(req *http.Request) (*url.URL, error) {
		return proxyURLFn(req.URL)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	childEnv := map[string]string{
		"NO_PROXY": noProxy,
		"no_proxy": noProxy,
	}
	if netCfg.HTTPProxy != "" {
		childEnv["HTTP_PROXY"] = netCfg.HTTPProxy
		childEnv["http_proxy"] = netCfg.HTTPProxy
	}
	if netCfg.HTTPSProxy != "" {
		childEnv["HTTPS_PROXY"] = netCfg.HTTPSProxy
		childEnv["https_proxy"] = netCfg.HTTPSProxy
	}
	if rtCfg.NPMRegistry != "" {
		childEnv["NPM_CONFIG_REGISTRY"] = rtCfg.NPMRegistry
	}
	if netCfg.CABundleFile != "" {
		pemBytes, err := os.ReadFile(netCfg.CABundleFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_bundle_file %q: %w", netCfg.CABundleFile, err)
		}
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("no valid PEM cert in %q", netCfg.CABundleFile)
		}
		childEnv["NODE_EXTRA_CA_CERTS"] = netCfg.CABundleFile
		childEnv["SSL_CERT_FILE"] = netCfg.CABundleFile
	}

	return &NetworkRuntime{
		CertPool:  pool,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		ProxyFunc: proxyFunc,
		ChildEnv:  childEnv,
		NoProxy:   noProxy,
	}, nil
}

// ensureLocalNoProxy guarantees loopback hosts always bypass corporate proxies so
// internal POST /mcp self-checks are never hijacked.
func ensureLocalNoProxy(raw string) string {
	seen := make(map[string]struct{})
	var items []string
	add := func(entry string) {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			return
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		items = append(items, trimmed)
	}

	for _, part := range strings.Split(raw, ",") {
		add(part)
	}
	for _, required := range []string{"127.0.0.1", "localhost", "::1"} {
		add(required)
	}
	return strings.Join(items, ",")
}
