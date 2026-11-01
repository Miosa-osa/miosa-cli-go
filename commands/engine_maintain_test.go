package commands_test

import (
	"net/http"
	"strings"
	"testing"
)

const maintainEngineJS = `{"data":[{"id":"` + engineID + `","name":"qa-brain","engine":"optimal","state":"running"}]}`

func maintainRoutes(body string) map[string]any {
	return map[string]any{
		"GET /databases": maintainEngineJS,
		"POST /databases/" + engineID + "/maintenance": resp{http.StatusOK, body},
	}
}

func TestEngineMaintainEmbedBackfillSendsTheTaskAndLimit(t *testing.T) {
	f := fakeRoutes(t, maintainRoutes(`{"status":"ok","embedded":200}`))

	out, err := run(t, "engine", "maintain", "qa-brain", "embed_backfill", "--limit", "200")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	posts := f.find("POST", "/databases/"+engineID+"/maintenance")
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}
	body := posts[0].Body
	if body["task"] != "embed_backfill" || body["limit"] != float64(200) {
		t.Fatalf("body = %v", body)
	}
	if !strings.Contains(out, "embed_backfill accepted") {
		t.Fatalf("output = %s", out)
	}
}

func TestEngineMaintainReindexCarriesTheEngineWorkspace(t *testing.T) {
	f := fakeRoutes(t, maintainRoutes(`{"status":"ok"}`))

	if _, err := run(t, "engine", "maintain", "qa-brain", "reindex", "--workspace", "default:team-a"); err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", "/databases/"+engineID+"/maintenance")[0].Body
	if body["task"] != "reindex" || body["workspace"] != "default:team-a" {
		t.Fatalf("body = %v", body)
	}
}

func TestEngineMaintainCorpusOrganizationNeedsExactlyOneMode(t *testing.T) {
	f := fakeRoutes(t, maintainRoutes(`{"status":"ok"}`))

	if _, err := run(t, "engine", "maintain", "qa-brain", "corpus_organization", "--deduplicate-exact"); err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", "/databases/"+engineID+"/maintenance")[0].Body
	if body["task"] != "corpus_organization" || body["deduplicate_exact"] != true {
		t.Fatalf("body = %v", body)
	}

	_, err := run(t, "engine", "maintain", "qa-brain", "corpus_organization")
	if err == nil || !strings.Contains(err.Error(), "exactly one of") {
		t.Fatalf("err = %v, want the mode requirement", err)
	}

	_, err = run(t, "engine", "maintain", "qa-brain", "corpus_organization", "--apply", "--queue-remaining")
	if err == nil || !strings.Contains(err.Error(), "exactly one of") {
		t.Fatalf("err = %v, want a refusal for two modes", err)
	}
}

func TestEngineMaintainRefusesAnUnknownTask(t *testing.T) {
	f := fakeRoutes(t, maintainRoutes(`{"status":"ok"}`))

	_, err := run(t, "engine", "maintain", "qa-brain", "vacuum")
	if err == nil || !strings.Contains(err.Error(), "embed_backfill, corpus_organization, reindex") {
		t.Fatalf("err = %v, want the allowed tasks", err)
	}
	if len(f.find("POST", "/databases/"+engineID+"/maintenance")) != 0 {
		t.Fatalf("nothing may be sent for an unknown task")
	}
}

func TestEngineMaintainRefusesAFlagTheTaskDoesNotUse(t *testing.T) {
	f := fakeRoutes(t, maintainRoutes(`{"status":"ok"}`))

	_, err := run(t, "engine", "maintain", "qa-brain", "reindex", "--limit", "10")
	if err == nil || !strings.Contains(err.Error(), "no --limit") {
		t.Fatalf("err = %v, want a refusal naming --limit", err)
	}
	if len(f.find("POST", "/databases/"+engineID+"/maintenance")) != 0 {
		t.Fatalf("nothing may be sent with a flag the task does not use")
	}
}
