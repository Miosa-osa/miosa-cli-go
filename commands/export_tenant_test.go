package commands_test

import (
	"strings"
	"testing"
)

// miosa export takes an organization out: one file per collection plus a
// manifest with checksums, written into a bucket of the same organization.
func TestExportWritesManifestIntoANamedBucket(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /storage/buckets": `{"data":[{"id":"b1","name":"exports"}]}`,
		"POST /exports": `{"data":{"prefix":"exports/x/","manifest_key":"exports/x/manifest.json",` +
			`"manifest_sha256":"abc123","rows":3,"files":[{"path":"agents.ndjson","key":"exports/x/agents.ndjson",` +
			`"sha256":"def456","bytes":42,"rows":3}]}}`,
	})
	out, err := run(t, "export", "--to", "bucket:exports")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "exports/x/manifest.json") || !strings.Contains(out, "agents.ndjson") {
		t.Fatalf("output = %s", out)
	}
	body := f.find("POST", "/exports")[0].Body
	if body["bucket_id"] != "b1" {
		t.Fatalf("bucket_id = %v", body["bucket_id"])
	}
	if _, ok := body["workspace_id"]; ok {
		t.Fatalf("an organization export must not name a workspace: %v", body)
	}
}

func TestExportNarrowsToOneWorkspace(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /storage/buckets": `{"data":[{"id":"b1","name":"exports"}]}`,
		"GET /workspaces":      `{"data":[{"id":"w1","slug":"prod","name":"Prod"}]}`,
		"POST /exports":        `{"data":{"manifest_key":"exports/x/manifest.json","files":[],"rows":0}}`,
	})
	if _, err := run(t, "export", "--to", "bucket:exports", "--workspace", "prod"); err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", "/exports")[0].Body
	if body["workspace_id"] != "w1" {
		t.Fatalf("workspace_id = %v", body["workspace_id"])
	}
}

func TestExportRefusesATargetThatIsNotABucket(t *testing.T) {
	if _, err := run(t, "export", "--to", "db:people"); err == nil || !strings.Contains(err.Error(), "bucket:") {
		t.Fatalf("err = %v", err)
	}
}

func TestExportNamesAnUnknownBucket(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /storage/buckets": `{"data":[{"id":"b1","name":"exports"}]}`})
	_, err := run(t, "export", "--to", "bucket:nope")
	if err == nil || !strings.Contains(err.Error(), "no bucket named") {
		t.Fatalf("err = %v", err)
	}
}
