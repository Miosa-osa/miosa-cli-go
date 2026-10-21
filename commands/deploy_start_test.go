package commands_test

import (
	"strings"
	"testing"
)

func TestDeployStartPostsToStartAndSaysSo(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /deployments/my-site/start": `{"data":{"id":"d1","name":"my-site","state":"building"}}`,
	})
	out, err := run(t, "deploy", "start", "my-site")
	if err != nil {
		t.Fatalf("deploy start: %v\n%s", err, out)
	}
	calls := f.find("POST", "/deployments/my-site/start")
	if len(calls) != 1 {
		t.Fatalf("exactly one POST /start expected: %+v", f)
	}
	if !strings.Contains(out, "Starting my-site") {
		t.Errorf("output must say it is starting:\n%s", out)
	}
}

func TestDeployStartNeedsADeployment(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	if _, err := run(t, "deploy", "start"); err == nil {
		t.Fatal("deploy start without a deployment must fail")
	}
}
