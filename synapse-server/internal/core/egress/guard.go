// Package egress provides transport-level anti-SSRF and anti-DNS-rebinding
// protection for outbound network connections initiated on untrusted URLs.
package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

// Resolver resolves hostnames into IP addresses; defaults to net.DefaultResolver.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options configures an egress Guard.
type Options struct {
	Allowed      []string      // Exact host, ".suffix", or IPv4/IPv6 CIDR allowlist entries.
	AllowedPorts []int         // Defaults to [80, 443, 8080, 8443].
	Resolver     Resolver      // Defaults to net.DefaultResolver.
	DialTimeout  time.Duration // Defaults to 10s.
	MaxRedirects int           // Defaults to 6.
}

// Guard enforces public-internet-only outbound connections with DialContext-level
// DNS pinning and socket Control defense-in-depth. Immutable and safe for concurrent use.
type Guard struct {
	allowedHosts    map[string]struct{}
	allowedSuffixes []string
	allowedPrefixes []netip.Prefix
	allowedPorts    map[int]struct{}
	resolver        Resolver
	dialTimeout     time.Duration
	maxRedirects    int
}

// PublicOnly is the default strict egress guard allowing only public HTTP(S) destinations.
var PublicOnly = MustNew(Options{})

// New builds a validated Guard from opts.
func New(opts Options) (*Guard, error) {
	g := &Guard{
		allowedHosts: make(map[string]struct{}),
		allowedPorts: make(map[int]struct{}),
		resolver:     opts.Resolver,
		dialTimeout:  opts.DialTimeout,
		maxRedirects: opts.MaxRedirects,
	}
	if g.resolver == nil {
		g.resolver = net.DefaultResolver
	}
	if g.dialTimeout <= 0 {
		g.dialTimeout = 10 * time.Second
	}
	if g.maxRedirects <= 0 {
		g.maxRedirects = 6
	}

	ports := opts.AllowedPorts
	if len(ports) == 0 {
		ports = []int{80, 443, 8080, 8443}
	}
	for _, p := range ports {
		if p > 0 && p <= 65535 {
			g.allowedPorts[p] = struct{}{}
		}
	}

	for _, entry := range opts.Allowed {
		s := strings.ToLower(strings.TrimSpace(entry))
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			pfx, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("invalid egress allowlist CIDR %q: %w", entry, err)
			}
			g.allowedPrefixes = append(g.allowedPrefixes, pfx)
			continue
		}
		if strings.HasPrefix(s, ".") {
			g.allowedSuffixes = append(g.allowedSuffixes, strings.TrimSuffix(s, "."))
			continue
		}
		g.allowedHosts[strings.TrimSuffix(s, ".")] = struct{}{}
	}
	return g, nil
}

// MustNew calls New(opts) and panics on invalid static configuration.
func MustNew(opts Options) *Guard {
	g, err := New(opts)
	if err != nil {
		panic(err)
	}
	return g
}

// ToURI trims raw and percent-encodes spaces, control characters, and non-ASCII
// bytes (such as Chinese path segments) before parsing, matching Java EgressGuard.toUri.
func ToURI(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, apperror.EgressDenied("不是有效的网址")
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		v := s[i]
		if v > 0x7e || v <= 0x20 || v == '"' || v == '<' || v == '>' || v == '\\' ||
			v == '^' || v == '`' || v == '{' || v == '|' || v == '}' {
			fmt.Fprintf(&b, "%%%02X", v)
		} else {
			b.WriteByte(v)
		}
	}
	u, err := url.Parse(b.String())
	if err != nil {
		return nil, apperror.EgressDenied("不是有效的网址")
	}
	return u, nil
}

// IsPublic reports whether addr is a globally routable public IP address.
// Unmap() is always called first to defeat ::ffff:127.0.0.1 and ::ffff:169.254.169.254 bypasses.
func IsPublic(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsPrivate() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	if addr.Is6() {
		b := addr.As16()
		if (b[0] & 0xfe) == 0xfc { // fc00::/7 ULA
			return false
		}
		if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x0d && b[3] == 0xb8 { // 2001:db8::/32 documentation
			return false
		}
		return true
	}
	b := addr.As4()
	x, y, z := b[0], b[1], b[2]
	if x == 0 || x == 10 || x == 127 || x >= 224 ||
		(x == 100 && y >= 64 && y <= 127) || // RFC6598 CGNAT 100.64.0.0/10
		(x == 169 && y == 254) || // 169.254.0.0/16 Link-Local / Cloud metadata
		(x == 172 && y >= 16 && y <= 31) || // RFC1918
		(x == 192 && y == 168) || // RFC1918
		(x == 192 && y == 0 && (z == 0 || z == 2)) || // 192.0.0.0/24, 192.0.2.0/24
		(x == 198 && (y == 18 || y == 19)) || // RFC2544 198.18.0.0/15
		(x == 198 && y == 51 && z == 100) || // 198.51.100.0/24 TEST-NET-2
		(x == 203 && y == 0 && z == 113) { // 203.0.113.0/24 TEST-NET-3
		return false
	}
	return true
}

// CheckURL validates scheme, hostname, port, local suffixes, and resolved IPs.
func (g *Guard) CheckURL(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := ToURI(rawURL)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, apperror.EgressDenied("只能读 http / https 链接")
	}
	host := u.Hostname()
	if strings.TrimSpace(host) == "" {
		return nil, apperror.EgressDenied("链接里没有域名")
	}

	portNum := 80
	if scheme == "https" {
		portNum = 443
	}
	if p := u.Port(); p != "" {
		parsed, err := strconv.Atoi(p)
		if err != nil {
			return nil, apperror.EgressDenied("不读这个端口上的地址")
		}
		portNum = parsed
	}
	if !g.isPortAllowed(portNum) {
		return nil, apperror.EgressDenied("不读这个端口上的地址")
	}

	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if g.allowedHost(h) {
		return u, nil
	}
	if isForbiddenLocalHost(h) {
		return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
	}
	if _, err := g.resolveAndValidate(ctx, h); err != nil {
		return nil, err
	}
	return u, nil
}

// DialContext resolves DNS once, validates every returned IP, dials validated IP
// literals directly (eliminating DNS rebinding), and re-checks the target IP inside
// net.Dialer.Control right before socket connect().
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, apperror.EgressDenied("不是有效的网址")
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || !g.isPortAllowed(portNum) {
		return nil, apperror.EgressDenied("不读这个端口上的地址")
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	allowBypass := g.allowedHost(h)

	dialer := &net.Dialer{
		Timeout:   g.dialTimeout,
		KeepAlive: 30 * time.Second,
		Control: func(_, rawAddr string, _ syscall.RawConn) error {
			if allowBypass {
				return nil
			}
			ipHost, _, err := net.SplitHostPort(rawAddr)
			if err != nil {
				return apperror.EgressDenied("不是有效的网址")
			}
			ip, err := netip.ParseAddr(ipHost)
			if err != nil || (!IsPublic(ip) && !g.allowedPrefix(ip)) {
				return apperror.EgressDenied("这是内网或本机地址，不能读取")
			}
			return nil
		},
	}

	if allowBypass {
		return dialer.DialContext(ctx, network, addr)
	}
	if isForbiddenLocalHost(h) {
		return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
	}
	ips, err := g.resolveAndValidate(ctx, h)
	if err != nil {
		return nil, err
	}

	totalDeadline := time.Now().Add(g.dialTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(totalDeadline) {
		totalDeadline = ctxDeadline
	}

	var lastErr error
	for i, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := time.Until(totalDeadline)
		if remaining <= 0 {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, context.DeadlineExceeded
		}
		// Split remaining dial budget across remaining candidate IPs so an unreachable
		// blackhole address (e.g., IPv6 in an IPv4-only enterprise network) cannot
		// multiply total dial latency or starve fallback IPs.
		left := len(ips) - i
		perAttempt := remaining / time.Duration(left)
		if perAttempt <= 0 {
			perAttempt = remaining
		}
		attemptCtx, cancel := context.WithTimeout(ctx, perAttempt)
		conn, err := dialer.DialContext(attemptCtx, network, net.JoinHostPort(ip.String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// CheckRedirect enforces MaxRedirects, re-validates every hop with CheckURL, and
// strips Authorization/Cookie/Proxy-Authorization headers across origin boundaries.
func (g *Guard) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= g.maxRedirects {
		return apperror.EgressDenied("重定向次数过多")
	}
	if _, err := g.CheckURL(req.Context(), req.URL.String()); err != nil {
		return err
	}
	if len(via) > 0 {
		if prev := via[len(via)-1]; prev != nil && !sameOrigin(prev.URL, req.URL) {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Proxy-Authorization")
		}
	}
	return nil
}

func (g *Guard) isPortAllowed(port int) bool {
	_, ok := g.allowedPorts[port]
	return ok
}

func (g *Guard) allowedHost(host string) bool {
	if _, ok := g.allowedHosts[host]; ok {
		return true
	}
	for _, suffix := range g.allowedSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func (g *Guard) allowedPrefix(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, pfx := range g.allowedPrefixes {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

func (g *Guard) resolveAndValidate(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !IsPublic(ip) && !g.allowedPrefix(ip) {
			return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
		}
		return []netip.Addr{ip}, nil
	}
	ips, err := g.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, apperror.EgressDenied("找不到这个域名")
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		if !IsPublic(ip) && !g.allowedPrefix(ip) {
			return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
		}
		out = append(out, ip)
	}
	return out, nil
}

func isForbiddenLocalHost(host string) bool {
	return host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal")
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}
