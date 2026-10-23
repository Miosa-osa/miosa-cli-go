package commands_test

import (
	"strings"
	"testing"
)

func metricsRoutes() map[string]any {
	return map[string]any{
		// resolveServiceProject: a project reference is a name
		"GET /projects": `{"data":[{"id":"11111111-1111-4111-8111-111111111111","name":"shop","slug":"shop"}]}`,
		"GET /projects/11111111-1111-4111-8111-111111111111/services": `{"data":[
			{"id":"22222222-2222-4222-8222-222222222222","name":"API","slug":"api","kind":"server"}]}`,
		"GET /projects/11111111-1111-4111-8111-111111111111/services/22222222-2222-4222-8222-222222222222/metrics/series": `{"data":{
			"resource_type":"service","source":"service_metrics",
			"available":["requests","latency"],
			"series":[
				{"t":1761200000,"requests":12,"errors":1,"error_rate":0.0833,"response_ms_p50":21,"response_ms_p95":140},
				{"t":1761200060,"requests":0,"errors":0,"error_rate":0,"response_ms_p50":null,"response_ms_p95":null}
			]}}`,
	}
}

func TestServiceMetricsPrintsTheSeriesAndSaysWhatIsReal(t *testing.T) {
	f := fakeRoutes(t, metricsRoutes())

	out, err := run(t, "service", "metrics", "shop", "API", "--range", "1h")
	if err != nil {
		t.Fatalf("service metrics: %v\n%s", err, out)
	}

	for _, want := range []string{"REQUESTS", "8.3%", "140", "Measured: requests, latency"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}

	// The null p50 on the second point is a dash, not a zero.
	if !strings.Contains(out, "-") {
		t.Errorf("a null field was not printed as a dash:\n%s", out)
	}

	calls := f.find("GET", "/projects/11111111-1111-4111-8111-111111111111/services/22222222-2222-4222-8222-222222222222/metrics/series")
	if len(calls) != 1 {
		t.Fatalf("series calls = %d", len(calls))
	}
	if got := calls[0].Query; !strings.Contains(got, "range=1h") {
		t.Fatalf("range = %q", got)
	}
}

func TestServiceMetricsWithNoTrafficSaysSo(t *testing.T) {
	routes := metricsRoutes()
	routes["GET /projects/11111111-1111-4111-8111-111111111111/services/22222222-2222-4222-8222-222222222222/metrics/series"] =
		`{"data":{"series":[],"available":[],"source":"service_metrics"}}`
	fakeRoutes(t, routes)

	out, err := run(t, "service", "metrics", "shop", "API")
	if err != nil {
		t.Fatalf("service metrics: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no traffic") {
		t.Fatalf("out = %s", out)
	}
}
