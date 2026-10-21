package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPostsTheBucketObjectToTheTable(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /storage/buckets": `{"data":[{"id":"b1","name":"exports"}]}`,
		"GET /databases":       `{"data":[{"id":"d1","name":"app-db"}]}`,
		"POST /data-imports": `{"data":{"id":"j1","rows_total":3,"rows_loaded":2,"rejected_total":1,` +
			`"errors":{"rows":[{"line":4,"reason":"invalid input syntax for type bigint"}]}}}`,
	})

	out, err := run(t, "import", "--from", "bucket:exports/people.csv", "--to", "db:app-db/people", "--override", "zip=text")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "2 loaded") || !strings.Contains(out, "1 refused") || !strings.Contains(out, "line 4") {
		t.Fatalf("output = %s", out)
	}

	body := f.find("POST", "/data-imports")[0].Body
	if body["bucket_id"] != "b1" || body["database_id"] != "d1" || body["table"] != "people" {
		t.Fatalf("body = %v", body)
	}
	if body["key"] != "people.csv" {
		t.Fatalf("key = %v", body["key"])
	}
	overrides, _ := body["overrides"].(map[string]any)
	if overrides["zip"] != "text" {
		t.Fatalf("overrides = %v", body["overrides"])
	}
}

func TestImportRefusesAVagueTarget(t *testing.T) {
	if _, err := run(t, "import", "--from", "exports/people.csv", "--to", "db:app-db/people"); err == nil || !strings.Contains(err.Error(), "bucket:") {
		t.Fatalf("err = %v", err)
	}
	if _, err := run(t, "import", "--from", "bucket:exports/people.csv", "--to", "people"); err == nil || !strings.Contains(err.Error(), "db:") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportNamesAnUnknownDatabase(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /storage/buckets": `{"data":[{"id":"b1","name":"exports"}]}`,
		"GET /databases":       `{"data":[{"id":"d1","name":"app-db"}]}`,
	})
	_, err := run(t, "import", "--from", "bucket:exports/people.csv", "--to", "db:nope/people")
	if err == nil || !strings.Contains(err.Error(), "no database named") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportSendsANotionZipToTheWorkspacesEngine(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /storage/buckets":                      `{"data":[{"id":"b1","name":"exports"}]}`,
		"GET /workspaces":                           `{"data":[{"id":"w1","slug":"research","name":"Research"}]}`,
		"POST /workspaces/w1/engine/imports/notion": `{"data":{"job_id":"imp_1","status":"queued","duplicate":false}}`,
	})

	out, err := run(t, "import", "--from", "bucket:exports/notion.zip", "--to", "engine:research")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "imp_1") {
		t.Fatalf("output = %s", out)
	}

	calls := f.find("POST", "/workspaces/w1/engine/imports/notion")
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].Body["bucket_id"] != "b1" || calls[0].Body["key"] != "notion.zip" {
		t.Fatalf("body = %v", calls[0].Body)
	}
}

func TestImportRefusesAJobWithoutAnEngineTarget(t *testing.T) {
	if _, err := run(t, "import", "--job", "imp_1", "--to", "db:app-db/people"); err == nil ||
		!strings.Contains(err.Error(), "engine:") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportStagesALocalFileInTheImportsBucketThenImportsIt(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(file, []byte("name,age\nAda,36\nGrace,45\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var uploaded []string

	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.Method == "GET" && c.Path == "/storage/buckets":
			return 200, `{"data":[]}`
		case c.Method == "POST" && c.Path == "/storage/buckets":
			if c.Body["name"] != "imports" {
				return 500, `{"error":{"code":"TEST_BUG"}}`
			}
			return 201, `{"data":{"id":"b-new","name":"imports"}}`
		case c.Method == "PUT" && strings.HasPrefix(c.Path, "/storage/buckets/b-new/objects/staged/"):
			uploaded = append(uploaded, c.Path)
			return 200, `{"ok":true}`
		case c.Method == "GET" && c.Path == "/databases":
			return 200, `{"data":[{"id":"d1","name":"app-db"}]}`
		case c.Method == "POST" && c.Path == "/data-imports":
			return 201, `{"data":{"id":"j2","rows_total":2,"rows_loaded":2,"rejected_total":0}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND"}}`
	})

	out, err := run(t, "import", file, "--to", "db:app-db/people")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if len(uploaded) != 1 {
		t.Fatalf("uploads = %v", uploaded)
	}
	if !strings.HasSuffix(uploaded[0], "/people.csv") || !strings.Contains(uploaded[0], "/staged/") {
		t.Fatalf("staged key = %s", uploaded[0])
	}

	body := f.find("POST", "/data-imports")[0].Body
	if body["bucket_id"] != "b-new" || body["database_id"] != "d1" || body["table"] != "people" {
		t.Fatalf("body = %v", body)
	}
	if key, _ := body["key"].(string); !strings.HasPrefix(key, "staged/") {
		t.Fatalf("key = %v", body["key"])
	}
}

func TestImportRefusesTwoSourcesAtOnce(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(file, []byte("a\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "import", file, "--from", "bucket:exports/people.csv", "--to", "db:app-db/people"); err == nil ||
		!strings.Contains(err.Error(), "name the source once") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportForwardsTheCallersIdempotencyKey(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /storage/buckets":                      `{"data":[{"id":"b1","name":"exports"}]}`,
		"GET /workspaces":                           `{"data":[{"id":"w1","slug":"research"}]}`,
		"POST /workspaces/w1/engine/imports/notion": `{"data":{"job_id":"imp_1","status":"queued"}}`,
	})

	if _, err := run(t, "import", "--from", "bucket:exports/notion.zip", "--to", "engine:research",
		"--idempotency-key", "abc-123"); err != nil {
		t.Fatal(err)
	}

	calls := f.find("POST", "/workspaces/w1/engine/imports/notion")
	if got := calls[0].Headers.Get("Idempotency-Key"); got != "abc-123" {
		t.Fatalf("Idempotency-Key = %q", got)
	}
}

func TestImportGeneratesAnIdempotencyKeyWhenNoneIsGiven(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /storage/buckets":                      `{"data":[{"id":"b1","name":"exports"}]}`,
		"GET /workspaces":                           `{"data":[{"id":"w1","slug":"research"}]}`,
		"POST /workspaces/w1/engine/imports/notion": `{"data":{"job_id":"imp_1","status":"queued"}}`,
	})

	if _, err := run(t, "import", "--from", "bucket:exports/notion.zip", "--to", "engine:research"); err != nil {
		t.Fatal(err)
	}

	got := f.find("POST", "/workspaces/w1/engine/imports/notion")[0].Headers.Get("Idempotency-Key")
	if len(got) != 36 || strings.Count(got, "-") != 4 {
		t.Fatalf("Idempotency-Key = %q, want a UUID", got)
	}
}
