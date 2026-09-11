package metrics

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type pooler interface {
	Stat() *pgxpool.Stat
}

// NewPoolCollector reports the connection pool on scrape rather than on every
// acquire: under contention the pool is usually the thing that ran out, and
// the waiting queue is what tells you so.
func NewPoolCollector(pool pooler) prometheus.Collector {
	return &poolCollector{
		pool: pool,
		conns: prometheus.NewDesc(
			"pgxpool_connections",
			"Connections in the pool by state.",
			[]string{"state"}, nil),
		max: prometheus.NewDesc(
			"pgxpool_max_connections",
			"Configured upper bound on pool size.",
			nil, nil),
		acquires: prometheus.NewDesc(
			"pgxpool_acquires_total",
			"Connection acquisitions by result.",
			[]string{"result"}, nil),
		waitSeconds: prometheus.NewDesc(
			"pgxpool_acquire_wait_seconds_total",
			"Time spent waiting for a free connection.",
			nil, nil),
	}
}

type poolCollector struct {
	pool        pooler
	conns       *prometheus.Desc
	max         *prometheus.Desc
	acquires    *prometheus.Desc
	waitSeconds *prometheus.Desc
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.conns
	ch <- c.max
	ch <- c.acquires
	ch <- c.waitSeconds
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	stat := c.pool.Stat()

	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(stat.AcquiredConns()), "acquired")
	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(stat.IdleConns()), "idle")
	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(stat.ConstructingConns()), "constructing")
	ch <- prometheus.MustNewConstMetric(c.max, prometheus.GaugeValue, float64(stat.MaxConns()))

	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(stat.AcquireCount()), "total")
	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(stat.EmptyAcquireCount()), "waited")
	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(stat.CanceledAcquireCount()), "canceled")
	ch <- prometheus.MustNewConstMetric(c.waitSeconds, prometheus.CounterValue, stat.EmptyAcquireWaitTime().Seconds())
}
