package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func generateTestCAPEM(t *testing.T) ([]byte, *x509.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Synapse Corp Test Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create self-signed cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return pemBytes, cert
}

func TestBuildNetworkRuntimeLoopbackBypassAndCustomCA(t *testing.T) {
	t.Parallel()

	pemBytes, rootCert := generateTestCAPEM(t)
	caFile := filepath.Join(t.TempDir(), "corp-ca.pem")
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}

	netCfg := NetworkConfig{
		HTTPProxy:    "http://proxy.corp.local:8080",
		HTTPSProxy:   "http://proxy.corp.local:8443",
		NoProxy:      "corp.local",
		CABundleFile: caFile,
	}
	rtCfg := RuntimeConfig{
		NPMRegistry: "https://registry.npmmirror.com",
	}

	nrt, err := BuildNetworkRuntime(netCfg, rtCfg)
	if err != nil {
		t.Fatalf("BuildNetworkRuntime() error = %v", err)
	}

	// Local loopback URLs must always bypass corporate proxy even when no_proxy only lists corp.local
	for _, rawURL := range []string{
		"http://127.0.0.1:18080/mcp",
		"http://localhost:18080/mcp",
		"http://[::1]:18080/mcp",
		"http://service.corp.local/api",
	} {
		req, err := http.NewRequest(http.MethodPost, rawURL, nil)
		if err != nil {
			t.Fatalf("new request %s: %v", rawURL, err)
		}
		gotProxy, err := nrt.ProxyFunc(req)
		if err != nil {
			t.Fatalf("ProxyFunc(%s) error = %v", rawURL, err)
		}
		if gotProxy != nil {
			t.Fatalf("ProxyFunc(%s) = %v, want nil (direct)", rawURL, gotProxy)
		}
	}

	// External URLs must route through configured proxy
	extReq, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	gotExtProxy, err := nrt.ProxyFunc(extReq)
	if err != nil {
		t.Fatalf("ProxyFunc(external) error = %v", err)
	}
	if gotExtProxy == nil || gotExtProxy.String() != "http://proxy.corp.local:8443" {
		t.Fatalf("ProxyFunc(external) = %v, want http://proxy.corp.local:8443", gotExtProxy)
	}

	// Custom PEM CA must be trusted by CertPool & TLSConfig enforces TLS 1.2+
	if nrt.TLSConfig == nil || nrt.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLSConfig must enforce TLS 1.2+, got %+v", nrt.TLSConfig)
	}
	if _, err := rootCert.Verify(x509.VerifyOptions{Roots: nrt.CertPool}); err != nil {
		t.Fatalf("custom CA cert not found in CertPool: %v", err)
	}

	// Verify standardized ChildEnv entries
	wantEnv := map[string]string{
		"HTTP_PROXY":          "http://proxy.corp.local:8080",
		"http_proxy":          "http://proxy.corp.local:8080",
		"HTTPS_PROXY":         "http://proxy.corp.local:8443",
		"https_proxy":         "http://proxy.corp.local:8443",
		"NODE_EXTRA_CA_CERTS": caFile,
		"SSL_CERT_FILE":       caFile,
		"NPM_CONFIG_REGISTRY": "https://registry.npmmirror.com",
	}
	for k, want := range wantEnv {
		if got := nrt.ChildEnv[k]; got != want {
			t.Errorf("ChildEnv[%q] = %q, want %q", k, got, want)
		}
	}
	for _, mustContain := range []string{"127.0.0.1", "localhost", "::1", "corp.local"} {
		if !strings.Contains(nrt.NoProxy, mustContain) {
			t.Errorf("NoProxy %q missing %q", nrt.NoProxy, mustContain)
		}
	}
	if nrt.ChildEnv["NO_PROXY"] != nrt.NoProxy || nrt.ChildEnv["no_proxy"] != nrt.NoProxy {
		t.Errorf("ChildEnv NO_PROXY mismatch: %q / %q", nrt.ChildEnv["NO_PROXY"], nrt.ChildEnv["no_proxy"])
	}
}

func TestBuildNetworkRuntimeFailsOnInvalidCA(t *testing.T) {
	t.Parallel()

	t.Run("missing CA file", func(t *testing.T) {
		t.Parallel()
		_, err := BuildNetworkRuntime(NetworkConfig{
			CABundleFile: filepath.Join(t.TempDir(), "missing.pem"),
		}, RuntimeConfig{})
		if err == nil {
			t.Fatalf("expected error for missing CA bundle file")
		}
	})

	t.Run("corrupt PEM file", func(t *testing.T) {
		t.Parallel()
		badFile := filepath.Join(t.TempDir(), "corrupt.pem")
		if err := os.WriteFile(badFile, []byte("invalid pem content"), 0o600); err != nil {
			t.Fatalf("write corrupt pem: %v", err)
		}
		_, err := BuildNetworkRuntime(NetworkConfig{
			CABundleFile: badFile,
		}, RuntimeConfig{})
		if err == nil {
			t.Fatalf("expected error for corrupt PEM file")
		}
	})
}
