package observability

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// The four metric types, and the one thing to know about each.
//
//	Counter    only goes up. Rate is computed by the query, not by the code, so a counter of
//	           "requests" is right and a gauge of "requests per second" is wrong: the gauge loses
//	           everything between scrapes.
//	Gauge      a value that goes up and down. Queue depth, connections in use, temperature.
//	Histogram  observations bucketed at FIXED boundaries chosen in advance. Cheap, aggregatable
//	           across instances, and the buckets cannot be changed retroactively.
//	Summary    quantiles computed in the process. Exact for that process and NOT AGGREGATABLE: you
//	           cannot average two p99s. Almost always the wrong choice in a service with more than
//	           one replica, which is why this package has no summary.
//
// The summary rule is the one that costs people a dashboard rebuild. A p99 from each of five replicas cannot be
// combined into a p99 across them, so the graph is five lines and the number nobody wanted. A histogram's
// buckets add, so the same query works for one replica or fifty.

// Metrics is a service's metric set.
//
// # Why this is a struct and not package-level variables
//
// prometheus/client_golang's examples use package-level `promauto.NewCounter`, which registers into the default
// registry at init time. That works and makes the metrics untestable: two tests in the same binary cannot each
// have a clean registry, and a test that asserts on a counter sees the value another test left.
//
// A struct with its own registry is a few more lines and every test gets a fresh one.
type Metrics struct {
	Registry *prometheus.Registry

	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	InFlight        prometheus.Gauge
	ResponseSize    *prometheus.HistogramVec

	// pathLabel maps a request to a label value, and it is the single most important field here.
	// See NewMetrics.
	pathLabel func(*http.Request) string
}

// DefaultBuckets are latency buckets for a web service, in seconds.
//
// # Why not prometheus.DefBuckets
//
// The library's defaults are .005 to 10 seconds in 11 buckets, which is a reasonable general shape and is wrong
// at both ends for most HTTP services. Nothing useful happens between 5 and 10 seconds (the request has already
// failed), and the resolution between 1ms and 100ms is where every real decision is made.
//
// These are powers of two from 1ms to about 2s, plus a 5s bucket for the tail. Exponential rather than linear,
// because latency distributions are log-normal and linear buckets put ten of them where nothing happens.
//
// The thing to know: a histogram's buckets are baked in at creation. Changing them later resets the series, so
// every dashboard and alert built on it breaks. Choosing them badly is the one metric mistake that is expensive
// to fix.
var DefaultBuckets = []float64{
	0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5,
}

// NewMetrics builds a metric set.
//
// # The pathLabel parameter is the cardinality decision
//
// `http_requests_total{path="/users/12345"}` creates one time series per user id. A million users is a million
// series, each with its own sample every scrape, and Prometheus falls over: the usual figure is that a series
// costs a few kilobytes of RAM, so a million is gigabytes, for data nobody queries.
//
// The label has to be the ROUTE, not the path: "/users/{id}", one series. Getting this from the router is easy
// and getting it from r.URL.Path is the mistake, so the function is a required parameter rather than a default
// that happens to be wrong.
//
// TestCardinalityExplodes measures the difference.
func NewMetrics(namespace string, pathLabel func(*http.Request) string, buckets []float64) *Metrics {
	if pathLabel == nil {
		// Not a sensible default: a constant. Every request counted under one label is less
		// useful and cannot take the service down, which is the right way round for a fallback.
		pathLabel = func(*http.Request) string { return "unlabelled" }
	}

	if len(buckets) == 0 {
		buckets = DefaultBuckets
	}

	m := &Metrics{
		Registry:  prometheus.NewRegistry(),
		pathLabel: pathLabel,
	}

	m.RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "http_requests_total",

		// The convention is name_unit_total for a counter, and the _total suffix is not
		// decoration: Prometheus's own tooling and the OpenMetrics format treat it as the marker
		// for a counter, and a counter without it is reported as an untyped series.
		Help: "Requests by route, method and status.",
	}, []string{"route", "method", "status"})

	m.RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "Request duration by route and method.",

		// SECONDS, always, and as a float. Prometheus's convention is base units, so seconds and
		// bytes rather than milliseconds and kilobytes. A dashboard that has to know whether this
		// particular metric is in milliseconds is a dashboard with a bug in it.
		Buckets: buckets,
	}, []string{"route", "method"})

	m.ResponseSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_response_size_bytes",
		Help:      "Response size by route.",
		Buckets:   prometheus.ExponentialBuckets(64, 4, 8),
	}, []string{"route"})

	m.InFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "http_requests_in_flight",
		Help:      "Requests currently being served.",
	})

	m.Registry.MustRegister(m.RequestsTotal, m.RequestDuration, m.ResponseSize, m.InFlight)

	return m
}

// routeHolder is a mutable slot in the context, so an inner middleware can tell an outer one what route was
// matched.
//
// # Why this is needed at all
//
// r.Pattern is set by http.ServeMux when it MATCHES, which happens inside the mux's ServeHTTP. A metrics
// middleware wrapping the mux from outside therefore sees an empty Pattern: at the moment it runs, the request
// has not been routed yet. The first version of this package labelled everything "unmatched" and the test
// caught it.
//
// The options were:
//
//	register the middleware per route, so it runs after matching. Correct, and it means every
//	  route registration carries it and one forgotten route is one unmeasured endpoint.
//	read the route after next.ServeHTTP returns. The mux passes a CLONED request to the handler,
//	  so the outer middleware's r is not the one with the pattern set. Does not work.
//	a pointer in the context, filled by a middleware registered inside the mux. One shared slot,
//	  written once, read by whoever put it there.
//
// The third is what chi does with its RouteContext and what this does. The mutable-value-in-a-context shape is
// unusual and is the only way to pass information back OUT through a handler chain.
type routeHolder struct{ route string }

type routeKey struct{}

// CaptureRoute publishes the matched route so an outer middleware can label by it.
//
// Registered INSIDE the mux, so it runs after matching:
//
//	mux.Handle("GET /users/{id}", observability.CaptureRoute(handler))
//
// or once around the mux's own handler if every route goes through one place.
func CaptureRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if holder, ok := r.Context().Value(routeKey{}).(*routeHolder); ok && r.Pattern != "" {
			holder.route = RouteFromPattern(r)
		}

		next.ServeHTTP(w, r)
	})
}

// Middleware records metrics for each request.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The holder goes in before the handler runs, and the label is read after. So the route
		// label comes from whatever CaptureRoute published, and falls back to pathLabel for a
		// request that never matched a route.
		holder := &routeHolder{}

		r = r.WithContext(context.WithValue(r.Context(), routeKey{}, holder))

		m.InFlight.Inc()

		// Dec in a defer, not at the end of the function. A handler that panics otherwise leaves
		// the gauge permanently high, and a gauge that only goes up is indistinguishable from a
		// leak, so the panic gets misdiagnosed as one.
		defer m.InFlight.Dec()

		start := time.Now()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		duration := time.Since(start).Seconds()

		route := holder.route
		if route == "" {
			route = m.pathLabel(r)
		}

		m.RequestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		m.RequestDuration.WithLabelValues(route, r.Method).Observe(duration)
		m.ResponseSize.WithLabelValues(route).Observe(float64(rec.written))
	})
}

// statusRecorder captures the status code and bytes written.
//
// The same wrapper as the one in http-tutorial's middleware package, and the same caveats: it has to implement
// Unwrap so http.ResponseController can reach the real writer, or Flush stops working and a streaming endpoint
// buffers.
type statusRecorder struct {
	http.ResponseWriter

	status      int
	written     int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}

	r.status = status
	r.wroteHeader = true

	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}

	n, err := r.ResponseWriter.Write(b)
	r.written += n

	return n, err
}

// Unwrap lets http.NewResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// SeriesCount counts the time series Prometheus would STORE, which is not the number of label combinations.
//
// The distinction cost a failing test. `Gather` returns one `dto.Metric` per label combination, so counting
// those says a histogram is one series. In the exposition format the same histogram is one `_bucket` series per
// boundary plus `_sum` and `_count`: with this package's 12 buckets, 15 series for one label combination.
//
// So bucket count MULTIPLIES cardinality rather than adding to it, and a count of label combinations understates
// what a histogram costs by fifteen times. Measuring the wrong one is how a metric that looks cheap fills a
// Prometheus.
func (m *Metrics) SeriesCount() (int, error) {
	families, err := m.Registry.Gather()
	if err != nil {
		return 0, err
	}

	count := 0

	for _, family := range families {
		for _, metric := range family.GetMetric() {
			switch {
			case metric.GetHistogram() != nil:
				// One per bucket, plus the implicit +Inf bucket, plus _sum and _count.
				count += len(metric.GetHistogram().GetBucket()) + 3

			case metric.GetSummary() != nil:
				count += len(metric.GetSummary().GetQuantile()) + 2

			default:
				count++
			}
		}
	}

	return count, nil
}

// LabelCombinations counts what Gather returns, which is the other number and the one that is easy to mistake
// for the first.
//
// Useful on its own: it is the count that grows when a LABEL takes a new value, so it isolates the cardinality
// caused by labels from the cardinality caused by bucket choice.
func (m *Metrics) LabelCombinations() (int, error) {
	families, err := m.Registry.Gather()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, family := range families {
		count += len(family.GetMetric())
	}

	return count, nil
}

// RouteFromPattern turns a Go 1.22 ServeMux pattern into a metric label.
//
// http.Request.Pattern, added in Go 1.23, is the registered pattern the request matched, which is exactly the
// right label value and did not exist before. Any code older than that had to maintain a separate mapping, which
// is why so many services label by raw path.
//
// The method and the host are stripped, because "GET /users/{id}" as a label value duplicates the method label
// and a host prefix splits one route into one series per hostname.
func RouteFromPattern(r *http.Request) string {
	if r.Pattern == "" {
		// No pattern means no route matched, which is a 404. Labelling those with the path is
		// exactly the cardinality bug: a scanner probing a thousand URLs would create a thousand
		// series. One label for all of them.
		return "unmatched"
	}

	pattern := r.Pattern

	// Strip a leading method.
	if i := strings.Index(pattern, " "); i >= 0 {
		pattern = pattern[i+1:]
	}

	// Strip a host.
	if i := strings.Index(pattern, "/"); i > 0 {
		pattern = pattern[i:]
	}

	return pattern
}

// RawPath is the WRONG label function, kept so the cardinality test can measure it.
//
// Every distinct URL becomes a time series. Do not use this.
func RawPath(r *http.Request) string { return r.URL.Path }

// Handler serves the metrics endpoint.
//
// Written by hand rather than with promhttp, for one reason worth stating: promhttp.HandlerFor is the right
// answer and carries options this package would have to explain (error handling, compression, concurrency
// limits). The text encoding itself is fifteen lines and seeing it makes the format obvious.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		families, err := m.Registry.Gather()
		if err != nil {
			// A partial scrape is worse than a failed one: Prometheus would record the metrics
			// that did gather and nothing would indicate the rest were missing.
			http.Error(w, "gathering metrics: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		var b strings.Builder

		for _, family := range families {
			fmt.Fprintf(&b, "# HELP %s %s\n", family.GetName(), family.GetHelp())
			fmt.Fprintf(&b, "# TYPE %s %s\n", family.GetName(),
				strings.ToLower(family.GetType().String()))

			for _, metric := range family.GetMetric() {
				writeMetric(&b, family.GetName(), metric)
			}
		}

		_, _ = io.WriteString(w, b.String())
	})
}

func writeMetric(b *strings.Builder, name string, m *dto.Metric) {
	labels := formatLabels(m.GetLabel())

	switch {
	case m.GetCounter() != nil:
		fmt.Fprintf(b, "%s%s %g\n", name, labels, m.GetCounter().GetValue())

	case m.GetGauge() != nil:
		fmt.Fprintf(b, "%s%s %g\n", name, labels, m.GetGauge().GetValue())

	case m.GetHistogram() != nil:
		h := m.GetHistogram()

		// A histogram is several series: one _bucket per boundary, plus _sum and _count. That is
		// why a histogram with 12 buckets and 3 labels of 10 values each is 12 * 1000 + 2000
		// series, and why bucket count multiplies cardinality rather than adding to it.
		for _, bucket := range h.GetBucket() {
			fmt.Fprintf(b, "%s_bucket%s %d\n",
				name, withLabel(labels, "le", formatFloat(bucket.GetUpperBound())),
				bucket.GetCumulativeCount())
		}

		fmt.Fprintf(b, "%s_bucket%s %d\n", name, withLabel(labels, "le", "+Inf"),
			h.GetSampleCount())
		fmt.Fprintf(b, "%s_sum%s %g\n", name, labels, h.GetSampleSum())
		fmt.Fprintf(b, "%s_count%s %d\n", name, labels, h.GetSampleCount())
	}
}

func formatLabels(pairs []*dto.LabelPair) string {
	if len(pairs) == 0 {
		return ""
	}

	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%s=%q", p.GetName(), p.GetValue()))
	}

	return "{" + strings.Join(parts, ",") + "}"
}

func withLabel(labels, name, value string) string {
	pair := fmt.Sprintf("%s=%q", name, value)

	if labels == "" {
		return "{" + pair + "}"
	}

	return labels[:len(labels)-1] + "," + pair + "}"
}

func formatFloat(f float64) string {
	if f == math.Inf(1) {
		return "+Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
