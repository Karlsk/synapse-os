package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

type mockResolver struct {
	fn func(ctx context.Context, network, host string) ([]netip.Addr, error)
}

func (m mockResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return m.fn(ctx, network, host)
}

func TestIsPublicBlocksPrivateMetadataCGNATULAAndMappedIPv6(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"127.0.0.1",
		"10.1.2.3",
		"172.16.0.1",
		"172.20.0.1",
		"172.31.255.255",
		"192.168.1.1",
		"169.254.169.254",
		"100.64.0.1",
		"100.127.255.254",
		"0.0.0.0",
		"224.0.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"::1",
		"::",
		"fd00::1",
		"fc00::1",
		"fe80::1",
		"2001:db8::1",
		"::ffff:127.0.0.1",
		"::ffff:169.254.169.254",
		"::ffff:10.0.0.1",
	}
	for _, raw := range blocked {
		addr := netip.MustParseAddr(raw)
		if IsPublic(addr) {
			t.Errorf("IsPublic(%q) = true, want false", raw)
		}
	}
	if IsPublic(netip.Addr{}) {
		t.Errorf("IsPublic(zero) = true, want false")
	}

	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"93.184.216.34",
		"2606:4700:4700::1111",
		"::ffff:8.8.8.8",
	}
	for _, raw := range allowed {
		addr := netip.MustParseAddr(raw)
		if !IsPublic(addr) {
			t.Errorf("IsPublic(%q) = false, want true", raw)
		}
	}
}

func TestCheckURLBlocksForbiddenTargetsAndAllowsWhitelisted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	deniedCases := []struct {
		name    string
		rawURL  string
		wantMsg string
	}{
		{"file scheme", "file:///etc/passwd", "只能读 http / https 链接"},
		{"ftp scheme", "ftp://example.com/a", "只能读 http / https 链接"},
		{"missing host", "https:///only-path", "链接里没有域名"},
		{"disallowed port 22", "http://1.1.1.1:22/", "不读这个端口上的地址"},
		{"loopback ipv4", "http://127.0.0.1/admin", "这是内网或本机地址，不能读取"},
		{"rfc1918 10", "http://10.1.2.3/", "这是内网或本机地址，不能读取"},
		{"rfc1918 172", "http://172.20.0.1/", "这是内网或本机地址，不能读取"},
		{"rfc1918 192", "http://192.168.1.1/", "这是内网或本机地址，不能读取"},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", "这是内网或本机地址，不能读取"},
		{"ipv6 loopback", "http://[::1]/", "这是内网或本机地址，不能读取"},
		{"ipv6 ula", "http://[fd00::1]/", "这是内网或本机地址，不能读取"},
		{"mapped loopback", "http://[::ffff:127.0.0.1]/", "这是内网或本机地址，不能读取"},
		{"mapped metadata", "http://[::ffff:169.254.169.254]/", "这是内网或本机地址，不能读取"},
		{"cgnat", "http://100.64.0.1/", "这是内网或本机地址，不能读取"},
		{"zero addr", "http://0.0.0.0/", "这是内网或本机地址，不能读取"},
		{"localhost", "http://localhost:8080/api", "这是内网或本机地址，不能读取"},
		{"dot local", "http://printer.local/status", "这是内网或本机地址，不能读取"},
		{"dot internal", "http://svc.internal/metrics", "这是内网或本机地址，不能读取"},
	}

	for _, tc := range deniedCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := PublicOnly.CheckURL(ctx, tc.rawURL)
			if err == nil {
				t.Fatalf("CheckURL(%q) = nil, want EgressDenied", tc.rawURL)
			}
			if !errors.Is(err, apperror.ErrEgressDenied) {
				t.Fatalf("CheckURL(%q) error = %v, want ErrEgressDenied", tc.rawURL, err)
			}
			if got := apperror.HTTPStatusOf(err); got != http.StatusForbidden {
				t.Fatalf("HTTPStatusOf = %d, want 403", got)
			}
			if err.Error() != tc.wantMsg {
				t.Errorf("error msg = %q, want %q", err.Error(), tc.wantMsg)
			}
		})
	}

	t.Run("admin allowlist permits CIDR and suffix", func(t *testing.T) {
		t.Parallel()
		g, err := New(Options{
			Allowed: []string{"10.8.0.0/16", ".corp.local", "wiki.corp.example"},
			Resolver: mockResolver{fn: func(_ context.Context, _, host string) ([]netip.Addr, error) {
				switch host {
				case "internal-db.example":
					return []netip.Addr{netip.MustParseAddr("10.8.4.5")}, nil
				case "other-private.example":
					return []netip.Addr{netip.MustParseAddr("10.9.4.5")}, nil
				default:
					return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
				}
			}},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		for _, okURL := range []string{
			"http://10.8.1.2:8080/health",
			"https://git.corp.local/repo",
			"https://wiki.corp.example/docs",
			"https://internal-db.example/api",
		} {
			if _, err := g.CheckURL(ctx, okURL); err != nil {
				t.Errorf("expected %q allowed, got error: %v", okURL, err)
			}
		}
		if _, err := g.CheckURL(ctx, "https://other-private.example/api"); !errors.Is(err, apperror.ErrEgressDenied) {
			t.Errorf("expected non-whitelisted 10.9.4.5 denied, got %v", err)
		}
	})

	t.Run("invalid CIDR in Allowed returns error", func(t *testing.T) {
		t.Parallel()
		if _, err := New(Options{Allowed: []string{"10.8.0.0/99"}}); err == nil {
			t.Fatalf("expected error for malformed CIDR")
		}
	})
}

func TestToURIEncodesChineseAndSpaces(t *testing.T) {
	t.Parallel()

	u, err := ToURI("  https://example.com/文档/设计 草案.html?q=你好 世界  ")
	if err != nil {
		t.Fatalf("ToURI() error = %v", err)
	}
	if u.Host != "example.com" {
		t.Fatalf("Host = %q, want example.com", u.Host)
	}
	if strings.Contains(u.String(), " ") {
		t.Fatalf("ToURI did not encode spaces: %q", u.String())
	}
	if !strings.Contains(u.EscapedPath(), "%E6%96%87%E6%A1%A3") {
		t.Fatalf("ToURI did not percent-encode Chinese path: %q", u.EscapedPath())
	}
}

func TestAntiDNSRebindingMixedRecordsAndDialContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("mixed public and metadata DNS answers rejected as a group", func(t *testing.T) {
		t.Parallel()
		g := MustNew(Options{
			Resolver: mockResolver{fn: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("93.184.216.34"),
					netip.MustParseAddr("169.254.169.254"),
				}, nil
			}},
		})
		if _, err := g.CheckURL(ctx, "https://dual.example.com/"); !errors.Is(err, apperror.ErrEgressDenied) {
			t.Fatalf("expected mixed record denied, got %v", err)
		}
		if _, err := g.DialContext(ctx, "tcp", "dual.example.com:443"); !errors.Is(err, apperror.ErrEgressDenied) {
			t.Fatalf("expected DialContext mixed record denied, got %v", err)
		}
	})

	t.Run("DNS lookup failure returns Chinese message", func(t *testing.T) {
		t.Parallel()
		g := MustNew(Options{
			Resolver: mockResolver{fn: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
				return nil, errors.New("no such host")
			}},
		})
		_, err := g.CheckURL(ctx, "https://missing.example.invalid/")
		if !errors.Is(err, apperror.ErrEgressDenied) || err.Error() != "找不到这个域名" {
			t.Fatalf("got err=%v, want EgressDenied('找不到这个域名')", err)
		}
	})

	t.Run("DialContext resolves once and rejects rebind to loopback", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		g := MustNew(Options{
			DialTimeout: 50 * time.Millisecond,
			Resolver: mockResolver{fn: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
				n := calls.Add(1)
				if n == 1 {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}},
		})
		_, err := g.DialContext(ctx, "tcp", "rebind.example.com:80")
		if !errors.Is(err, apperror.ErrEgressDenied) {
			t.Fatalf("expected loopback rebind denied, got %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("expected single DNS lookup inside DialContext, got %d", calls.Load())
		}
	})
}

func TestCheckRedirectLimitAndCrossOriginHeaderStripping(t *testing.T) {
	t.Parallel()

	g := MustNew(Options{
		Resolver: mockResolver{fn: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}},
	})

	// 6 hops already in via => 7th hop denied
	via6 := make([]*http.Request, 6)
	for i := range via6 {
		via6[i] = &http.Request{URL: &url.URL{Scheme: "https", Host: "a.example.com"}}
	}
	req7, _ := http.NewRequest(http.MethodGet, "https://a.example.com/hop7", nil)
	err := g.CheckRedirect(req7, via6)
	if !errors.Is(err, apperror.ErrEgressDenied) || err.Error() != "重定向次数过多" {
		t.Fatalf("7th redirect got %v, want EgressDenied('重定向次数过多')", err)
	}

	// Cross-origin redirect strips Authorization, Cookie, Proxy-Authorization
	prev, _ := http.NewRequest(http.MethodGet, "https://origin-a.example.com/start", nil)
	next, _ := http.NewRequest(http.MethodGet, "https://origin-b.example.com/next", nil)
	next.Header.Set("Authorization", "Bearer secret-token")
	next.Header.Set("Cookie", "sid=secret-cookie")
	next.Header.Set("Proxy-Authorization", "Basic secret-proxy")
	next.Header.Set("Accept", "text/html")

	if err := g.CheckRedirect(next, []*http.Request{prev}); err != nil {
		t.Fatalf("valid cross-origin redirect error = %v", err)
	}
	if next.Header.Get("Authorization") != "" || next.Header.Get("Cookie") != "" || next.Header.Get("Proxy-Authorization") != "" {
		t.Fatalf("sensitive headers were not stripped on cross-origin redirect: %+v", next.Header)
	}
	if next.Header.Get("Accept") != "text/html" {
		t.Fatalf("non-sensitive header Accept was unexpectedly stripped")
	}

	// Redirect to private IP is blocked
	badNext, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil)
	if err := g.CheckRedirect(badNext, []*http.Request{prev}); !errors.Is(err, apperror.ErrEgressDenied) {
		t.Fatalf("expected redirect to metadata IP blocked, got %v", err)
	}

	// Same-origin redirect (explicit :443 vs implicit https) preserves Authorization
	sameNext, _ := http.NewRequest(http.MethodGet, "https://origin-a.example.com:443/other", nil)
	sameNext.Header.Set("Authorization", "Bearer keep-me")
	if err := g.CheckRedirect(sameNext, []*http.Request{prev}); err != nil {
		t.Fatalf("same-origin redirect error = %v", err)
	}
	if sameNext.Header.Get("Authorization") != "Bearer keep-me" {
		t.Fatalf("same-origin redirect stripped Authorization")
	}
}

func TestDialContextControlAndAllowlistConnect(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Empty URI, invalid addr, disallowed port, and forbidden localhost in DialContext
	if _, err := ToURI("   "); !errors.Is(err, apperror.ErrEgressDenied) {
		t.Fatalf("ToURI(blank) = %v, want EgressDenied", err)
	}
	if _, err := PublicOnly.DialContext(ctx, "tcp", "no-port"); !errors.Is(err, apperror.ErrEgressDenied) {
		t.Fatalf("DialContext(no-port) = %v, want EgressDenied", err)
	}
	if _, err := PublicOnly.DialContext(ctx, "tcp", "1.1.1.1:22"); !errors.Is(err, apperror.ErrEgressDenied) {
		t.Fatalf("DialContext(:22) = %v, want EgressDenied", err)
	}
	if _, err := PublicOnly.DialContext(ctx, "tcp", "localhost:80"); !errors.Is(err, apperror.ErrEgressDenied) {
		t.Fatalf("DialContext(localhost) = %v, want EgressDenied", err)
	}

	// Start a local TCP listener and verify both CIDR-allowlisted DialContext (exercises Control)
	// and host-allowlisted bypass dial successfully.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	portNum, _ := strconv.Atoi(portStr)

	gCIDR := MustNew(Options{
		Allowed:      []string{"127.0.0.0/8"},
		AllowedPorts: []int{portNum},
		Resolver: mockResolver{fn: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}},
	})
	conn, err := gCIDR.DialContext(ctx, "tcp", net.JoinHostPort("allowed-cidr.example", portStr))
	if err != nil {
		t.Fatalf("CIDR-allowlisted DialContext failed: %v", err)
	}
	_ = conn.Close()

	gHost := MustNew(Options{
		Allowed:      []string{"127.0.0.1"},
		AllowedPorts: []int{portNum},
	})
	conn2, err := gHost.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("host-allowlisted DialContext failed: %v", err)
	}
	_ = conn2.Close()

	// Multi-IP budget splitting: unreachable first IP must not exhaust the full budget before falling back to 127.0.0.1.
	gMulti := MustNew(Options{
		Allowed:      []string{"192.0.2.0/24", "127.0.0.0/8"},
		AllowedPorts: []int{portNum},
		DialTimeout:  240 * time.Millisecond,
		Resolver: mockResolver{fn: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("192.0.2.1"), // TEST-NET-1 blackhole/unreachable
				netip.MustParseAddr("127.0.0.1"), // reachable fallback
			}, nil
		}},
	})
	start := time.Now()
	conn3, err := gMulti.DialContext(ctx, "tcp", net.JoinHostPort("multi-ip.example", portStr))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("multi-IP fallback DialContext failed after %v: %v", elapsed, err)
	}
	_ = conn3.Close()
	if elapsed > 350*time.Millisecond {
		t.Fatalf("multi-IP DialContext took %v, expected <= ~240ms budget", elapsed)
	}

	// Pre-canceled context returns context.Canceled immediately.
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := gCIDR.DialContext(canceledCtx, "tcp", net.JoinHostPort("allowed-cidr.example", portStr)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled DialContext error = %v, want context.Canceled", err)
	}
}
