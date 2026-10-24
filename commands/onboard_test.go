package commands_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const onboardPricing = `{"plans":[
 {"id":"free","name":"Free","price_cents":0},
 {"id":"developer","name":"Developer","description":"$20 a month","price_cents":2000},
 {"id":"team","name":"Team","description":"for teams","price_cents":10000},
 {"id":"enterprise","name":"Enterprise","price_cents":500000,"approval_required":true}]}`

const onboardWorkspaces = `{"data":[{"id":"w-other","name":"scratch","is_default":false},{"id":"w-1","name":"Default","is_default":true}]}`

// onboardAPI answers the onboarding routes; plan is the organization's plan,
// switched to the subscribed one after `paidAfter` plan reads.
func onboardAPI(t *testing.T, plan string, paidAfter int32) (*fakeAPI, *int32) {
	var reads int32
	var subscribed atomic.Value
	subscribed.Store("")
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Method + " " + c.Path {
		case "POST /auth/cli/start":
			return 200, `{"device_code":"dev-secret","user_code":"CODE-1","verification_uri":"https://miosa.ai/cli/auth","verification_uri_complete":"https://miosa.ai/cli/auth?code=CODE-1","expires_in":600,"interval":1}`
		case "POST /auth/cli/token":
			return 200, `{"api_key":"msk_u_onboard","tenant":{"id":"t-1","slug":"acme","name":"Acme Inc"}}`
		case "GET /platform/tenants/current":
			return 200, tenantJSON
		case "GET /whoami":
			if c.Headers.Get("Authorization") == "" {
				return 401, `{"error":"unauthorized"}`
			}
			return 200, `{"user":{"email":"ada@acme.dev"},"organization":{"name":"Acme Inc","slug":"acme"}}`
		case "GET /bill-to":
			return 200, `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme Inc","viewing":true}]}}`
		case "GET /workspaces":
			return 200, onboardWorkspaces
		case "GET /tenant/plan":
			n := atomic.AddInt32(&reads, 1)
			if s := subscribed.Load().(string); s != "" && n > paidAfter {
				return 200, `{"plan":{"name":"` + s + `"}}`
			}
			return 200, `{"plan":{"name":"` + plan + `"}}`
		case "GET /pricing":
			return 200, onboardPricing
		case "POST /billing/checkout":
			subscribed.Store(c.Body["plan"].(string))
			return 200, `{"checkout_url":"https://checkout.example/s/1"}`
		}
		t.Logf("unhandled %s %s", c.Method, c.Path)
		return 404, `{}`
	})
	return f, &reads
}

func decodeData(t *testing.T, out string) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return env.Data
}

func TestOnboardForAnAgentIsTwoSteps(t *testing.T) {
	f, _ := onboardAPI(t, "free", 0)
	os.Unsetenv("MIOSA_API_KEY")
	defer commands.SetSleepForTest(func(context.Context, time.Duration) error { return nil })()

	out, err := run(t, "onboard", "--github", "--json")
	if err != nil {
		t.Fatalf("start: %v %s", err, out)
	}
	first := decodeData(t, out)
	if first["status"] != "pending" || first["next"] != "miosa onboard --wait" {
		t.Fatalf("start = %s", out)
	}
	if first["url"] != "https://miosa.ai/cli/auth?code=CODE-1&method=github" {
		t.Fatalf("the link must carry the sign-in method: %v", first["url"])
	}

	out, err = run(t, "onboard", "--wait", "--json")
	if err != nil {
		t.Fatalf("wait: %v %s", err, out)
	}
	done := decodeData(t, out)
	if done["signed_in_now"] != true || done["user"] != "ada@acme.dev" || done["plan"] != "free" {
		t.Fatalf("finish = %s", out)
	}
	if ws, _ := done["workspace"].(map[string]any); ws["id"] != "w-1" {
		t.Fatalf("workspace = %v", done["workspace"])
	}
	if plans, _ := done["plans"].([]any); len(plans) != 2 || plans[0] != "developer" || plans[1] != "team" {
		t.Fatalf("an agent on Free gets the buyable plans, not a prompt: %v", done["plans"])
	}
	if len(f.find("POST", "/billing/checkout")) != 0 {
		t.Fatal("an agent must never start a checkout it was not asked for")
	}
	if !strings.Contains(readConfig(t), "msk_u_onboard") {
		t.Fatal("the issued key was not stored")
	}
}

func TestOnboardAgainOnlyReports(t *testing.T) {
	f, _ := onboardAPI(t, "developer", 0)
	out, err := run(t, "onboard", "--json")
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	got := decodeData(t, out)
	if got["signed_in_now"] != false || got["plan"] != "developer" || got["plans"] != nil {
		t.Fatalf("re-run = %s", out)
	}
	if len(f.find("POST", "/auth/cli/start")) != 0 || len(f.find("POST", "/billing/checkout")) != 0 {
		t.Fatal("a signed-in, paid account must not sign in or check out again")
	}
}

func TestOnboardPlanOpensCheckoutAndWaitsForThePayment(t *testing.T) {
	f, _ := onboardAPI(t, "free", 2)
	defer commands.SetOnboardWaitForTest(time.Millisecond, time.Second)()
	var opened string
	defer commands.SetOpenBrowserForTest(func(u string) error { opened = u; return nil })()

	out, err := run(t, "onboard", "--plan", "developer")
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	co := f.find("POST", "/billing/checkout")
	if len(co) != 1 || co[0].Body["type"] != "subscription" || co[0].Body["plan"] != "developer" {
		t.Fatalf("checkout = %+v", co)
	}
	if opened != "https://checkout.example/s/1" {
		t.Fatalf("opened %q", opened)
	}
	if !strings.Contains(out, "You are on developer now.") {
		t.Fatalf("output: %s", out)
	}
}

func TestOnboardOnATerminalOffersThePlans(t *testing.T) {
	f, _ := onboardAPI(t, "free", 0)
	defer commands.SetStdinTerminalForTest(true)()
	defer commands.SetOnboardWaitForTest(time.Millisecond, time.Second)()
	defer commands.SetOpenBrowserForTest(func(string) error { return nil })()

	out, err := runIn(t, "2\n", "onboard")
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if !strings.Contains(out, "1) developer") || strings.Contains(out, "enterprise") {
		t.Fatalf("the menu lists only plans bought online: %s", out)
	}
	if co := f.find("POST", "/billing/checkout"); len(co) != 1 || co[0].Body["plan"] != "team" {
		t.Fatalf("picking 2 must check out team: %+v", co)
	}

	f2, _ := onboardAPI(t, "free", 0)
	if out, err := runIn(t, "\n", "onboard"); err != nil || !strings.Contains(out, "You are on Free") {
		t.Fatalf("Enter stays on Free: %v %s", err, out)
	}
	if len(f2.find("POST", "/billing/checkout")) != 0 {
		t.Fatal("Enter must not start a checkout")
	}
}

func TestLoginMethodFlags(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.Method+" "+c.Path == "POST /auth/cli/start" {
			return 200, `{"device_code":"d","user_code":"CODE-9","verification_uri":"https://miosa.ai/cli/auth","verification_uri_complete":"https://miosa.ai/cli/auth?code=CODE-9","expires_in":600,"interval":1}`
		}
		return 404, `{}`
	})
	os.Unsetenv("MIOSA_API_KEY")
	out, err := run(t, "login", "--no-wait", "--signup", "--json")
	if err != nil || decodeData(t, out)["url"] != "https://miosa.ai/cli/auth?code=CODE-9&method=signup" {
		t.Fatalf("--signup: %v %s", err, out)
	}
	if _, err := run(t, "login", "--no-wait", "--new", "--github", "--google"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("two methods must be a usage error: %v", err)
	}
}
