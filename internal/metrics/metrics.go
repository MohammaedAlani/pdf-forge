// Package metrics centralizes Prometheus collectors used across the service.
package metrics

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ConversionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pdfforge_conversions_total",
			Help: "Total conversions processed, labeled by type and outcome.",
		},
		[]string{"type", "outcome"},
	)

	ConversionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pdfforge_conversion_duration_seconds",
			Help:    "Conversion latency in seconds, labeled by type.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 12), // 50ms ... ~100s
		},
		[]string{"type"},
	)

	OutputBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pdfforge_output_bytes",
			Help:    "Size of generated PDFs in bytes, labeled by type.",
			Buckets: prometheus.ExponentialBuckets(1024, 4, 10), // 1KB ... ~1GB
		},
		[]string{"type"},
	)

	WorkersInUse = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pdfforge_workers_in_use",
		Help: "Number of Chrome workers currently busy.",
	})

	AsyncQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pdfforge_async_queue_depth",
		Help: "Number of async jobs currently in flight.",
	})

	totalConv      atomic.Int64
	successfulConv atomic.Int64
	failedConv     atomic.Int64
)

// Record reports a conversion outcome for both Prometheus and the lightweight
// in-process counters consumed by /health.
func Record(convType, outcome string, durationSeconds float64, sizeBytes int) {
	ConversionsTotal.WithLabelValues(convType, outcome).Inc()
	ConversionDuration.WithLabelValues(convType).Observe(durationSeconds)
	if outcome == "success" {
		OutputBytes.WithLabelValues(convType).Observe(float64(sizeBytes))
		successfulConv.Add(1)
	} else {
		failedConv.Add(1)
	}
	totalConv.Add(1)
}

// Snapshot returns a cheap copy of the in-process counters for /health.
type Snapshot struct {
	Total      int64
	Successful int64
	Failed     int64
}

func Counters() Snapshot {
	return Snapshot{
		Total:      totalConv.Load(),
		Successful: successfulConv.Load(),
		Failed:     failedConv.Load(),
	}
}
