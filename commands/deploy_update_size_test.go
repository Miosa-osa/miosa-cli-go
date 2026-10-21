package commands_test

import (
	"strings"
	"testing"
)

func TestDeployUpdateSizeSendsOnlySizeAndSaysWhenItApplies(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"PATCH /deployments/my-app": `{"data":{"id":"d1","name":"my-app","size":{"size":"small","memory_mb":4096,"cpu_millicores":2000,"services_updated":1,"applies":"next release or restart"}}}`,
	})
	out, err := run(t, "deploy", "update", "my-app", "--size", "Small")
	if err != nil {
		t.Fatalf("update --size: %v\n%s", err, out)
	}
	calls := f.find("PATCH", "/deployments/my-app")
	if len(calls) != 1 || calls[0].Body["size"] != "small" || len(calls[0].Body) != 1 {
		t.Fatalf("only the size (lower-cased) may be sent: %+v", calls)
	}
	for _, want := range []string{"my-app", "small", "4096", "next release or restart"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must show %q:\n%s", want, out)
		}
	}
}

func TestDeployUpdateSizeRefusesAnUnknownSizeBeforeAnyRequest(t *testing.T) {
	f := fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "update", "my-app", "--size", "large")
	if err == nil || !strings.Contains(err.Error(), "small or medium") {
		t.Fatalf("large must be refused with the allowed sizes: %v", err)
	}
	if len(f.find("PATCH", "/deployments/my-app")) != 0 {
		t.Fatal("nothing may be sent for a refused size")
	}
}

func TestDeployUpdateWithNoFlagsNamesSizeAmongTheOptions(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "update", "my-app")
	if err == nil || !strings.Contains(err.Error(), "--size") {
		t.Fatalf("the nothing-to-change error must list --size: %v", err)
	}
}
