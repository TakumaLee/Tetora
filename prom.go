package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// --- Prometheus Metrics (text exposition format, no external dependency) ---

// metrics is the global metrics registry.
var metrics *MetricsRegistry

// initMetrics creates and registers all Tetora metrics.
func initMetrics() {
	metrics = newMetricsRegistry()

	// Dispatch metrics.
	metrics.RegisterCounter("tetora_dispatch_total", "Total dispatches", []string{"role", "status"})
	metrics.RegisterHistogram("tetora_dispatch_duration_seconds", "Dispatch latency", []string{"role"}, defaultBuckets)
	metrics.RegisterCounter("tetora_dispatch_cost_usd", "Total cost in USD", []string{"role"})

	// Provider metrics.
	metrics.RegisterCounter("tetora_provider_requests_total", "Provider API calls", []string{"provider", "status"})
	metrics.RegisterHistogram("tetora_provider_latency_seconds", "Provider response time", []string{"provider"}, defaultBuckets)
	metrics.RegisterCounter("tetora_provider_tokens_total", "Token usage", []string{"provider", "direction"})

	// Infrastructure metrics.
	metrics.RegisterGauge("tetora_circuit_state", "Circuit breaker state (0=closed,1=open,2=half-open)", []string{"provider"})
	metrics.RegisterGauge("tetora_session_active", "Active session count", []string{"role"})
	metrics.RegisterGauge("tetora_queue_depth", "Offline queue depth", nil)
	metrics.RegisterCounter("tetora_cron_runs_total", "Cron job executions", []string{"status"})
}

var defaultBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// --- Metric Types ---

type metricType int

const (
	metricCounter metricType = iota
	metricGauge
	metricHistogram
)

type metricDef struct {
	name    string
	help    string
	typ     metricType
	labels  []string
	buckets []float64 // histogram only
}

type labelKey struct {
	name   string
	labels string // sorted label=value pairs
}

type counterValue struct {
	value float64
}

type gaugeValue struct {
	value float64
}

type histogramValue struct {
	count   uint64
	sum     float64
	buckets []histBucket
}

type histBucket struct {
	le    float64
	count uint64
}

// MetricsRegistry holds all metrics.
type MetricsRegistry struct {
	mu         sync.RWMutex
	defs       []metricDef
	counters   map[labelKey]*counterValue
	gauges     map[labelKey]*gaugeValue
	histograms map[labelKey]*histogramValue
	startTime  time.Time
}

func newMetricsRegistry() *MetricsRegistry {
	return &MetricsRegistry{
		counters:   make(map[labelKey]*counterValue),
		gauges:     make(map[labelKey]*gaugeValue),
		histograms: make(map[labelKey]*histogramValue),
		startTime:  time.Now(),
	}
}

func (r *MetricsRegistry) RegisterCounter(name, help string, labels []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = append(r.defs, metricDef{name: name, help: help, typ: metricCounter, labels: labels})
}

func (r *MetricsRegistry) RegisterGauge(name, help string, labels []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = append(r.defs, metricDef{name: name, help: help, typ: metricGauge, labels: labels})
}

func (r *MetricsRegistry) RegisterHistogram(name, help string, labels []string, buckets []float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = append(r.defs, metricDef{name: name, help: help, typ: metricHistogram, labels: labels, buckets: buckets})
}

// --- Counter Operations ---

func (r *MetricsRegistry) CounterInc(name string, labelValues ...string) {
	r.CounterAdd(name, 1, labelValues...)
}

func (r *MetricsRegistry) CounterAdd(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[key]
	if !ok {
		c = &counterValue{}
		r.counters[key] = c
	}
	c.value += val
}

// --- Gauge Operations ---

func (r *MetricsRegistry) GaugeSet(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[key]
	if !ok {
		g = &gaugeValue{}
		r.gauges[key] = g
	}
	g.value = val
}

func (r *MetricsRegistry) GaugeInc(name string, labelValues ...string) {
	r.GaugeAdd(name, 1, labelValues...)
}

func (r *MetricsRegistry) GaugeDec(name string, labelValues ...string) {
	r.GaugeAdd(name, -1, labelValues...)
}

func (r *MetricsRegistry) GaugeAdd(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[key]
	if !ok {
		g = &gaugeValue{}
		r.gauges[key] = g
	}
	g.value += val
}

// --- Histogram Operations ---

func (r *MetricsRegistry) HistogramObserve(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.histograms[key]
	if !ok {
		// Find the metric def for bucket configuration.
		var buckets []float64
		for _, d := range r.defs {
			if d.name == name && d.typ == metricHistogram {
				buckets = d.buckets
				break
			}
		}
		if buckets == nil {
			buckets = defaultBuckets
		}
		h = &histogramValue{
			buckets: make([]histBucket, len(buckets)),
		}
		for i, b := range buckets {
			h.buckets[i].le = b
		}
		r.histograms[key] = h
	}
	h.count++
	h.sum += val
	for i := range h.buckets {
		if val <= h.buckets[i].le {
			h.buckets[i].count++
		}
	}
}

// --- Key Helpers ---

func (r *MetricsRegistry) makeKey(name string, labelValues []string) labelKey {
	// Find the corresponding metric definition.
	var labels []string
	for _, d := range r.defs {
		if d.name == name {
			labels = d.labels
			break
		}
	}

	var labelStr string
	if len(labels) > 0 && len(labelValues) > 0 {
		pairs := make([]string, 0, len(labels))
		for i, l := range labels {
			val := ""
			if i < len(labelValues) {
				val = labelValues[i]
			}
			pairs = append(pairs, fmt.Sprintf(`%s="%s"`, l, val))
		}
		labelStr = strings.Join(pairs, ",")
	}

	return labelKey{name: name, labels: labelStr}
}

// --- Exposition ---

// WriteMetrics writes all metrics in Prometheus text exposition format.
func (r *MetricsRegistry) WriteMetrics(w io.Writer) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, def := range r.defs {
		switch def.typ {
		case metricCounter:
			fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
			fmt.Fprintf(w, "# TYPE %s counter\n", def.name)
			r.writeCounterValues(w, def.name)

		case metricGauge:
			fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
			fmt.Fprintf(w, "# TYPE %s gauge\n", def.name)
			r.writeGaugeValues(w, def.name)

		case metricHistogram:
			fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
			fmt.Fprintf(w, "# TYPE %s histogram\n", def.name)
			r.writeHistogramValues(w, def.name)
		}
		fmt.Fprintln(w)
	}
}

func (r *MetricsRegistry) writeCounterValues(w io.Writer, name string) {
	found := false
	for key, val := range r.counters {
		if key.name == name {
			found = true
			if key.labels != "" {
				fmt.Fprintf(w, "%s{%s} %g\n", name, key.labels, val.value)
			} else {
				fmt.Fprintf(w, "%s %g\n", name, val.value)
			}
		}
	}
	if !found {
		// No data points yet — don't output anything.
	}
}

func (r *MetricsRegistry) writeGaugeValues(w io.Writer, name string) {
	found := false
	for key, val := range r.gauges {
		if key.name == name {
			found = true
			if key.labels != "" {
				fmt.Fprintf(w, "%s{%s} %g\n", name, key.labels, val.value)
			} else {
				fmt.Fprintf(w, "%s %g\n", name, val.value)
			}
		}
	}
	if !found {
		// No data points yet — don't output anything.
	}
}

func (r *MetricsRegistry) writeHistogramValues(w io.Writer, name string) {
	for key, val := range r.histograms {
		if key.name == name {
			labelPrefix := ""
			if key.labels != "" {
				labelPrefix = key.labels + ","
			}
			for _, b := range val.buckets {
				fmt.Fprintf(w, "%s_bucket{%sle=\"%g\"} %d\n", name, labelPrefix, b.le, b.count)
			}
			fmt.Fprintf(w, "%s_bucket{%sle=\"+Inf\"} %d\n", name, labelPrefix, val.count)
			if key.labels != "" {
				fmt.Fprintf(w, "%s_sum{%s} %g\n", name, key.labels, val.sum)
				fmt.Fprintf(w, "%s_count{%s} %d\n", name, key.labels, val.count)
			} else {
				fmt.Fprintf(w, "%s_sum %g\n", name, val.sum)
				fmt.Fprintf(w, "%s_count %d\n", name, val.count)
			}
		}
	}
}

// --- Convenience: Record Dispatch Metrics ---

func recordDispatchMetrics(role, status string, durationSec float64, costUSD float64, tokensIn, tokensOut int, provider string) {
	if metrics == nil {
		return
	}
	metrics.CounterInc("tetora_dispatch_total", role, status)
	metrics.HistogramObserve("tetora_dispatch_duration_seconds", durationSec, role)
	if costUSD > 0 {
		metrics.CounterAdd("tetora_dispatch_cost_usd", costUSD, role)
	}
	if provider != "" {
		metrics.CounterInc("tetora_provider_requests_total", provider, status)
		metrics.HistogramObserve("tetora_provider_latency_seconds", durationSec, provider)
		if tokensIn > 0 {
			metrics.CounterAdd("tetora_provider_tokens_total", float64(tokensIn), provider, "input")
		}
		if tokensOut > 0 {
			metrics.CounterAdd("tetora_provider_tokens_total", float64(tokensOut), provider, "output")
		}
	}
}
