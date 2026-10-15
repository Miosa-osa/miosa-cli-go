package commands_test

import (
	"strings"
	"testing"
)

// 'miosa deploy get' says what the deployment is running: the kind lives on
// the active version, not on the deployment.
func deployGetAPI(t *testing.T, versionReply string) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/deployments/d3"):
			return 200, `{"data":{"id":"d3","name":"my-site","state":"running","public_url":"https://d3.miosa.app",` +
				`"source_type":"upload","branch":"main","active_version_id":"v1"}}`
		case c.is("GET", "/deployments/d3/versions/v1"):
			return 200, versionReply
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestDeployGetShowsTheKind(t *testing.T) {
	deployGetAPI(t, `{"data":{"id":"v1","kind":"static","version_number":1}}`)

	out, err := run(t, "deploy", "get", "d3")
	if err != nil {
		t.Fatalf("deploy get: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "Kind") || !strings.Contains(out, "static") {
		t.Fatalf("the kind should be shown: %q", out)
	}
}

func TestDeployGetShowsTheRuntimeForADynamicDeployment(t *testing.T) {
	deployGetAPI(t, `{"data":{"id":"v1","kind":"dynamic","runtime_image":"miosa/node:22",`+
		`"runtime_command":"npm start","runtime_port":3000}}`)

	out, err := run(t, "deploy", "get", "d3")
	if err != nil {
		t.Fatalf("deploy get: %v (out=%q)", err, out)
	}
	for _, want := range []string{"dynamic", "miosa/node:22", "npm start", "3000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from %q", want, out)
		}
	}
}

// A version we cannot read leaves the deployment answer intact.
func TestDeployGetSurvivesAMissingVersion(t *testing.T) {
	deployGetAPI(t, `{"error":{"code":"NOT_FOUND"}}`)

	out, err := run(t, "deploy", "get", "d3")
	if err != nil {
		t.Fatalf("deploy get: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "https://d3.miosa.app") {
		t.Fatalf("the deployment should still be shown: %q", out)
	}
	if strings.Contains(out, "Kind") {
		t.Fatalf("no kind is known for a version we cannot read: %q", out)
	}
}
