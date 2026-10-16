package commands_test

import (
	"strings"
	"testing"
)

// Commands with no columns of their own printed raw JSON without --json
// (forge check list, check evidence, release tag, policy owners, ...).
func TestCommandsWithoutColumnsPrintReadableText(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /forge/repositories/platform/check-suites":           `{"data":[{"id":"cs-1","head_sha":"abc123","status":"completed","conclusion":"passed","runs":[{"id":"r"}],"created_at":"2026-10-16T10:00:00Z"}]}`,
		"GET /forge/repositories/platform/check-runs/r1/evidence": `{"data":{"check_run_id":"r1","evidence":{"tests":{"passed":12,"failed":0}}}}`,
	})
	out, err := run(t, "forge", "check", "list", "platform")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "{") || !strings.Contains(out, "CONCLUSION") || !strings.Contains(out, "passed") {
		t.Fatalf("check list is not a table:\n%s", out)
	}
	out, err = run(t, "forge", "check", "evidence", "platform", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "{") || !strings.Contains(out, "Evidence.tests.passed") || !strings.Contains(out, "12") {
		t.Fatalf("check evidence is not readable:\n%s", out)
	}
	jout, err := run(t, "forge", "check", "list", "platform", "--json")
	if err != nil || !strings.Contains(jout, `"head_sha"`) {
		t.Fatalf("--json must stay JSON: %v\n%s", err, jout)
	}
}

func TestDeployCreateAcceptsYes(t *testing.T) {
	fakeRoutes(t, nil)
	_, err := run(t, "deploy", "create", "--yes", "--help")
	if err != nil {
		t.Fatalf("deploy create --yes: %v", err)
	}
}
