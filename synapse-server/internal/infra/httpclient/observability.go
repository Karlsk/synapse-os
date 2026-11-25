package httpclient

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

var sensitiveQueryParts = []string{
	"token", "key", "secret", "password", "passwd", "signature", "sig", "code", "auth", "credential",
}

type observabilityRT struct {
	next          http.RoundTripper
	profile       Profile
	service       string
	logger        *slog.Logger
	requestsTotal *prometheus.CounterVec
	durationSecs  *prometheus.HistogramVec
}

func newObservabilityRT(
	next http.RoundTripper,
	profile Profile,
	service string,
	logger *slog.Logger,
	reg prometheus.Registerer,
) *observabilityRT {
	if logger == nil {
		logger = slog.Default()
	}
	o := &observabilityRT{
		next:    next,
		profile: profile,
		service: service,
		logger:  logger,
	}
	if reg != nil {
		reqTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "synapse_http_client_requests_total",
			Help: "Total outbound HTTP requests partitioned by service, profile, method, status class, and error kind.",
		}, []string{"service", "profile", "method", "status_class", "error_kind"})
		if err := reg.Register(reqTotal); err == nil {
			o.requestsTotal = reqTotal
		} else if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
				o.requestsTotal = existing
			}
		}

		durHist := prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "synapse_http_client_request_duration_seconds",
			Help:    "Outbound HTTP request duration distribution in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service", "profile", "method"})
		if err := reg.Register(durHist); err == nil {
			o.durationSecs = durHist
		} else if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.HistogramVec); ok {
				o.durationSecs = existing
			}
		}
	}
	return o
}

func (o *observabilityRT) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	svc := serviceFromContext(req.Context(), o.service)
	host := ""
	urlSanitized := ""
	if req.URL != nil {
		host = req.URL.Host
		urlSanitized = sanitizeURL(req.URL)
	}

	resp, err := o.next.RoundTrip(req)
	elapsed := time.Since(start)

	status := 0
	statusClass := "error"
	errKind := ""
	if resp != nil {
		status = resp.StatusCode
		statusClass = fmt.Sprintf("%dxx", status/100)
	}
	if err != nil {
		if k, ok := KindOf(err); ok {
			errKind = string(k)
		} else {
			errKind = string(classifyError(svc, host, 1, status, 0, err).Kind)
		}
	}

	if o.requestsTotal != nil {
		o.requestsTotal.WithLabelValues(svc, string(o.profile), req.Method, statusClass, errKind).Inc()
	}
	if o.durationSecs != nil {
		o.durationSecs.WithLabelValues(svc, string(o.profile), req.Method).Observe(elapsed.Seconds())
	}

	// Never log request/response headers or bodies; only sanitized URL and safe operational fields.
	attrs := []any{
		"service", svc,
		"profile", string(o.profile),
		"method", req.Method,
		"host", host,
		"url_sanitized", urlSanitized,
		"status", status,
		"duration_ms", elapsed.Milliseconds(),
		"error_kind", errKind,
	}
	if err != nil || status >= 500 {
		o.logger.WarnContext(req.Context(), "outbound http request", attrs...)
	} else {
		o.logger.InfoContext(req.Context(), "outbound http request", attrs...)
	}
	return resp, err
}

func sanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	cp := *u
	if cp.User != nil {
		if _, hasPwd := cp.User.Password(); hasPwd {
			cp.User = url.UserPassword(cp.User.Username(), "***")
		}
	}
	if cp.RawQuery != "" {
		q := cp.Query()
		for k := range q {
			if isSensitiveQueryKey(k) {
				q.Set(k, obs.RedactedValue)
			}
		}
		cp.RawQuery = q.Encode()
	}
	return strings.Replace(cp.String(), "%2A%2A%2A", "***", 1)
}

func isSensitiveQueryKey(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, pat := range sensitiveQueryParts {
		if strings.Contains(k, pat) {
			return true
		}
	}
	return false
}
