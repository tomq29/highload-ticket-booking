package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry  *prometheus.Registry
	requests  *prometheus.HistogramVec
	outcomes  *prometheus.CounterVec
	inFlight  prometheus.Gauge
	collector prometheus.Registerer
}

// New labels every metric with the strategy the process runs, so two runs can
// be compared on one dashboard without renaming anything.
func New(strategy string) *Metrics {
	registry := prometheus.NewRegistry()
	reg := prometheus.WrapRegistererWith(prometheus.Labels{"strategy": strategy}, registry)

	m := &Metrics{
		registry:  registry,
		collector: reg,
		requests: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Request latency by route and status.",
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, []string{"route", "status"}),
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "booking_outcomes_total",
			Help: "Booking attempts by how they ended.",
		}, []string{"outcome"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Requests currently being served.",
		}),
	}

	reg.MustRegister(m.requests, m.outcomes, m.inFlight)
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// Register adds a collector such as the connection pool statistics.
func (m *Metrics) Register(c prometheus.Collector) {
	m.collector.MustRegister(c)
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) Observe(route string, status int, took time.Duration) {
	m.requests.WithLabelValues(route, strconv.Itoa(status)).Observe(took.Seconds())
}

func (m *Metrics) Outcome(outcome string) {
	m.outcomes.WithLabelValues(outcome).Inc()
}

func (m *Metrics) Started() { m.inFlight.Inc() }
func (m *Metrics) Done()    { m.inFlight.Dec() }
