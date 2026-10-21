package commands_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const capWS = "5d968ccf-7c64-4ca6-b887-a5b62d1d04fe"

func capAPI(t *testing.T) *orgFakeAPI {
	return &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		if r.URL.Path == "/limits" {
			return 200, `{"data":{"organization":{"id":"org-1","name":"Acme","type":"company","role":"owner"},"can_start":true}}`
		}
		return 200, `{"data":{"org":{"cap_cents":50000,"action":"alert","spent_cents":120},"workspaces":[{"workspace_id":"` + capWS + `","name":"web","cap_cents":null,"total_cap_cents":10000,"total_spent_cents":2500,"action":"pause"}]}}`
	}}
}

func TestBillingCapSetOrgSendsCentsAndAction(t *testing.T) {
	api := capAPI(t)
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "billing", "cap", "set", "--org", "--monthly", "500", "--action", "alert")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	got := api.last(t)
	if got.Method != "PATCH" || got.Path != "/tenants/org-1/sandbox-spend" {
		t.Fatalf("request = %+v", got)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(got.Body), &body)
	if body["org_cap_cents"] != float64(50000) || body["org_cap_action"] != "alert" {
		t.Errorf("body = %v", body)
	}
	if !strings.Contains(out, "action alert") {
		t.Errorf("output = %s", out)
	}
}

func TestBillingCapSetWorkspaceDefaultsToAllProducts(t *testing.T) {
	api := capAPI(t)
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	if _, err := runOrg(t, "billing", "cap", "set", "--workspace", capWS, "--monthly", "100", "--action", "pause"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body struct {
		Caps []map[string]any `json:"workspace_caps"`
	}
	_ = json.Unmarshal([]byte(api.last(t).Body), &body)
	if len(body.Caps) != 1 {
		t.Fatalf("body = %s", api.last(t).Body)
	}
	c := body.Caps[0]
	if c["workspace_id"] != capWS || c["scope"] != "all" || c["cap_cents"] != float64(10000) || c["action"] != "pause" {
		t.Errorf("cap = %v", c)
	}
}

func TestBillingCapClearSendsNull(t *testing.T) {
	api := capAPI(t)
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	if _, err := runOrg(t, "billing", "cap", "set", "--org", "--clear"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(api.last(t).Body, `"org_cap_cents":null`) {
		t.Errorf("body = %s", api.last(t).Body)
	}
}

func TestBillingCapSetRefusesBadInput(t *testing.T) {
	api := capAPI(t)
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	for _, args := range [][]string{
		{"billing", "cap", "set", "--monthly", "5"},
		{"billing", "cap", "set", "--org", "--workspace", capWS, "--monthly", "5"},
		{"billing", "cap", "set", "--org"},
		{"billing", "cap", "set", "--org", "--monthly", "5", "--action", "destroy"},
		{"billing", "cap", "set", "--org", "--monthly", "-5"},
		{"billing", "cap", "set", "--org", "--clear", "--monthly", "5"},
	} {
		if _, err := runOrg(t, args...); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
	if len(api.requests) != 0 {
		t.Errorf("a bad command reached the API: %+v", api.requests)
	}
}

func TestBillingCapShowListsCaps(t *testing.T) {
	api := capAPI(t)
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "billing", "cap", "show")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"$500.00", "action alert", "web", "$25.00 of $100.00 across all products"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
