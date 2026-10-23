package commands_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestForwardReverseRunsSSHWithARemoteForwardPerMapping(t *testing.T) {
	log := installFakeSSH(t)
	fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/ssh-certificates": resp{201, `{"data":{"certificate":"ssh-ed25519-cert-v01@openssh.com AAAACERT","user":"root","tunnel_url":"wss://api.example/api/v1/sandboxes/` + boxID + `/ssh-tunnel"}}`},
	})
	if _, errOut, err := runSplitIn(t, "", "forward", boxID, "--reverse", "3000:8080", "5432:15432"); err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	args := readFile(t, log+".args")
	for _, want := range []string{"-N", "ExitOnForwardFailure=yes", "-R\n8080:127.0.0.1:3000", "-R\n15432:127.0.0.1:5432", "root@sandbox-" + boxID} {
		if !strings.Contains(args, want) {
			t.Errorf("ssh args lack %q:\n%s", want, args)
		}
	}
}

func TestForkSetsVariablesAndSizeOnTheForkOnly(t *testing.T) {
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	const forkID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("POST", boxPath+"/fork"):
			return 201, `{"id":"` + forkID + `","state":"provisioning","name":"my-box-fork"}`, true
		case c.is("GET", "/sandboxes/"+forkID):
			return 200, `{"id":"` + forkID + `","state":"running","ready":true}`, true
		case c.is("PUT", "/sandboxes/"+forkID+"/env"):
			return 200, `{"data":[]}`, true
		case c.is("POST", "/sandboxes/"+forkID+"/env/sync"):
			return 200, `{"data":{"status":"synced"}}`, true
		case c.is("PATCH", "/sandboxes/"+forkID+"/resources"):
			return 200, `{"sandbox_id":"` + forkID + `","changes":{"cpu":{"status":"applied"}}}`, true
		}
		return 0, "", false
	})
	if out, err := run(t, "fork", "my-box", "-e", "STAGE=try", "-e", "A=1", "--cpu", "4"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	vars, _ := f.find("PUT", "/sandboxes/"+forkID+"/env")[0].Body["vars"].([]any)
	if len(vars) != 2 || vars[0].(map[string]any)["key"] != "A" || vars[1].(map[string]any)["value"] != "try" {
		t.Fatalf("vars = %v", vars)
	}
	if len(f.find("POST", "/sandboxes/"+forkID+"/env/sync")) != 1 {
		t.Fatal("the variables must be synced into the fork")
	}
	if b := f.find("PATCH", "/sandboxes/"+forkID+"/resources")[0].Body; b["cpu_count"] != float64(4) {
		t.Fatalf("resize = %v", b)
	}
	if len(f.find("PUT", boxPath+"/env")) != 0 {
		t.Fatal("the source must not be touched")
	}
	if _, err := run(t, "fork", "my-box", "-e", "NOEQUALS"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("a malformed -e must be a usage error before forking: %v", err)
	}
}

// A size the fork cannot take live: it is paused, recreated at the new size,
// the first copy is destroyed, and the variables go on the sandbox that stays.
func TestForkRecreatedAtANewSizeKeepsTheVariablesOnTheNewOne(t *testing.T) {
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	const forkID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	const sizedID = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	paused := false
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("POST", boxPath+"/fork"):
			return 201, `{"id":"` + forkID + `","state":"provisioning"}`, true
		case c.is("GET", "/sandboxes/"+forkID):
			if paused {
				return 200, `{"id":"` + forkID + `","state":"paused"}`, true
			}
			return 200, `{"id":"` + forkID + `","state":"running","ready":true}`, true
		case c.is("POST", "/sandboxes/"+forkID+"/pause"):
			paused = true
			return 200, `{"id":"` + forkID + `","state":"pausing"}`, true
		case c.is("PATCH", "/sandboxes/"+forkID+"/resources"):
			if c.Body["restart"] == true {
				return 202, `{"restarted":true,"mode":"restart","new_sandbox":{"id":"` + sizedID + `"}}`, true
			}
			return 409, `{"error":{"code":"RESIZE_REQUIRES_RESTART","message":"cannot apply live"}}`, true
		case c.is("GET", "/sandboxes/"+sizedID):
			return 200, `{"id":"` + sizedID + `","state":"running","ready":true}`, true
		case c.is("DELETE", "/sandboxes/"+forkID):
			return 204, ``, true
		case c.is("PUT", "/sandboxes/"+sizedID+"/env"), c.is("POST", "/sandboxes/"+sizedID+"/env/sync"):
			return 200, `{"data":{}}`, true
		}
		return 0, "", false
	})
	out, err := run(t, "fork", "my-box", "-e", "A=1", "--memory", "1024", "--json")
	if err != nil || !strings.Contains(out, sizedID) {
		t.Fatalf("%v %s", err, out)
	}
	if len(f.find("DELETE", "/sandboxes/"+forkID)) != 1 || len(f.find("PUT", "/sandboxes/"+sizedID+"/env")) != 1 {
		t.Fatal("the first copy must be destroyed and the variables set on the new one")
	}
	if len(f.find("PUT", "/sandboxes/"+forkID+"/env")) != 0 {
		t.Fatal("no variables on the destroyed copy")
	}
}

func TestTopShowsTheLiveListOrWhenTheKeptOneWasTaken(t *testing.T) {
	live := true
	sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if !c.is("GET", boxPath+"/top") {
			return 0, "", false
		}
		if live {
			return 200, `{"data":{"live":true,"sandbox_state":"running","processes":[{"pid":42,"user":"root","cpu_percent":3.5,"memory_percent":0.5,"rss_kb":2048,"elapsed_sec":3700,"command":"python3 -m http.server"}]}}`, true
		}
		return 200, `{"data":{"live":false,"sandbox_state":"paused","source":"stop","sampled_at":"2026-10-23T10:00:00Z","processes":[{"pid":42,"user":"root","cpu_percent":0,"memory_percent":0,"rss_kb":0,"elapsed_sec":5,"command":"sleep 100"}]}}`, true
	})
	out, err := run(t, "top", "my-box")
	if err != nil || !strings.Contains(out, "python3 -m http.server") || !strings.Contains(out, "1h1m") || strings.Contains(out, "kept") {
		t.Fatalf("%v %s", err, out)
	}
	live = false
	out, err = run(t, "top", "my-box")
	if err != nil || !strings.Contains(out, "The sandbox is paused") || !strings.Contains(out, "kept at its stop") || !strings.Contains(out, "sleep 100") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestBillingTopupOpensTheCheckoutAndWaitsForThePayment(t *testing.T) {
	defer commands.SetTopupWaitForTest(time.Millisecond, time.Second)()
	var opened string
	defer commands.SetOpenBrowserForTest(func(u string) error { opened = u; return nil })()
	reads := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/credits/balance"):
			reads++
			if reads < 3 {
				return 200, `{"balance_credits":100}`
			}
			return 200, `{"balance_credits":2100}`
		case c.is("POST", "/billing/checkout"):
			return 200, `{"checkout_url":"https://checkout.example/s1"}`
		}
		return 404, `{}`
	})
	out, err := run(t, "billing", "add-credits", "20")
	if err != nil || !strings.Contains(out, "Paid. The balance is now 2100 credits") {
		t.Fatalf("%v %s", err, out)
	}
	if opened != "https://checkout.example/s1" {
		t.Fatalf("opened = %q", opened)
	}
	if b := f.find("POST", "/billing/checkout")[0].Body; b["type"] != "topup" || b["amount"] != float64(2000) {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "billing", "add-credits", "twenty"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("a non-number must be a usage error: %v", err)
	}
}

func TestBillingPortalPrintsTheLinkWithNoOpen(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/account/billing/portal-session") {
			return 200, `{"url":"https://billing.example/p1"}`
		}
		return 404, `{}`
	})
	out, err := run(t, "billing", "portal", "--no-open")
	if err != nil || !strings.Contains(out, "https://billing.example/p1") {
		t.Fatalf("%v %s", err, out)
	}
}
