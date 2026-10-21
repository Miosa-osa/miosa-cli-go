package commands_test

import (
	"strings"
	"testing"
)

// The Services product: /projects/:id/services and the flat /services/:id
// routes. See tasks/services-surface-gaps-2026-10-21.md for the route map.

const serviceProjectJSON = `{"data":[{"id":"11111111-1111-1111-1111-111111111111","name":"Clinic IQ","slug":"cliniciq","is_default":true},
{"id":"22222222-2222-2222-2222-222222222222","name":"Other","slug":"other","is_default":false}],"total":2}`

const webServiceJSON = `{"data":[
{"id":"33333333-3333-3333-3333-333333333333","project_id":"11111111-1111-1111-1111-111111111111",
 "environment_id":"e1","name":"API","slug":"api","kind":"server","source":{"type":"git","ref":"https://github.com/acme/api"},
 "build":{},"settings":{"replicas":2},"active_deployment_id":"44444444-4444-4444-4444-444444444444",
 "pending_changes":{"needs":"rebuild","variables":["DATABASE_URL"],"because":"Web.DATABASE_URL","at":"2026-10-21T10:00:00Z"},
 "private_domain":"api.miosa.internal","public_address":{"label":"api","host":"api.acme.miosa.app","url":"https://api.acme.miosa.app"},
 "lock_version":3,"created_at":"2026-10-20T10:00:00Z","updated_at":"2026-10-21T10:00:00Z"},
{"id":"55555555-5555-5555-5555-555555555555","project_id":"11111111-1111-1111-1111-111111111111",
 "environment_id":"e1","name":"Worker","slug":"worker","kind":"worker","source":{},"build":{},"settings":{},
 "private_domain":"worker.miosa.internal","public_address":{"label":null,"host":null,"url":null},
 "lock_version":1,"created_at":"2026-10-20T10:00:00Z","updated_at":"2026-10-20T10:00:00Z"}]}`

const oneWebServiceJSON = `{"data":{"id":"33333333-3333-3333-3333-333333333333","project_id":"11111111-1111-1111-1111-111111111111",
"environment_id":"e1","name":"API","slug":"api","kind":"server","source":{"type":"git","ref":"https://github.com/acme/api"},
"build":{},"settings":{"replicas":2},"active_deployment_id":"44444444-4444-4444-4444-444444444444",
"pending_changes":{"needs":"rebuild","variables":["DATABASE_URL"],"because":"Web.DATABASE_URL","at":"2026-10-21T10:00:00Z"},
"private_domain":"api.miosa.internal","public_address":{"label":"api","host":"api.acme.miosa.app","url":"https://api.acme.miosa.app"},
"lock_version":3,"created_at":"2026-10-20T10:00:00Z","updated_at":"2026-10-21T10:00:00Z"}}`

const releaseJSON = `{"data":{"id":"44444444-4444-4444-4444-444444444444","service_id":"33333333-3333-3333-3333-333333333333",
"environment_id":"e1","number":7,"state":"active","reason":null,"deadline_at":null,"trigger":"manual",
"image_ref":"registry.local/api:7","message":"first","rollback_of_id":null,
"source":{"type":"git","ref":"https://github.com/acme/api"},"started_at":"2026-10-21T10:00:00Z",
"finished_at":"2026-10-21T10:01:00Z","created_at":"2026-10-21T09:59:00Z"}}`

const releasesJSON = `{"data":[{"id":"44444444-4444-4444-4444-444444444444","service_id":"33333333-3333-3333-3333-333333333333",
"environment_id":"e1","number":7,"state":"active","reason":null,"deadline_at":null,"trigger":"manual",
"image_ref":"registry.local/api:7","message":"first","rollback_of_id":null,
"source":{"type":"git","ref":"https://github.com/acme/api"},"started_at":"2026-10-21T10:00:00Z",
"finished_at":"2026-10-21T10:01:00Z","created_at":"2026-10-21T09:59:00Z"}]}`

const eventsJSON = `{"data":[{"seq":1,"from_state":null,"to_state":"queued","reason_code":null,"detail":{},"actor":"user","at":"2026-10-21T09:59:00Z"},
{"seq":2,"from_state":"queued","to_state":"failed","reason_code":"build_failed","detail":{"message":"npm run build exited 1"},"actor":"system","at":"2026-10-21T10:01:00Z"}]}`

const variablesJSON = `{"data":[{"id":"v1","name":"DATABASE_URL","kind":"reference","source":"reference","sealed":false,
"build":true,"expression":"${{Postgres.DATABASE_URL}}","value_preview":null,"linked_service_ids":["66666666-6666-6666-6666-666666666666"],
"updated_at":"2026-10-21T10:00:00Z"}]}`

const effectiveJSON = `{"data":{"variables":[{"name":"DATABASE_URL","source":"user","layer":"service","kind":"reference",
"sealed":false,"build":true,"value_preview":"postgres://••••cdef","references":["Postgres.DATABASE_URL"],"overrides_platform":false,"overridden":false}],"problems":[]}}`

const environmentsJSON = `{"data":[{"id":"e1","project_id":"11111111-1111-1111-1111-111111111111","name":"production","slug":"production",
"kind":null,"is_default":true,"created_at":"2026-10-20T10:00:00Z"},
{"id":"e3","project_id":"11111111-1111-1111-1111-111111111111","name":"QA","slug":"qa","kind":null,"is_default":false,"created_at":"2026-10-20T10:00:00Z"}]}`

// servicesAPI is the route table for a project with one web service and one worker.
func servicesAPI(t *testing.T) *fakeAPI {
	return fakeRoutes(t, map[string]any{
		"GET /projects": serviceProjectJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/services":                                                                                              webServiceJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333":                                                         oneWebServiceJSON,
		"POST /projects/11111111-1111-1111-1111-111111111111/services":                                                                                             oneWebServiceJSON,
		"PATCH /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333":                                                       oneWebServiceJSON,
		"DELETE /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333":                                                      resp{202, `{"data":{"id":"33333333-3333-3333-3333-333333333333","state":"deleting"}}`},
		"PUT /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/public-address":                                          `{"data":{"label":"api","host":"api.acme.miosa.app","url":"https://api.acme.miosa.app"}}`,
		"DELETE /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/public-address":                                       resp{200, `{"data":{"label":null,"host":null,"url":null}}`},
		"POST /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/deployments":                                            resp{202, releaseJSON},
		"GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/deployments":                                             releasesJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/deployments/44444444-4444-4444-4444-444444444444":        releaseJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/deployments/44444444-4444-4444-4444-444444444444/events": eventsJSON,
		"POST /service-deployments/44444444-4444-4444-4444-444444444444/rollback":                                                                                  resp{202, releaseJSON},
		"POST /service-deployments/44444444-4444-4444-4444-444444444444/cancel":                                                                                    resp{200, releaseJSON},
		"DELETE /service-deployments/44444444-4444-4444-4444-444444444444":                                                                                         resp{200, releaseJSON},
		"POST /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/scale":                                                  resp{201, releaseJSON},
		"POST /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/start":                                                  resp{201, releaseJSON},
		"POST /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/stop":                                                   `{"data":{"stopped":2}}`,
		"POST /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/restart":                                                resp{201, releaseJSON},
		"GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/variables":                                               variablesJSON,
		"PUT /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/variables":                                               variablesJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/variables/effective":                                     effectiveJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/shared-variables":                                                                                      variablesJSON,
		"PUT /projects/11111111-1111-1111-1111-111111111111/shared-variables":                                                                                      variablesJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/environments":                                                                                          environmentsJSON,
		"POST /projects/11111111-1111-1111-1111-111111111111/environments":                                                                                         resp{201, `{"data":{"id":"e2","project_id":"11111111-1111-1111-1111-111111111111","name":"staging","slug":"staging","kind":null,"is_default":false,"created_at":"2026-10-21T10:00:00Z"}}`},
		"POST /projects/11111111-1111-1111-1111-111111111111/environments/e1/duplicate":                                                                            resp{201, `{"data":{"environment":{"id":"e2","name":"staging"},"services":[],"variables":[]}}`},
		"DELETE /projects/11111111-1111-1111-1111-111111111111/environments/e1":                                                                                    resp{200, `{"data":{"id":"e1","state":"deleted"}}`},
	})
}

func TestServiceListResolvesProjectByName(t *testing.T) {
	f := servicesAPI(t)
	out, err := run(t, "service", "list", "cliniciq")
	if err != nil {
		t.Fatalf("service list: %v", err)
	}
	if !strings.Contains(out, "api") || !strings.Contains(out, "worker") {
		t.Fatalf("want both services in the table, got:\n%s", out)
	}
	if len(f.find("GET", "/projects/11111111-1111-1111-1111-111111111111/services")) != 1 {
		t.Fatalf("want the services list called once with the resolved project id")
	}
}

func TestServiceCreateWebMapsToServerAndEnablesAddress(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "service", "create", "cliniciq", "API", "--type", "web",
		"--repo", "https://github.com/acme/api", "--branch", "main",
		"--start-command", "node server.js", "--replicas", "2", "--region", "us-east",
		"--env", "NODE_ENV=production")
	if err != nil {
		t.Fatalf("service create: %v", err)
	}

	created := f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/services")
	if len(created) != 1 {
		t.Fatalf("want one create call, got %d", len(created))
	}
	body := created[0].Body
	if body["kind"] != "server" {
		t.Fatalf("web must map to kind server, got %v", body["kind"])
	}
	src, _ := body["source"].(map[string]interface{})
	if src["type"] != "git" || src["ref"] != "https://github.com/acme/api" || src["branch"] != "main" {
		t.Fatalf("wrong source: %#v", src)
	}
	settings, _ := body["settings"].(map[string]interface{})
	if settings["start_command"] != "node server.js" {
		t.Fatalf("wrong settings: %#v", settings)
	}
	if settings["replicas"] != float64(2) {
		t.Fatalf("replicas must be a number, got %#v", settings["replicas"])
	}
	if settings["region"] != "us-east" {
		t.Fatalf("region missing: %#v", settings)
	}
	if len(f.find("PUT", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/public-address")) != 1 {
		t.Fatalf("a web service must get a public address")
	}
	vars := f.find("PUT", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/variables")
	if len(vars) != 1 {
		t.Fatalf("want one variables write, got %d", len(vars))
	}
	if got := vars[0].Body["variables"]; got == nil {
		t.Fatalf("variables body missing: %#v", vars[0].Body)
	}
}

func TestServiceCreatePrivateSkipsAddress(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "service", "create", "cliniciq", "DB Proxy", "--type", "private", "--image", "acme/proxy:1", "--port", "6543")
	if err != nil {
		t.Fatalf("service create private: %v", err)
	}
	body := f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/services")[0].Body
	if body["kind"] != "server" {
		t.Fatalf("private must map to kind server, got %v", body["kind"])
	}
	src, _ := body["source"].(map[string]interface{})
	if src["type"] != "image" || src["ref"] != "acme/proxy:1" || src["port"] != float64(6543) {
		t.Fatalf("wrong image source: %#v", src)
	}
	if len(f.find("PUT", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/public-address")) != 0 {
		t.Fatalf("a private service must not get a public address")
	}
}

func TestServiceCreateRejectsUnknownType(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /projects": serviceProjectJSON})
	_, err := run(t, "service", "create", "cliniciq", "X", "--type", "lambda")
	if err == nil {
		t.Fatal("want an error for an unknown type")
	}
	if !strings.Contains(err.Error(), "web, worker, private, static") {
		t.Fatalf("error should list the types, got: %v", err)
	}
}

func TestServiceCreateCronNeedsSchedule(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /projects": serviceProjectJSON})
	_, err := run(t, "service", "create", "cliniciq", "Nightly", "--type", "schedule")
	if err == nil || !strings.Contains(err.Error(), "--schedule") {
		t.Fatalf("want a --schedule error, got: %v", err)
	}
}

func TestServiceGetResolvesByName(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "service", "get", "cliniciq", "API")
	if err != nil {
		t.Fatalf("service get: %v", err)
	}
	if !strings.Contains(out, "https://api.acme.miosa.app") {
		t.Fatalf("want the public url, got:\n%s", out)
	}
}

func TestServiceDeployQueuesARelease(t *testing.T) {
	f := servicesAPI(t)
	out, err := run(t, "service", "deploy", "cliniciq", "API", "--message", "first")
	if err != nil {
		t.Fatalf("service deploy: %v", err)
	}
	if !strings.Contains(out, "44444444") && !strings.Contains(out, "7") {
		t.Fatalf("want the release in the output, got:\n%s", out)
	}
	calls := f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/deployments")
	if len(calls) != 1 {
		t.Fatalf("want one deploy call, got %d", len(calls))
	}
	if calls[0].Body["message"] != "first" {
		t.Fatalf("wrong body: %#v", calls[0].Body)
	}
}

func TestServiceScaleSendsReplicas(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "service", "scale", "cliniciq", "API", "--replicas", "4")
	if err != nil {
		t.Fatalf("service scale: %v", err)
	}
	calls := f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/scale")
	if len(calls) != 1 {
		t.Fatalf("want one scale call, got %d", len(calls))
	}
	if calls[0].Body["replicas"] != float64(4) {
		t.Fatalf("wrong replicas: %#v", calls[0].Body)
	}
}

func TestServiceScaleNeedsAChange(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /projects": serviceProjectJSON,
		"GET /projects/11111111-1111-1111-1111-111111111111/services": webServiceJSON,
	})
	_, err := run(t, "service", "scale", "cliniciq", "API")
	if err == nil || !strings.Contains(err.Error(), "--replicas") {
		t.Fatalf("want a --replicas error, got: %v", err)
	}
}

func TestServiceStopAndStart(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "service", "stop", "cliniciq", "API", "--yes"); err != nil {
		t.Fatalf("service stop: %v", err)
	}
	if len(f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/stop")) != 1 {
		t.Fatal("want one stop call")
	}
	if _, err := run(t, "service", "start", "cliniciq", "API"); err != nil {
		t.Fatalf("service start: %v", err)
	}
	if len(f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/start")) != 1 {
		t.Fatal("want one start call")
	}
}

func TestServiceDeleteConfirms(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "service", "delete", "cliniciq", "API")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("delete must require confirmation, got: %v", err)
	}
	if _, err := run(t, "service", "delete", "cliniciq", "API", "--yes"); err != nil {
		t.Fatalf("service delete: %v", err)
	}
	if len(f.find("DELETE", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333")) != 1 {
		t.Fatal("want one delete call")
	}
}

func TestServiceReleasesListsHistory(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "service", "releases", "cliniciq", "API")
	if err != nil {
		t.Fatalf("service releases: %v", err)
	}
	if !strings.Contains(out, "active") || !strings.Contains(out, "7") {
		t.Fatalf("want the release row, got:\n%s", out)
	}
}

func TestServiceRollbackPostsToTheRelease(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "service", "rollback", "cliniciq", "API", "7", "--yes"); err != nil {
		t.Fatalf("service rollback: %v", err)
	}
	if len(f.find("POST", "/service-deployments/44444444-4444-4444-4444-444444444444/rollback")) != 1 {
		t.Fatal("want a rollback of the resolved release")
	}
}

func TestServicePromoteIsRollback(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "service", "promote", "cliniciq", "API", "7", "--yes"); err != nil {
		t.Fatalf("service promote: %v", err)
	}
	if len(f.find("POST", "/service-deployments/44444444-4444-4444-4444-444444444444/rollback")) != 1 {
		t.Fatal("promote must call the rollback route")
	}
}

func TestServiceLogsPrintsReleaseEvents(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "service", "logs", "cliniciq", "API")
	if err != nil {
		t.Fatalf("service logs: %v", err)
	}
	if !strings.Contains(out, "build_failed") || !strings.Contains(out, "npm run build exited 1") {
		t.Fatalf("want the failure reason in the log, got:\n%s", out)
	}
}

func TestServiceLogsFollowIsRefused(t *testing.T) {
	servicesAPI(t)
	_, err := run(t, "service", "logs", "cliniciq", "API", "--follow")
	if err == nil || !strings.Contains(err.Error(), "row 12") {
		t.Fatalf("want a clear refusal that streaming needs row 12, got: %v", err)
	}
}

func TestServiceDomainsShowsTheAddress(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "service", "domains", "cliniciq", "API")
	if err != nil {
		t.Fatalf("service domains: %v", err)
	}
	if !strings.Contains(out, "api.acme.miosa.app") {
		t.Fatalf("want the host, got:\n%s", out)
	}
}

func TestServiceVarsSetSendsReferences(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "service", "vars", "set", "cliniciq", "API", "DATABASE_URL=${{Postgres.DATABASE_URL}}")
	if err != nil {
		t.Fatalf("service vars set: %v", err)
	}
	calls := f.find("PUT", "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/variables")
	if len(calls) != 1 {
		t.Fatalf("want one variables write, got %d", len(calls))
	}
	vars, _ := calls[0].Body["variables"].(map[string]interface{})
	if vars["DATABASE_URL"] != "${{Postgres.DATABASE_URL}}" {
		t.Fatalf("a reference must pass through unchanged, got %#v", vars)
	}
}

func TestServiceVarsListMasksValues(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "service", "vars", "list", "cliniciq", "API")
	if err != nil {
		t.Fatalf("service vars list: %v", err)
	}
	if !strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("want the variable name, got:\n%s", out)
	}
}

func TestProjectVarsSetWritesSharedVariables(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "project", "vars", "set", "cliniciq", "API_BASE=https://api.acme.dev")
	if err != nil {
		t.Fatalf("project vars set: %v", err)
	}
	calls := f.find("PUT", "/projects/11111111-1111-1111-1111-111111111111/shared-variables")
	if len(calls) != 1 {
		t.Fatalf("want one shared variables write, got %d", len(calls))
	}
	vars, _ := calls[0].Body["variables"].(map[string]interface{})
	if vars["API_BASE"] != "https://api.acme.dev" {
		t.Fatalf("wrong body: %#v", calls[0].Body)
	}
}

func TestServiceRejectsAnUnknownServiceWithCandidates(t *testing.T) {
	servicesAPI(t)
	_, err := run(t, "service", "get", "cliniciq", "nope")
	if err == nil {
		t.Fatal("want a not-found error")
	}
	if !strings.Contains(err.Error(), "API") {
		t.Fatalf("the error should list the real names, got: %v", err)
	}
}
func TestProjectEnvironmentList(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "project", "target", "list", "cliniciq")
	if err != nil {
		t.Fatalf("project target list: %v", err)
	}
	if !strings.Contains(out, "production") || !strings.Contains(out, "QA") {
		t.Fatalf("want both Deploy targets, got:\n%s", out)
	}
}

func TestProjectEnvironmentCreate(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "project", "target", "create", "cliniciq", "staging", "--slug", "staging")
	if err != nil {
		t.Fatalf("project target create: %v", err)
	}
	calls := f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/environments")
	if len(calls) != 1 {
		t.Fatalf("want one create call, got %d", len(calls))
	}
	if calls[0].Body["name"] != "staging" || calls[0].Body["slug"] != "staging" {
		t.Fatalf("wrong body: %#v", calls[0].Body)
	}
}

func TestProjectEnvironmentDuplicateResolvesByName(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "project", "target", "duplicate", "cliniciq", "production", "--name", "staging")
	if err != nil {
		t.Fatalf("project target duplicate: %v", err)
	}
	if len(f.find("POST", "/projects/11111111-1111-1111-1111-111111111111/environments/e1/duplicate")) != 1 {
		t.Fatal("want a duplicate of the resolved Deploy target id")
	}
}

func TestProjectEnvironmentDuplicateNeedsAName(t *testing.T) {
	servicesAPI(t)
	_, err := run(t, "project", "target", "duplicate", "cliniciq", "production")
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("want a --name error, got: %v", err)
	}
}

func TestProjectEnvironmentRemoveConfirms(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "project", "target", "rm", "cliniciq", "production")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("rm must require confirmation, got: %v", err)
	}
	if _, err := run(t, "project", "target", "rm", "cliniciq", "production", "--yes"); err != nil {
		t.Fatalf("project target rm: %v", err)
	}
	if len(f.find("DELETE", "/projects/11111111-1111-1111-1111-111111111111/environments/e1")) != 1 {
		t.Fatal("want one delete call")
	}
}
func TestServiceGetShowsPendingVariableChanges(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "service", "get", "cliniciq", "API")
	if err != nil {
		t.Fatalf("service get: %v", err)
	}
	if !strings.Contains(out, "rebuild needed for DATABASE_URL") {
		t.Fatalf("want the pending rebuild, got:\n%s", out)
	}
	if !strings.Contains(out, "via Web.DATABASE_URL") {
		t.Fatalf("want the reference that caused it, got:\n%s", out)
	}
	if !strings.Contains(out, "miosa service deploy") {
		t.Fatalf("want the action, got:\n%s", out)
	}
}
