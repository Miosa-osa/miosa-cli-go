package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const billToPinned = `{"data":{
  "billing":{"id":"org-acme","name":"Acme","type":"company","role":"member"},
  "source":"setting","setting_pinned":true,"setting_stale":false,"viewing_id":"org-mine","can_change":true,
  "options":[
    {"id":"org-mine","name":"Mine","slug":"mine","type":"personal","role":"owner","viewing":true,"billing":false,"pinned":false},
    {"id":"org-acme","name":"Acme","slug":"acme","type":"company","role":"member","viewing":false,"billing":true,"pinned":true}
  ]}}`

const billToFollowing = `{"data":{
  "billing":{"id":"org-mine","name":"Mine","type":"personal","role":"owner"},
  "source":"credential","setting_pinned":false,"setting_stale":false,"viewing_id":"org-mine","can_change":true,
  "options":[
    {"id":"org-mine","name":"Mine","slug":"mine","type":"personal","role":"owner","viewing":true,"billing":true,"pinned":false}
  ]}}`

// recorded is one request the fake API saw.
type recorded struct {
	Method string
	Path   string
	Body   string
	BillTo string
}

type orgFakeAPI struct {
	mu       sync.Mutex
	requests []recorded
	handler  func(r *http.Request, body string) (int, string)
}

func (f *orgFakeAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, string(raw), r.Header.Get("X-Miosa-Bill-To")})
		f.mu.Unlock()
		status, body := f.handler(r, string(raw))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func (f *orgFakeAPI) last(t *testing.T) recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("the API saw no requests")
	}
	return f.requests[len(f.requests)-1]
}

// runOrg runs the CLI with the global --org flag and MIOSA_ORG cleared first,
// so a previous test cannot leak into this one.
func runOrg(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("MIOSA_ORG", "")
	_ = commands.Root().PersistentFlags().Set("org", "")
	t.Cleanup(func() { _ = commands.Root().PersistentFlags().Set("org", "") })
	return run(t, args...)
}

func TestOrgShowsWhoPaysAndWhatIsViewed(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) { return 200, billToPinned }}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "org")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Bills to   Acme (company, member)", "pinned for your account", "Viewing    Mine"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if got := api.last(t); got.Method != "GET" || got.Path != "/bill-to" {
		t.Errorf("request = %+v", got)
	}
}

func TestOrgShowFollowingExplainsTheDefault(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) { return 200, billToFollowing }}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "org")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "the organization your key belongs to") {
		t.Errorf("output:\n%s", out)
	}
	if strings.Contains(out, "Viewing") {
		t.Errorf("viewing line is redundant when it is also who pays:\n%s", out)
	}
}

func TestOrgListMarksWhoPaysAndTheKeyOrganization(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) { return 200, billToPinned }}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "org", "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "bills") || !strings.Contains(out, "key org") || !strings.Contains(out, "org-acme") {
		t.Errorf("output:\n%s", out)
	}

	jsonOut, err := runOrg(t, "org", "list", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var options []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &options); err != nil || len(options) != 2 {
		t.Fatalf("json output = %q (%v)", jsonOut, err)
	}
}

func TestOrgBillPinsAndClears(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) { return 200, billToPinned }}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "org", "bill", "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "New machines now bill Acme") {
		t.Errorf("output:\n%s", out)
	}
	if got := api.last(t); got.Method != "PUT" || got.Path != "/bill-to" || got.Body != `{"org":"acme"}` {
		t.Errorf("request = %+v", got)
	}

	if _, err := runOrg(t, "org", "bill", "--clear"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := api.last(t); got.Method != "DELETE" || got.Path != "/bill-to" {
		t.Errorf("request = %+v", got)
	}
}

func TestOrgBillNeedsExactlyOneOfAnOrgOrClear(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) { return 200, billToPinned }}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	for _, args := range [][]string{{"org", "bill"}, {"org", "bill", "acme", "--clear"}} {
		if _, err := runOrg(t, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	if len(api.requests) != 0 {
		t.Errorf("a malformed command must not reach the API: %+v", api.requests)
	}
}

func TestOrgBillShowsTheAPIRefusalWithItsCode(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		return 403, `{"ok":false,"error":{"code":"BILL_TO_LOCKED","message":"This credential is locked to the organization it was issued for."}}`
	}}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	_, err := runOrg(t, "org", "bill", "acme")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "locked to the organization") || !strings.Contains(err.Error(), "BILL_TO_LOCKED") {
		t.Errorf("error = %v", err)
	}
}

func TestGlobalOrgFlagTravelsAsTheBillToHeader(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		return 200, `{"data":{"organization":{"id":"org-acme","name":"Acme","type":"company"},"can_start":true,
			"credits":{"available_cents":899},"plan":{"name":"pro"},
			"concurrency":{"limit":10,"running":4,"remaining":6},
			"spend":{"mode":"limited","cap_cents":5000,"accounted_cents":1200,"status":"active"},"member":null}}`
	}}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "--org", "acme", "limits")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := api.last(t); got.Path != "/limits" || got.BillTo != "acme" {
		t.Errorf("request = %+v", got)
	}
	for _, want := range []string{"Bills to      Acme (company)", "Can start     yes", "$8.99 available", "4 of 10", "$12.00 of $50.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Without the flag nothing is sent: the account setting decides.
	if _, err := runOrg(t, "limits"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := api.last(t); got.BillTo != "" {
		t.Errorf("header leaked into a later command: %+v", got)
	}
}

func TestMiosaOrgEnvironmentVariableIsTheSameOverride(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		return 200, `{"data":{"organization":{"name":"Acme"},"can_start":true,"credits":{"available_cents":1},"concurrency":{"limit":null,"running":0},"spend":{"mode":"unlimited"}}}`
	}}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	t.Setenv("MIOSA_ORG", "acme")
	commands.ResetForTest()
	_ = commands.Root().PersistentFlags().Set("org", "")
	root := commands.Root()
	root.SetArgs([]string{"limits"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := api.last(t); got.BillTo != "acme" {
		t.Errorf("request = %+v", got)
	}
}

func TestLimitsExplainsWhyMachinesCannotStart(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		return 200, `{"data":{"organization":{"name":"Acme","type":"company"},"can_start":false,
			"blocked_reasons":["member_usage_cap_reached","something_new"],
			"credits":{"available_cents":50},"concurrency":{"limit":null,"running":2},
			"spend":{"mode":"unlimited","accounted_cents":0},
			"member":{"usage_cap_cents":500,"usage_cents":500,"max_concurrent_sandboxes":2,"running_sandboxes":1}}}`
	}}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "limits")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Can start     no: you reached your usage cap in this organization; something_new",
		"Running       2 of no limit",
		"Sandbox spend $0.00 of no cap",
		"Your caps     $5.00 of $5.00 spent, 1 of 2 running",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

const memberCapsList = `{"data":{"window_end":"2026-11-01T00:00:00Z","members":[
  {"user_id":"u-dana","email":"dana@example.com","name":"Dana","role":"member","usage_cap_cents":2500,"max_concurrent_sandboxes":3,"usage_cents":100,"running_sandboxes":1},
  {"user_id":"u-eli","email":"eli@example.com","name":"Eli","role":"admin","usage_cap_cents":null,"max_concurrent_sandboxes":null,"usage_cents":0,"running_sandboxes":0}
]}}`

func capsAPI() *orgFakeAPI {
	return &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		switch {
		case r.URL.Path == "/bill-to":
			return 200, billToFollowing
		case strings.HasSuffix(r.URL.Path, "/member-caps") && r.Method == "GET":
			return 200, memberCapsList
		case strings.Contains(r.URL.Path, "/member-caps/"):
			return 200, `{"data":{"user_id":"u-dana","email":"dana@example.com","name":"Dana","role":"member","usage_cap_cents":2500,"max_concurrent_sandboxes":null,"usage_cents":100,"running_sandboxes":1}}`
		}
		return 404, `{"error":"not_found"}`
	}}
}

func TestOrgCapsListShowsUsageAgainstCaps(t *testing.T) {
	api := capsAPI()
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "org", "caps")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Dana <dana@example.com>", "$1.00", "$25.00", "no cap", "no limit", "Usage resets 2026-11-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if got := api.last(t); got.Path != "/tenants/org-mine/member-caps" {
		t.Errorf("caps are managed for the key's organization, got %+v", got)
	}
}

func TestOrgCapsSetSendsOnlyTheFieldsGiven(t *testing.T) {
	api := capsAPI()
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	if _, err := runOrg(t, "org", "caps", "set", "DANA@example.com", "--spend", "$25.50"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := api.last(t)
	if got.Method != "PUT" || got.Path != "/tenants/org-mine/member-caps/u-dana" || got.Body != `{"usage_cap_cents":2550}` {
		t.Errorf("request = %+v", got)
	}

	if _, err := runOrg(t, "org", "caps", "set", "dana", "--no-spend-cap", "--concurrent", "4"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(api.last(t).Body), &body); err != nil {
		t.Fatal(err)
	}
	if v, present := body["usage_cap_cents"]; !present || v != nil {
		t.Errorf("--no-spend-cap must send an explicit null, body = %v", body)
	}
	if body["max_concurrent_sandboxes"] != float64(4) {
		t.Errorf("body = %v", body)
	}
}

func TestOrgCapsSetRejectsInputTheServerWouldRefuse(t *testing.T) {
	api := capsAPI()
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	cases := [][]string{
		{"org", "caps", "set", "dana"},                                   // nothing to change
		{"org", "caps", "set", "dana", "--spend", "abc"},                 // not money
		{"org", "caps", "set", "dana", "--spend", "0"},                   // zero is "no cap", not a cap
		{"org", "caps", "set", "dana", "--spend", "1.234"},               // fractions of a cent
		{"org", "caps", "set", "dana", "--concurrent", "0"},              // not a limit
		{"org", "caps", "set", "dana", "--spend", "5", "--no-spend-cap"}, // contradictory
		{"org", "caps", "set", "nobody@example.com", "--spend", "5"},     // unknown member
	}
	for _, args := range cases {
		if _, err := runOrg(t, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	for _, r := range api.requests {
		if r.Method == "PUT" {
			t.Errorf("an invalid command reached the API: %+v", r)
		}
	}
}

func TestOrgCapsClear(t *testing.T) {
	api := capsAPI()
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "org", "caps", "clear", "dana@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Removed the caps on Dana") {
		t.Errorf("output:\n%s", out)
	}
	if got := api.last(t); got.Method != "DELETE" || got.Path != "/tenants/org-mine/member-caps/u-dana" {
		t.Errorf("request = %+v", got)
	}
}

func TestOrgCapsRefuseAnotherOrganizationThanTheKeys(t *testing.T) {
	api := capsAPI()
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	_, err := runOrg(t, "--org", "acme", "org", "caps")
	if err == nil || !strings.Contains(err.Error(), "this key belongs to") {
		t.Fatalf("error = %v", err)
	}
	// Naming the key's own organization is fine.
	if _, err := runOrg(t, "--org", "mine", "org", "caps"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrgMembersAndInvite(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		switch {
		case r.URL.Path == "/bill-to":
			return 200, billToFollowing
		case r.URL.Path == "/tenants/org-mine/members":
			return 200, `{"total":1,"members":[{"user_id":"u1","user_name":"Dana","user_email":"dana@example.com","role":"admin","status":"active"}]}`
		case r.URL.Path == "/tenants/org-mine/invites":
			return 201, `{"data":{"invite_id":"i1","email":"new@example.com","role":"member","expires_at":"2026-10-16T00:00:00Z","invite_url":"https://miosa.ai/invites/tok"}}`
		}
		return 404, `{"error":"not_found"}`
	}}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := runOrg(t, "org", "members")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "dana@example.com") || !strings.Contains(out, "admin") {
		t.Errorf("output:\n%s", out)
	}

	out, err = runOrg(t, "org", "invite", "new@example.com", "--role", "member")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Invited new@example.com") || !strings.Contains(out, "https://miosa.ai/invites/tok") {
		t.Errorf("output:\n%s", out)
	}
	if got := api.last(t); got.Method != "POST" || got.Body != `{"email":"new@example.com","role":"member"}` {
		t.Errorf("request = %+v", got)
	}
}

// A create goes through the SDK, not the REST client the other commands use.
// The override must reach it too, or `--org` would bill the wrong organization.
func TestGlobalOrgFlagReachesTheCreateCall(t *testing.T) {
	api := &orgFakeAPI{handler: func(r *http.Request, _ string) (int, string) {
		if r.Method == http.MethodPost && r.URL.Path == "/sandboxes" {
			return 201, string(fakeSandbox("abc123", "my-box"))
		}
		return 404, `{"error":"not_found"}`
	}}
	srv := api.server(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	if _, err := runOrg(t, "--org", "Acme", "create", "my-box", "--size", "small"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := api.last(t)
	if got.Method != "POST" || got.Path != "/sandboxes" || got.BillTo != "Acme" {
		t.Errorf("request = %+v", got)
	}

	if _, err := runOrg(t, "create", "other-box", "--size", "small"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := api.last(t); got.BillTo != "" {
		t.Errorf("the override leaked into a later create: %+v", got)
	}
}
