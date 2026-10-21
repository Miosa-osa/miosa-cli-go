package commands_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const driftWorkspaces = `{"data":[
 {"id":"11111111-1111-4111-8111-111111111111","slug":"clips","name":"Clips"},
 {"id":"22222222-2222-4222-8222-222222222222","slug":"other","name":"Other"},
 {"id":"33333333-3333-4333-8333-333333333333","slug":"stray","name":"Stray"}]}`

// driftReply answers the three endpoints the diff reads, keyed by the
// workspace the query names.
func driftReply(c call) (int, string) {
	switch {
	case c.is("GET", "/workspaces"):
		return http.StatusOK, driftWorkspaces

	case c.is("GET", "/projects") && strings.Contains(c.Query, "11111111-1111-4111-8111-111111111111"):
		return http.StatusOK, `{"data":[{"slug":"site"},{"slug":"extra"}]}`

	case c.is("GET", "/projects"):
		return http.StatusOK, `{"data":[]}`

	case c.is("GET", "/sandboxes") && strings.Contains(c.Query, "11111111-1111-4111-8111-111111111111"):
		return http.StatusOK, `{"data":[
		 {"id":"s1","name":"build-1","tags":{}},
		 {"id":"s2","name":"build-2","tags":{"purpose":""}},
		 {"id":"s3","name":"api-1","tags":{"purpose":"production"}}]}`

	case c.is("GET", "/sandboxes"):
		return http.StatusOK, `{"data":[]}`
	}
	return http.StatusNotFound, `{"error":{"code":"NOT_FOUND","message":"no route in test"}}`
}

func writeTopology(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "topology.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOrgDiffReportsBothDirectionsAndUntagged(t *testing.T) {
	newFakeAPI(t, driftReply)

	path := writeTopology(t, `schema_version: 1
miosa_org: osa
keep_workspaces: [default]
entities:
  acme:
    workspaces:
      clips:
        projects: [site, api]
      gone-ws:
        projects: [legacy]
`)

	out, err := run(t, "org", "diff", "--file", path, "--json")
	if err == nil {
		t.Fatalf("want a drift error, got none:\n%s", out)
	}
	if !strings.Contains(err.Error(), "drift detected") {
		t.Fatalf("error = %v, want drift detected", err)
	}

	var envelope struct {
		Data struct {
			UndeclaredWorkspaces []string `json:"undeclared_workspaces"`
			MissingWorkspaces    []string `json:"missing_workspaces"`
			UndeclaredProjects   []string `json:"undeclared_projects"`
			MissingProjects      []string `json:"missing_projects"`
			UntaggedResources    []struct {
				Workspace string `json:"workspace"`
				Kind      string `json:"kind"`
				Count     int    `json:"count"`
			} `json:"untagged_resources"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out)
	}

	report := envelope.Data
	if got := strings.Join(report.UndeclaredWorkspaces, ","); got != "other,stray" {
		t.Fatalf("undeclared workspaces = %q", got)
	}
	if got := strings.Join(report.MissingWorkspaces, ","); got != "gone-ws" {
		t.Fatalf("missing workspaces = %q", got)
	}
	if got := strings.Join(report.UndeclaredProjects, ","); got != "clips/extra" {
		t.Fatalf("undeclared projects = %q", got)
	}
	if got := strings.Join(report.MissingProjects, ","); got != "clips/api" {
		t.Fatalf("missing projects = %q", got)
	}
	if len(report.UntaggedResources) != 1 ||
		report.UntaggedResources[0].Workspace != "clips" ||
		report.UntaggedResources[0].Count != 2 {
		t.Fatalf("untagged = %+v", report.UntaggedResources)
	}
}

func TestOrgDiffCleanTopologyExitsZero(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/workspaces"):
			return http.StatusOK, `{"data":[{"id":"11111111-1111-4111-8111-111111111111","slug":"clips"}]}`
		case c.is("GET", "/projects"):
			return http.StatusOK, `{"data":[{"slug":"site"}]}`
		case c.is("GET", "/sandboxes"):
			return http.StatusOK, `{"data":[{"id":"s1","tags":{"purpose":"production"}}]}`
		}
		return http.StatusNotFound, `{}`
	})

	path := writeTopology(t, `schema_version: 1
entities:
  acme:
    workspaces:
      clips:
        projects: [site]
`)

	out, err := run(t, "org", "diff", "--file", path, "--tags=false")
	if err != nil {
		t.Fatalf("clean topology must exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No drift") {
		t.Fatalf("output = %q, want No drift", out)
	}
}

func TestOrgDiffRejectsAnUnknownSchema(t *testing.T) {
	newFakeAPI(t, driftReply)
	path := writeTopology(t, "schema_version: 9\nentities: {}\n")

	out, err := run(t, "org", "diff", "--file", path)
	if err == nil {
		t.Fatalf("want a usage error for schema_version 9:\n%s", out)
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error = %v", err)
	}
}
