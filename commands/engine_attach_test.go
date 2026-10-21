package commands_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	attachWS      = "0190a1b2-0000-7000-8000-000000000001"
	attachWSOther = "0190a1b2-0000-7000-8000-000000000002"
	attachEngA    = "0190a1b2-0000-7000-8000-0000000000aa"
	attachEngB    = "0190a1b2-0000-7000-8000-0000000000bb"
)

func engineJSON(id, name, scope string) string {
	metadata := `{}`
	if scope != "" {
		metadata = `{"scope":"` + scope + `"}`
	}
	return `{"id":"` + id + `","name":"` + name + `","engine":"optimal","engine_version":"0.3",` +
		`"state":"running","host":"10.0.0.9","port":4200,"workspace_id":"` + attachWS + `",` +
		`"metadata":` + metadata + `}`
}

func attachmentBody(workspaceID, databaseID, scope string) string {
	return `{"workspace_id":"` + workspaceID + `","database_id":"` + databaseID + `",` +
		`"scope":"` + scope + `","status":"active",` +
		`"engine_workspace_id":"default:kii","engine_workspace_slug":"kii",` +
		`"key_id":"key_1","key_minted_at":"2026-10-19T12:00:00Z",` +
		`"engine":{"id":"` + databaseID + `","name":"brain","state":"running","scope":"` + scope + `","url":"http://10.0.0.9:4200"}}`
}

func attachmentEnvelope(workspaceID, databaseID, scope string) string {
	return `{"data":` + attachmentBody(workspaceID, databaseID, scope) + `}`
}

func workspacesRoute() string {
	return `{"data":[{"id":"` + attachWS + `","name":"kii","slug":"kii"},` +
		`{"id":"` + attachWSOther + `","name":"peak","slug":"peak"}]}`
}

func attachRoutes(engines []string, workspaceID, databaseID, scope string) map[string]any {
	list := `{"data":[` + strings.Join(engines, ",") + `]}`
	envelope := attachmentEnvelope(workspaceID, databaseID, scope)
	return map[string]any{
		"GET /workspaces": workspacesRoute(),
		"GET /databases":  list,
		"POST /databases/" + attachEngA + "/attachments":                  envelope,
		"POST /databases/" + attachEngB + "/attachments":                  envelope,
		"GET /workspaces/" + workspaceID + "/engine":                      envelope,
		"DELETE /databases/" + databaseID + "/attachments/" + workspaceID: envelope,
		"GET /databases/" + attachEngA + "/attachments":                   `{"data":[` + attachmentBody(workspaceID, attachEngA, scope) + `]}`,
		"GET /databases/" + attachEngB + "/attachments":                   `{"data":[]}`,
	}
}

func decodeEnvelope(t *testing.T, out string) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("output has no data object: %q", out)
	}
	return data
}

func TestEngineAttachToOrgDeclaresTheOnlyEngine(t *testing.T) {
	f := fakeRoutes(t, attachRoutes(
		[]string{engineJSON(attachEngA, "brain", "")}, attachWS, attachEngA, "org"))

	out, err := run(t, "engine", "attach", "kii", "--to", "org")
	if err != nil {
		t.Fatalf("attach: %v (%s)", err, out)
	}
	if !strings.Contains(out, "organization's Engine") {
		t.Fatalf("output should say the Engine became the organization's: %q", out)
	}

	calls := f.find("POST", "/databases/"+attachEngA+"/attachments")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	if calls[0].Body["workspace_id"] != attachWS || calls[0].Body["declare_org"] != true {
		t.Fatalf("body = %+v", calls[0].Body)
	}
}

func TestEngineAttachToNamedEngine(t *testing.T) {
	f := fakeRoutes(t, attachRoutes(
		[]string{engineJSON(attachEngA, "brain", ""), engineJSON(attachEngB, "other", "")},
		attachWS, attachEngB, "workspace"))

	if _, err := run(t, "engine", "attach", "kii", "--to", "other"); err != nil {
		t.Fatal(err)
	}
	calls := f.find("POST", "/databases/"+attachEngB+"/attachments")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	if _, declared := calls[0].Body["declare_org"]; declared {
		t.Fatalf("a named Engine must not be declared org: %+v", calls[0].Body)
	}
}

func TestEngineAttachToOrgUsesTheDeclaredEngine(t *testing.T) {
	f := fakeRoutes(t, attachRoutes(
		[]string{engineJSON(attachEngA, "brain", ""), engineJSON(attachEngB, "org-brain", "org")},
		attachWS, attachEngB, "org"))

	if _, err := run(t, "engine", "attach", "kii", "--to", "org"); err != nil {
		t.Fatal(err)
	}
	if calls := f.find("POST", "/databases/"+attachEngB+"/attachments"); len(calls) != 1 {
		t.Fatalf("the declared org Engine must be used: %+v", f.calls)
	}
	if calls := f.find("POST", "/databases/"+attachEngA+"/attachments"); len(calls) != 0 {
		t.Fatalf("the undeclared Engine must not be used: %+v", f.calls)
	}
}

func TestEngineAttachRejectsUnknownEngine(t *testing.T) {
	fakeRoutes(t, attachRoutes([]string{engineJSON(attachEngA, "brain", "")}, attachWS, attachEngA, "org"))

	_, err := run(t, "engine", "attach", "kii", "--to", "nope")
	if err == nil || !strings.Contains(err.Error(), "no managed Optimal Engine named") {
		t.Fatalf("err = %v", err)
	}
}

func TestEngineAttachRequiresTo(t *testing.T) {
	fakeRoutes(t, attachRoutes(nil, attachWS, attachEngA, "org"))

	if _, err := run(t, "engine", "attach", "kii"); err == nil {
		t.Fatal("--to must be required")
	}
}

func TestEngineDetachRevokesThroughTheServingEngine(t *testing.T) {
	f := fakeRoutes(t, attachRoutes(
		[]string{engineJSON(attachEngA, "brain", "org")}, attachWS, attachEngA, "org"))

	out, err := run(t, "engine", "detach", "kii", "--force")
	if err != nil {
		t.Fatalf("detach: %v (%s)", err, out)
	}
	calls := f.find("DELETE", "/databases/"+attachEngA+"/attachments/"+attachWS)
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	if !strings.Contains(calls[0].Query, "force=true") {
		t.Fatalf("query = %q", calls[0].Query)
	}
}

func TestEngineDetachWhenNotAttached(t *testing.T) {
	routes := attachRoutes([]string{engineJSON(attachEngA, "brain", "org")}, attachWS, attachEngA, "org")
	routes["GET /workspaces/"+attachWS+"/engine"] = resp{404, `{"error":{"code":"NOT_FOUND"}}`}
	fakeRoutes(t, routes)

	_, err := run(t, "engine", "detach", "kii")
	if err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("err = %v", err)
	}
}

func TestEngineStatusForOneWorkspace(t *testing.T) {
	fakeRoutes(t, attachRoutes(
		[]string{engineJSON(attachEngA, "brain", "org")}, attachWS, attachEngA, "org"))

	out, err := run(t, "engine", "status", "kii", "--json")
	if err != nil {
		t.Fatalf("status: %v (%s)", err, out)
	}
	data := decodeEnvelope(t, out)
	if data["scope"] != "org" || data["engine_workspace_slug"] != "kii" || data["key_id"] != "key_1" {
		t.Fatalf("data = %+v", data)
	}
	if strings.Contains(out, "oe_") {
		t.Fatalf("a key must never be printed: %q", out)
	}
}

func TestEngineStatusNotAttachedExitsZero(t *testing.T) {
	routes := attachRoutes([]string{engineJSON(attachEngA, "brain", "")}, attachWS, attachEngA, "org")
	routes["GET /workspaces/"+attachWS+"/engine"] = resp{404, `{"error":{"code":"NOT_FOUND"}}`}
	fakeRoutes(t, routes)

	out, err := run(t, "engine", "status", "kii", "--json")
	if err != nil {
		t.Fatalf("status must exit 0 when not attached: %v", err)
	}
	data := decodeEnvelope(t, out)
	if data["attached"] != false || data["workspace_id"] != attachWS {
		t.Fatalf("data = %+v", data)
	}
}

func TestEngineStatusListsEveryEngineAndItsWorkspaces(t *testing.T) {
	fakeRoutes(t, attachRoutes(
		[]string{engineJSON(attachEngA, "brain", "org"), engineJSON(attachEngB, "other", "")},
		attachWS, attachEngA, "org"))

	out, err := run(t, "engine", "status")
	if err != nil {
		t.Fatalf("status: %v (%s)", err, out)
	}
	for _, want := range []string{"brain", "other", "org", "kii"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
}

func TestEngineAttachNamesTheEngineItIsAlreadyAttachedTo(t *testing.T) {
	routes := attachRoutes([]string{engineJSON(attachEngA, "brain", ""), engineJSON(attachEngB, "other", "")}, attachWS, attachEngB, "workspace")
	routes["POST /databases/"+attachEngB+"/attachments"] = resp{409, `{"error":{"code":"ALREADY_ATTACHED","engine_name":"brain","engine_id":"` + attachEngA + `","workspace_id":"` + attachWS + `","message":"this workspace is already attached to another Engine; pass replace: true (owners and admins only) to move it"}}`}
	fakeRoutes(t, routes)

	_, err := run(t, "engine", "attach", "kii", "--to", "other")
	if err == nil {
		t.Fatal("a 409 must fail the command")
	}
	if !strings.Contains(err.Error(), `Engine "brain"`) || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("the refusal must name the Engine and the flag: %v", err)
	}
}

func TestEngineAttachFallsBackToTheEngineIDWhenTheNameIsNull(t *testing.T) {
	routes := attachRoutes([]string{engineJSON(attachEngA, "brain", ""), engineJSON(attachEngB, "other", "")}, attachWS, attachEngB, "workspace")
	routes["POST /databases/"+attachEngB+"/attachments"] = resp{409, `{"error":{"code":"ALREADY_ATTACHED","engine_name":null,"engine_id":"` + attachEngA + `","workspace_id":"` + attachWS + `","message":"this workspace is already attached to another Engine; pass replace: true (owners and admins only) to move it"}}`}
	fakeRoutes(t, routes)

	_, err := run(t, "engine", "attach", "kii", "--to", "other")
	if err == nil {
		t.Fatal("a 409 must fail the command")
	}
	if !strings.Contains(err.Error(), attachEngA) || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("the refusal must name the Engine id and the flag: %v", err)
	}
}

func TestEngineAttachReplaceSendsTheFlag(t *testing.T) {
	routes := attachRoutes([]string{engineJSON(attachEngA, "brain", ""), engineJSON(attachEngB, "other", "")}, attachWS, attachEngB, "workspace")
	f := fakeRoutes(t, routes)

	if _, err := run(t, "engine", "attach", "kii", "--to", "other", "--replace"); err != nil {
		t.Fatal(err)
	}
	calls := f.find("POST", "/databases/"+attachEngB+"/attachments")
	if len(calls) != 1 || calls[0].Body["replace"] != true {
		t.Fatalf("body = %+v", calls)
	}
}

func TestEngineAttachWithoutReplaceSendsNoReplaceField(t *testing.T) {
	routes := attachRoutes([]string{engineJSON(attachEngA, "brain", ""), engineJSON(attachEngB, "other", "")}, attachWS, attachEngB, "workspace")
	f := fakeRoutes(t, routes)

	if _, err := run(t, "engine", "attach", "kii", "--to", "other"); err != nil {
		t.Fatal(err)
	}
	calls := f.find("POST", "/databases/"+attachEngB+"/attachments")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	if _, ok := calls[0].Body["replace"]; ok {
		t.Fatalf("no --replace must leave the field out: %+v", calls[0].Body)
	}
}

func TestEngineAttachReplaceRequiresAdmin(t *testing.T) {
	routes := attachRoutes([]string{engineJSON(attachEngA, "brain", ""), engineJSON(attachEngB, "other", "")}, attachWS, attachEngB, "workspace")
	routes["POST /databases/"+attachEngB+"/attachments"] = resp{403, `{"error":{"code":"REPLACE_REQUIRES_ADMIN","message":"moving a workspace to another Engine requires an organization owner or admin"}}`}
	fakeRoutes(t, routes)

	_, err := run(t, "engine", "attach", "kii", "--to", "other", "--replace")
	if err == nil || !strings.Contains(err.Error(), "owner or admin") {
		t.Fatalf("err = %v", err)
	}
}
