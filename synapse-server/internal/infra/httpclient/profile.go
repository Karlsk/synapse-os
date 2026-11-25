package httpclient

import (
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Karlsk/synapse-os/internal/core/egress"
	"github.com/Karlsk/synapse-os/internal/infra/config"
)

// Profile identifies the trust boundary of an outbound HTTP client.
type Profile string

const (
	ProfileTrusted   Profile = "trusted"
	ProfileUntrusted Profile = "untrusted"
)

// TimeoutPolicy configures layered connection, handshake, header, total, and streaming idle timeouts.
type TimeoutPolicy struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	TotalTimeout          time.Duration
	IdleReadTimeout       time.Duration
}

// RetryPolicy configures exponential backoff + full jitter retries for idempotent requests.
type RetryPolicy struct {
	MaxAttempts   int
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	MaxRetryAfter time.Duration
}

// BreakerPolicy configures per-(service, host) gobreaker/v2 circuit breakers.
type BreakerPolicy struct {
	Enabled         bool
	Interval        time.Duration
	Timeout         time.Duration
	MaxRequests     uint32
	MinRequests     uint32
	FailureRatio    float64
	ConsecutiveFail uint32
	MaxHostEntries  int
}

// Options configures NewTrusted or NewUntrustedEgress.
type Options struct {
	Service          string
	Streaming        bool
	Timeouts         TimeoutPolicy
	Retry            RetryPolicy
	Breaker          BreakerPolicy
	MaxResponseBytes int64
	EgressGuard      *egress.Guard
	Network          *config.NetworkRuntime
	Logger           *slog.Logger
	Registerer       prometheus.Registerer
}

const defaultUntrustedMaxResponseBytes int64 = 20 << 20 // 20 MiB

func applyDefaults(opts *Options, profile Profile) {
	if strings.TrimSpace(opts.Service) == "" {
		opts.Service = string(profile)
	}
	if opts.Timeouts.DialTimeout <= 0 {
		if profile == ProfileUntrusted {
			opts.Timeouts.DialTimeout = 10 * time.Second
		} else {
			opts.Timeouts.DialTimeout = 5 * time.Second
		}
	}
	if opts.Timeouts.TLSHandshakeTimeout <= 0 {
		opts.Timeouts.TLSHandshakeTimeout = 10 * time.Second
	}
	if opts.Timeouts.ResponseHeaderTimeout <= 0 {
		opts.Timeouts.ResponseHeaderTimeout = 30 * time.Second
	}
	if opts.Streaming {
		// SSE / large-file streaming mode MUST have no global client timeout and no automatic retries.
		opts.Timeouts.TotalTimeout = 0
		opts.Retry.MaxAttempts = 1
	} else {
		if opts.Timeouts.TotalTimeout <= 0 {
			opts.Timeouts.TotalTimeout = 60 * time.Second
		}
		if opts.Retry.MaxAttempts <= 0 {
			opts.Retry.MaxAttempts = 3
		}
	}
	if opts.Retry.BaseBackoff <= 0 {
		opts.Retry.BaseBackoff = 200 * time.Millisecond
	}
	if opts.Retry.MaxBackoff <= 0 {
		opts.Retry.MaxBackoff = 5 * time.Second
	}
	if opts.Retry.MaxRetryAfter <= 0 {
		opts.Retry.MaxRetryAfter = 30 * time.Second
	}

	// Default Breaker.Enabled to true when caller leaves the entire BreakerPolicy zero-valued.
	if opts.Breaker == (BreakerPolicy{}) {
		opts.Breaker.Enabled = true
	}
	if opts.Breaker.Interval <= 0 {
		opts.Breaker.Interval = 60 * time.Second
	}
	if opts.Breaker.Timeout <= 0 {
		opts.Breaker.Timeout = 30 * time.Second
	}
	if opts.Breaker.MaxRequests == 0 {
		opts.Breaker.MaxRequests = 2
	}
	if opts.Breaker.MinRequests == 0 {
		opts.Breaker.MinRequests = 5
	}
	if opts.Breaker.FailureRatio <= 0 {
		opts.Breaker.FailureRatio = 0.6
	}
	if opts.Breaker.ConsecutiveFail == 0 {
		opts.Breaker.ConsecutiveFail = 5
	}
	if opts.Breaker.MaxHostEntries <= 0 {
		opts.Breaker.MaxHostEntries = 256
	}

	if profile == ProfileUntrusted && opts.MaxResponseBytes <= 0 {
		opts.MaxResponseBytes = defaultUntrustedMaxResponseBytes
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
}

// NewTrusted constructs an outbound HTTP client for trusted enterprise/platform
// targets (Feishu, DingTalk, LLMs, models.dev, Node mirrors) with corporate proxy
// and private CA support from config.NetworkRuntime.
func NewTrusted(opts Options) (*Client, error) {
	applyDefaults(&opts, ProfileTrusted)
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: opts.Timeouts.DialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   opts.Timeouts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.Timeouts.ResponseHeaderTimeout,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if opts.Network != nil {
		if opts.Network.TLSConfig != nil {
			tr.TLSClientConfig = opts.Network.TLSConfig.Clone()
		}
		if opts.Network.ProxyFunc != nil {
			tr.Proxy = opts.Network.ProxyFunc
		}
	}
	return buildClient(ProfileTrusted, opts, tr, nil), nil
}

// NewUntrustedEgress constructs a hardened outbound HTTP client for untrusted
// user-supplied URLs (LinkReader, webhooks, external custom MCP). It enforces
// egress.Guard.DialContext, disables proxy bypass (Proxy=nil), enforces CheckRedirect,
// and caps response bodies via io.LimitReader (default 20 MiB).
func NewUntrustedEgress(opts Options) (*Client, error) {
	applyDefaults(&opts, ProfileUntrusted)
	guard := opts.EgressGuard
	if guard == nil {
		guard = egress.PublicOnly
	}
	tr := &http.Transport{
		DialContext:           guard.DialContext,
		Proxy:                 nil, // Never allow proxy routing to bypass local EgressGuard validation.
		TLSHandshakeTimeout:   opts.Timeouts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.Timeouts.ResponseHeaderTimeout,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return buildClient(ProfileUntrusted, opts, tr, guard.CheckRedirect), nil
}

func buildClient(
	profile Profile,
	opts Options,
	tr *http.Transport,
	checkRedirect func(*http.Request, []*http.Request) error,
) *Client {
	var rt http.RoundTripper = tr
	// Inner -> outer assembly: Transport -> BodyControl -> Breaker -> Retry -> Observability
	if opts.MaxResponseBytes > 0 || (opts.Streaming && opts.Timeouts.IdleReadTimeout > 0) {
		rt = &bodyControlRT{
			next:            rt,
			maxBytes:        opts.MaxResponseBytes,
			idleReadTimeout: opts.Timeouts.IdleReadTimeout,
			streaming:       opts.Streaming,
			service:         opts.Service,
		}
	}
	if opts.Breaker.Enabled {
		rt = newBreakerRT(rt, opts.Service, opts.Breaker, opts.Registerer)
	}
	if !opts.Streaming && opts.Retry.MaxAttempts > 1 {
		rt = newRetryRT(rt, opts.Service, opts.Retry, opts.Registerer)
	}
	rt = newObservabilityRT(rt, profile, opts.Service, opts.Logger, opts.Registerer)

	totalTimeout := opts.Timeouts.TotalTimeout
	if opts.Streaming {
		totalTimeout = 0
	}
	return &Client{
		http: &http.Client{
			Transport:     rt,
			Timeout:       totalTimeout,
			CheckRedirect: checkRedirect,
		},
		profile: profile,
		service: opts.Service,
	}
}
