package observability

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestCardinalityExplodes is the mistake that takes Prometheus down, measured.
func TestCardinalityExplodes(t *testing.T) {
	const requests = 1_000

	measure := func(label func(*http.Request) string, capture bool) int {
		m := NewMetrics("card", label, nil)

		inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		mux := http.NewServeMux()
		if capture {
			mux.Handle("GET /users/{id}", CaptureRoute(inner))
		} else {
			mux.Handle("GET /users/{id}", inner)
		}

		handler := m.Middleware(mux)

		for i := range requests {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET",
				fmt.Sprintf("/users/%d", i), nil))
		}

		count, err := m.SeriesCount()
		if err != nil {
			t.Fatal(err)
		}

		return count
	}

	byRoute := measure(RouteFromPattern, true)
	byPath := measure(RawPath, false)

	t.Logf("%d requests to /users/{id} with %d distinct ids", requests, requests)
	t.Logf("labelled by ROUTE: %d time series", byRoute)
	t.Logf("labelled by PATH:  %d time series", byPath)
	t.Logf("%.0fx", float64(byPath)/float64(byRoute))

	// By route the count is a constant: one counter series, one gauge, and the histograms' buckets.
	// It does not grow with the number of distinct ids at all, which is the property that matters
	// and the reason the assertion is on the RATIO rather than on either number.
	if byRoute > 50 {
		t.Errorf("labelling by route produced %d series for one endpoint", byRoute)
	}
	if float64(byPath)/float64(byRoute) < 100 {
		t.Errorf("only %.0fx between the two labellings", float64(byPath)/float64(byRoute))
	}

	// By path, one series per counter and per histogram, per id.
	if byPath < requests {
		t.Errorf("labelling by path produced only %d series for %d ids", byPath, requests)
	}

	t.Log("a series costs a few kilobytes of resident memory in Prometheus, so a million user " +
		"ids is gigabytes of data nobody queries. The label has to be the route.")

	// And the histogram is why it is worse than it looks: a histogram with 12 buckets is 14 series
	// per label combination, not one.
	m := NewMetrics("buckets", RouteFromPattern, nil)
	m.RequestDuration.WithLabelValues("/a", "GET").Observe(0.1)

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	for _, family := range families {
		if !strings.Contains(family.GetName(), "duration") {
			continue
		}

		h := family.GetMetric()[0].GetHistogram()

		// GetBucket excludes the implicit +Inf bucket, which the exposition format always
		// emits, so the stored count is buckets + 1 + _sum + _count.
		t.Logf("one histogram observation creates %d explicit buckets, plus +Inf, _sum and "+
			"_count: %d stored series per label combination",
			len(h.GetBucket()), len(h.GetBucket())+3)

		if len(h.GetBucket()) != len(DefaultBuckets) {
			t.Errorf("%d buckets, want %d", len(h.GetBucket()), len(DefaultBuckets))
		}
	}
}

// TestUnmatchedPathsAreOneSeries, because a scanner probing a thousand URLs must not create a thousand series.
func TestUnmatchedPathsAreOneSeries(t *testing.T) {
	m := NewMetrics("scan", RouteFromPattern, nil)

	mux := http.NewServeMux()
	mux.Handle("GET /known", CaptureRoute(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))

	handler := m.Middleware(mux)

	// A scan.
	for i := range 500 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET",
			fmt.Sprintf("/wp-admin/%d.php", i), nil))
	}

	count, err := m.SeriesCount()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("500 requests to 500 distinct unmatched paths produced %d series", count)

	// A constant, whatever the scan tried. 500 distinct paths under RawPath would be about 13,500.
	if count > 50 {
		t.Errorf("%d series from a scan", count)
	}

	series, err := seriesNames(m)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range series {
		if strings.Contains(s, ".php") {
			t.Errorf("a scanned path became a label: %s", s)
		}
	}

	t.Logf("all under one label: %v", series[:min(3, len(series))])
}

// TestInFlightSurvivesAPanic, because a gauge that only goes up looks exactly like a leak.
func TestInFlightSurvivesAPanic(t *testing.T) {
	m := NewMetrics("panic", RouteFromPattern, nil)

	handler := m.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	}))

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("the panic did not propagate")
			}
		}()

		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()

	value := gaugeValue(t, m.InFlight)

	t.Logf("after a panicking request, in-flight is %g", value)

	if value != 0 {
		t.Errorf("in-flight is %g after a panic; the Dec must be deferred", value)
	}

	t.Log("without the deferred Dec this reads 1 forever, and a gauge that only goes up is " +
		"indistinguishable from a leak, so the panic gets misdiagnosed")
}

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()

	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatal(err)
	}

	return m.GetGauge().GetValue()
}

// TestHandlerOutputIsScrapable, so the exposition format is exercised rather than assumed.
func TestHandlerOutputIsScrapable(t *testing.T) {
	m := NewMetrics("expo", RouteFromPattern, nil)

	m.RequestsTotal.WithLabelValues("/users/{id}", "GET", "200").Inc()
	m.RequestsTotal.WithLabelValues("/users/{id}", "GET", "200").Inc()
	m.RequestsTotal.WithLabelValues("/users/{id}", "POST", "201").Inc()
	m.RequestDuration.WithLabelValues("/users/{id}", "GET").Observe(0.013)
	m.InFlight.Set(3)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}

	body := rec.Body.String()

	t.Logf("Content-Type: %s", rec.Header().Get("Content-Type"))
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		t.Logf("  %s", line)
	}

	for _, want := range []string{
		`# TYPE expo_http_requests_total counter`,
		`expo_http_requests_total{method="GET",route="/users/{id}",status="200"} 2`,
		`expo_http_requests_in_flight 3`,
		`expo_http_request_duration_seconds_count{method="GET",route="/users/{id}"} 1`,
		`le="+Inf"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the output does not contain %q", want)
		}
	}

	// The _total suffix is not decoration: it is how the format marks a counter.
	if !strings.Contains(body, "_total") {
		t.Error("no _total suffix, so the series would be reported as untyped")
	}
}

// TestBucketsCannotBeChangedLater, stated as a measurement rather than a warning.
func TestBucketsCannotBeChangedLater(t *testing.T) {
	first := NewMetrics("rebucket", RouteFromPattern, []float64{0.1, 1})
	first.RequestDuration.WithLabelValues("/a", "GET").Observe(0.5)

	second := NewMetrics("rebucket", RouteFromPattern, []float64{0.01, 0.1, 1, 10})
	second.RequestDuration.WithLabelValues("/a", "GET").Observe(0.5)

	firstCount, err := first.SeriesCount()
	if err != nil {
		t.Fatal(err)
	}

	secondCount, err := second.SeriesCount()
	if err != nil {
		t.Fatal(err)
	}

	firstLabels, err := first.LabelCombinations()
	if err != nil {
		t.Fatal(err)
	}

	secondLabels, err := second.LabelCombinations()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("2 buckets: %d label combinations, %d stored series", firstLabels, firstCount)
	t.Logf("4 buckets: %d label combinations, %d stored series", secondLabels, secondCount)

	// The two numbers are the whole point. The label count is identical and the stored series count
	// is not, which is what "bucket count multiplies cardinality" means.
	if firstLabels != secondLabels {
		t.Errorf("the label combinations differ (%d against %d), so this comparison is not "+
			"isolating the buckets", firstLabels, secondLabels)
	}

	if secondCount <= firstCount {
		t.Errorf("more buckets should mean more series: %d against %d", secondCount, firstCount)
	}

	t.Log("bucket count MULTIPLIES cardinality rather than adding to it, because each bucket " +
		"is a series per label combination. And changing the boundaries later resets the " +
		"series, so every dashboard and alert built on it breaks.")

	// The default set, for the record.
	t.Logf("DefaultBuckets covers %v to %v in %d buckets",
		DefaultBuckets[0], DefaultBuckets[len(DefaultBuckets)-1], len(DefaultBuckets))

	if DefaultBuckets[0] > 0.005 {
		t.Error("the fastest bucket is too coarse for an HTTP service")
	}
}

// TestRouteFromPattern.
func TestRouteFromPattern(t *testing.T) {
	for _, tc := range []struct{ pattern, want string }{
		{"GET /users/{id}", "/users/{id}"},
		{"/users/{id}", "/users/{id}"},
		{"POST /", "/"},
		{"GET example.com/users/{id}", "/users/{id}"},
		{"GET /files/{path...}", "/files/{path...}"},
		{"", "unmatched"},
	} {
		r := httptest.NewRequest("GET", "/whatever", nil)
		r.Pattern = tc.pattern

		if got := RouteFromPattern(r); got != tc.want {
			t.Errorf("%q gave %q, want %q", tc.pattern, got, tc.want)
		}
	}

	t.Log("the method is stripped because it duplicates the method label, and the host because " +
		"it would split one route into one series per hostname")
}
