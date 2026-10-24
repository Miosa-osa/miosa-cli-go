package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestTelemetrySetShowsTheTestAndNeverTheHeaderValues(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"PUT /settings/telemetry":    `{"data":{"configured":true,"endpoint":"https://otlp.datadoghq.com","header_names":["dd-api-key"],"last_status":"ok","test":{"ok":true,"error":null}}}`,
		"GET /settings/telemetry":    `{"data":{"configured":true,"endpoint":"https://otlp.datadoghq.com","header_names":["dd-api-key"],"last_export_at":"2026-10-24T01:00:00Z","last_status":"error","last_error":"the endpoint answered HTTP 403 to /v1/metrics","consecutive_failures":3}}`,
		"DELETE /settings/telemetry": `{"data":{"configured":false,"removed":true}}`,
	})

	out, err := run(t, "telemetry", "set", "https://otlp.datadoghq.com", "-H", "dd-api-key=sk-123", "-H", "x-extra=a=b")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("PUT", "/settings/telemetry")[0].Body
	hdrs, _ := b["headers"].(map[string]any)
	if b["endpoint"] != "https://otlp.datadoghq.com" || hdrs["dd-api-key"] != "sk-123" || hdrs["x-extra"] != "a=b" {
		t.Fatalf("body = %v", b)
	}
	if strings.Contains(out, "sk-123") || !strings.Contains(out, "dd-api-key") || !strings.Contains(out, "Test export") {
		t.Fatalf("set output:\n%s", out)
	}

	out, err = run(t, "telemetry")
	if err != nil || !strings.Contains(out, "error: the endpoint answered HTTP 403 to /v1/metrics (3 in a row)") {
		t.Fatalf("show: %v\n%s", err, out)
	}

	if out, err := run(t, "telemetry", "unset"); err != nil || !strings.Contains(out, "removed") {
		t.Fatalf("unset: %v\n%s", err, out)
	}
}

func TestTelemetrySetExitsNonZeroWhenTheTestFails(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"PUT /settings/telemetry": `{"data":{"configured":true,"endpoint":"https://otel.example.com","header_names":[],"test":{"ok":false,"error":"the endpoint answered HTTP 401 to /v1/metrics"}}}`,
	})
	out, err := run(t, "telemetry", "set", "https://otel.example.com")
	if err == nil || !strings.Contains(err.Error()+out, "HTTP 401") {
		t.Fatalf("a failed test export must fail the command: %v\n%s", err, out)
	}
}

func TestTelemetrySetRefusesBadInput(t *testing.T) {
	f := fakeRoutes(t, map[string]any{})
	for _, args := range [][]string{
		{"telemetry", "set", "http://otel.example.com"},
		{"telemetry", "set", "https://otel.example.com", "-H", "novalue"},
	} {
		if _, err := run(t, args...); commands.ExitCode(err) != commands.ExitUsage {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if f.count() != 0 {
		t.Fatal("bad input must not reach the API")
	}
}
