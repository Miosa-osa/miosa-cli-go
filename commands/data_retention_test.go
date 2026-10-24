package commands_test

import (
	"strings"
	"testing"
)

func TestDataRetentionStatus(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /data-retention": `{"data":{"mode":"keep","protected":false}}`})
	out, err := run(t, "data-retention", "status")
	if err != nil || !strings.Contains(out, "keep") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDataRetentionEnableNeedsYes(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"PUT /data-retention": `{"data":{"mode":"delete_on_stop","protected":false}}`})

	if out, err := run(t, "data-retention", "enable"); err == nil || len(f.find("PUT", "/data-retention")) != 0 {
		t.Fatalf("enable without --yes must not call the API: %v\n%s", err, out)
	}

	out, err := run(t, "data-retention", "enable", "--yes")
	if err != nil || !strings.Contains(out, "delete_on_stop") {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("PUT", "/data-retention")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	body := calls[0].Body
	if body["mode"] != "delete_on_stop" || body["confirm"] != true {
		t.Fatalf("body = %v", body)
	}
}

func TestDataRetentionDisableSendsKeep(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"PUT /data-retention": `{"data":{"mode":"keep","protected":false}}`})
	if out, err := run(t, "data-retention", "disable"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	body := f.find("PUT", "/data-retention")[0].Body
	if body["mode"] != "keep" {
		t.Fatalf("body = %v", body)
	}
}
