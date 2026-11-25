package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/infra/config"
)

func TestDualProfileIsolationTrustedCAProxyVsUntrustedBlock(t *testing.T) {
	t.Parallel()

	var serverHits atomic.Int32
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("trusted-ok"))
	}))
	defer tlsSrv.Close()

	// Build a config.NetworkRuntime trusting the httptest server's self-signed CA.
	pool := x509.NewCertPool()
	pool.AddCert(tlsSrv.Certificate())
	netRT := &config.NetworkRuntime{
		CertPool:  pool,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
	}

	trusted, err := NewTrusted(Options{
		Service: "feishu",
		Network: netRT,
	})
	if err != nil {
		t.Fatalf("NewTrusted() error = %v", err)
	}
	if trusted.Profile() != ProfileTrusted {
		t.Fatalf("Profile() = %q, want %q", trusted.Profile(), ProfileTrusted)
	}

	req, err := http.NewRequest(http.MethodGet, tlsSrv.URL+"/api", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := trusted.Do(req)
	if err != nil {
		t.Fatalf("TrustedClient.Do(self-signed TLS) error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "trusted-ok" {
		t.Fatalf("unexpected trusted response: status=%d body=%q", resp.StatusCode, string(body))
	}

	// Verify TrustedClient honors ProxyFunc when configured.
	var proxyHit atomic.Bool
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHit.Store(true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxySrv.Close()
	proxyURL, _ := url.Parse(proxySrv.URL)

	proxiedClient, err := NewTrusted(Options{
		Service: "node-mirror",
		Network: &config.NetworkRuntime{
			ProxyFunc: http.ProxyURL(proxyURL),
		},
	})
	if err != nil {
		t.Fatalf("NewTrusted(proxy) error = %v", err)
	}
	pReq, _ := http.NewRequest(http.MethodGet, "http://example.invalid/node.tar.gz", nil)
	pResp, err := proxiedClient.Do(pReq)
	if err != nil {
		t.Fatalf("proxied TrustedClient.Do error = %v", err)
	}
	_ = pResp.Body.Close()
	if !proxyHit.Load() {
		t.Fatalf("expected TrustedClient to route through NetworkRuntime.ProxyFunc")
	}

	// UntrustedEgressClient must immediately block the exact same 127.0.0.1 server without dialing.
	hitsBefore := serverHits.Load()
	untrusted, err := NewUntrustedEgress(Options{
		Service: "link_reader",
	})
	if err != nil {
		t.Fatalf("NewUntrustedEgress() error = %v", err)
	}
	if untrusted.Profile() != ProfileUntrusted {
		t.Fatalf("Profile() = %q, want %q", untrusted.Profile(), ProfileUntrusted)
	}

	uReq, _ := http.NewRequest(http.MethodGet, tlsSrv.URL+"/secret", nil)
	_, err = untrusted.Do(uReq)
	if err == nil {
		t.Fatalf("expected UntrustedEgressClient to block 127.0.0.1")
	}
	kind, ok := KindOf(err)
	if !ok || kind != KindEgressDenied {
		t.Fatalf("KindOf(err) = (%q, %v), want (%q, true); err=%v", kind, ok, KindEgressDenied, err)
	}
	if !errors.Is(err, apperror.ErrEgressDenied) {
		t.Fatalf("expected errors.Is(err, apperror.ErrEgressDenied) == true, got %v", err)
	}
	if serverHits.Load() != hitsBefore {
		t.Fatalf("UntrustedEgressClient must never establish TCP connection to 127.0.0.1")
	}
}
