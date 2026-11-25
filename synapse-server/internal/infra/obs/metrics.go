package obs

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics owns an isolated Prometheus Registry whose metrics always carry the
// constant label application="<appName>". It never mutates prometheus.DefaultRegisterer.
type Metrics struct {
	ChatTurnsTotal   *prometheus.CounterVec
	TaskRunsTotal    *prometheus.CounterVec
	TaskQueueDepth   *prometheus.GaugeVec
	TokensTotal      *prometheus.CounterVec
	CLIWarmPoolSize  prometheus.Gauge
	IMConnected      *prometheus.GaugeVec
	HTTPRequestsSecs *prometheus.HistogramVec

	registry   *prometheus.Registry
	registerer prometheus.Registerer
}

// NewMetrics builds a fresh isolated Prometheus registry and registers runtime
// collectors, 6 core superagent_* business metrics, and HTTP duration histograms.
func NewMetrics(appName string) (*Metrics, error) {
	if strings.TrimSpace(appName) == "" {
		appName = "synapse-server"
	}

	reg := prometheus.NewRegistry()
	wrapped := prometheus.WrapRegistererWith(prometheus.Labels{"application": appName}, reg)

	m := &Metrics{
		ChatTurnsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "superagent_chat_turns_total",
			Help: "Total completed chat turns partitioned by company, CLI runtime, and outcome.",
		}, []string{"company_id", "cli", "outcome"}),
		TaskRunsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "superagent_task_runs_total",
			Help: "Total terminal task executions partitioned by company, flow, and status.",
		}, []string{"company_id", "flow", "status"}),
		TaskQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "superagent_task_queue_depth",
			Help: "Current number of tasks per company and scheduling status.",
		}, []string{"company_id", "status"}),
		TokensTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "superagent_tokens_total",
			Help: "Total LLM tokens consumed partitioned by company, CLI, model, and kind.",
		}, []string{"company_id", "cli", "model", "kind"}),
		CLIWarmPoolSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "superagent_cli_warm_pool_size",
			Help: "Current number of warm CLI processes held in the Claude warm pool.",
		}),
		IMConnected: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "superagent_im_connected",
			Help: "Current IM outbound WebSocket connection status (1=connected, 0=disconnected).",
		}, []string{"company_id", "channel"}),
		HTTPRequestsSecs: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_requests_seconds",
			Help:    "HTTP server request latency distribution in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route", "status"}),
		registry:   reg,
		registerer: wrapped,
	}

	toRegister := []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.ChatTurnsTotal,
		m.TaskRunsTotal,
		m.TaskQueueDepth,
		m.TokensTotal,
		m.CLIWarmPoolSize,
		m.IMConnected,
		m.HTTPRequestsSecs,
	}
	for _, c := range toRegister {
		if err := wrapped.Register(c); err != nil {
			return nil, fmt.Errorf("register prometheus collector: %w", err)
		}
	}

	return m, nil
}

// Registerer returns the application-labeled prometheus.Registerer for downstream
// infrastructure packages (e.g. SA-02 db, SA-03 httpclient) to register metrics.
func (m *Metrics) Registerer() prometheus.Registerer {
	return m.registerer
}

// Gatherer returns the isolated prometheus.Gatherer exposed at /actuator/prometheus.
func (m *Metrics) Gatherer() prometheus.Gatherer {
	return m.registry
}
