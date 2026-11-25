# Data Model: SA-03 L0 EgressGuard & Resilient HTTP Client

> SA-03 is a stateless L0 network and security layer with **zero database tables**.

## 1. `internal/core/egress` Structures

| Type | Fields / Members | Defaults & Invariants |
|---|---|---|
| `Resolver` | `LookupNetIP(ctx, network, host) ([]netip.Addr, error)` | Defaults to `net.DefaultResolver` |
| `Options` | `Allowed []string`, `AllowedPorts []int`, `Resolver Resolver`, `DialTimeout time.Duration`, `MaxRedirects int` | `AllowedPorts=[80,443,8080,8443]`, `DialTimeout=10s`, `MaxRedirects=6` |
| `Guard` | Unexported `allowedHosts`, `allowedSuffixes`, `allowedPrefixes`, `allowedPorts`, `resolver`, `dialTimeout`, `maxRedirects` | Immutable after `New(opts)` (safe for concurrent use without locks) |

### Canonical Chinese `EgressDenied` Messages (`spec_03 §4.3`)
- Invalid URL syntax: `"不是有效的网址"`
- Non-http/https scheme: `"只能读 http / https 链接"`
- Missing host: `"链接里没有域名"`
- Disallowed port: `"不读这个端口上的地址"`
- Private/loopback/metadata/CGNAT/ULA/local suffix: `"这是内网或本机地址，不能读取"`
- DNS lookup failure: `"找不到这个域名"`
- Redirects > `MaxRedirects` (6): `"重定向次数过多"`

## 2. `internal/infra/httpclient` Structures

| Type | Key Fields / Constants | Defaults & Invariants |
|---|---|---|
| `Profile` | `ProfileTrusted = "trusted"`, `ProfileUntrusted = "untrusted"` | Immutable on `Client` |
| `TimeoutPolicy` | `DialTimeout`, `TLSHandshakeTimeout`, `ResponseHeaderTimeout`, `TotalTimeout`, `IdleReadTimeout` | `Dial=5s(Trusted)/10s(Untrusted)`, `TLS=10s`, `Header=30s`, `Total=60s` (`0` when `Streaming=true`), `IdleRead=0` |
| `RetryPolicy` | `MaxAttempts`, `BaseBackoff`, `MaxBackoff`, `MaxRetryAfter` | `MaxAttempts=3` (`1` when `Streaming=true`), `BaseBackoff=200ms`, `MaxBackoff=5s`, `MaxRetryAfter=30s` |
| `BreakerPolicy` | `Enabled`, `Interval`, `Timeout`, `MaxRequests`, `MinRequests`, `FailureRatio`, `ConsecutiveFail`, `MaxHostEntries` | `Enabled=true` (when zero struct), `Interval=60s`, `Timeout=30s`, `MaxRequests=2`, `MinRequests=5`, `FailureRatio=0.6`, `ConsecutiveFail=5`, `MaxHostEntries=256` |
| `ErrorKind` | `KindEgressDenied`, `KindCircuitOpen`, `KindTimeout`, `KindRateLimited`, `KindUpstream5xx`, `KindResponseTooLarge`, `KindNetwork`, `KindCanceled` | 8 frozen classification constants |
| `ClientError` | `Kind`, `Service`, `Host`, `StatusCode`, `RetryAfter`, `Attempts`, `Err` | Implements `Error() string` and `Unwrap() error`; `KindOf(err)` extracts `(Kind, true)` via `errors.As` |
