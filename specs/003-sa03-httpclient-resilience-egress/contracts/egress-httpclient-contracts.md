# Frozen Go Package Contracts: SA-03 (`egress` & `httpclient`)

Source of truth: [docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md](../../../docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md) `§4.1`–`§4.3`. All exported symbol names, parameter lists, and return types MUST match verbatim.

## 1. `internal/core/egress`

- `type Resolver interface { LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) }`
- `type Options struct { Allowed []string; AllowedPorts []int; Resolver Resolver; DialTimeout time.Duration; MaxRedirects int }`
- `type Guard struct`
- `var PublicOnly = MustNew(Options{})`
- `func New(opts Options) (*Guard, error)`
- `func MustNew(opts Options) *Guard`
- `func ToURI(raw string) (*url.URL, error)`
- `func IsPublic(addr netip.Addr) bool`
- `func (g *Guard) CheckURL(ctx context.Context, rawURL string) (*url.URL, error)`
- `func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error)`
- `func (g *Guard) CheckRedirect(req *http.Request, via []*http.Request) error`

## 2. `internal/infra/httpclient`

- `type Profile string` (`ProfileTrusted = "trusted"`, `ProfileUntrusted = "untrusted"`)
- `type TimeoutPolicy struct`, `type RetryPolicy struct`, `type BreakerPolicy struct`, `type Options struct`
- `type Client struct`
- `func NewTrusted(opts Options) (*Client, error)`
- `func NewUntrustedEgress(opts Options) (*Client, error)`
- `func (c *Client) Do(req *http.Request) (*http.Response, error)`
- `func (c *Client) StdClient() *http.Client`
- `func (c *Client) Profile() Profile`
- `func WithIdempotent(ctx context.Context) context.Context`
- `func WithService(ctx context.Context, service string) context.Context`
- `func ReadLimited(r io.Reader, maxBytes int64) ([]byte, error)`
- `type ErrorKind string` (`KindEgressDenied`, `KindCircuitOpen`, `KindTimeout`, `KindRateLimited`, `KindUpstream5xx`, `KindResponseTooLarge`, `KindNetwork`, `KindCanceled`)
- `type ClientError struct { Kind ErrorKind; Service, Host string; StatusCode int; RetryAfter time.Duration; Attempts int; Err error }`
  - `func (e *ClientError) Error() string`
  - `func (e *ClientError) Unwrap() error`
- `func KindOf(err error) (ErrorKind, bool)`
