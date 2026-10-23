package commands_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestLoginInTwoStepsForAnAgent(t *testing.T) {
	var starts, polls int32
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Method + " " + c.Path {
		case "POST /auth/cli/start":
			n := atomic.AddInt32(&starts, 1)
			code := "CODE-" + string(rune('0'+n))
			return 200, `{"device_code":"dev-secret-` + code + `","user_code":"` + code + `","verification_uri":"https://miosa.ai/cli/auth","verification_uri_complete":"https://miosa.ai/cli/auth?code=` + code + `","expires_in":600,"interval":1}`
		case "POST /auth/cli/token":
			if atomic.AddInt32(&polls, 1) < 2 {
				return 428, `{"error":"authorization_pending"}`
			}
			return 200, `{"api_key":"msk_u_agent_key","key":{"id":"k1"},"tenant":{"id":"t-1","slug":"acme","name":"Acme Inc"}}`
		case "GET /platform/tenants/current":
			return 200, tenantJSON
		}
		return 404, `{}`
	})
	os.Unsetenv("MIOSA_API_KEY")
	defer commands.SetSleepForTest(func(context.Context, time.Duration) error { return nil })()

	out, err := run(t, "login", "--no-wait", "--json")
	if err != nil {
		t.Fatalf("start: %v %s", err, out)
	}
	var first struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal([]byte(out), &first) != nil || first.Data["user_code"] != "CODE-1" || first.Data["url"] != "https://miosa.ai/cli/auth?code=CODE-1" || first.Data["status"] != "pending" {
		t.Fatalf("start = %s", out)
	}
	if strings.Contains(out, "dev-secret") {
		t.Fatal("the device code must never be printed")
	}
	pending := filepath.Join(os.Getenv("HOME"), ".miosa", "pending-login.json")
	if st, err := os.Stat(pending); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("pending file: %v %v", st, err)
	}

	again, _ := run(t, "login", "--no-wait", "--json")
	if !strings.Contains(again, "CODE-1") || starts != 1 {
		t.Fatalf("a rerun must show the same pending link: %s (starts=%d)", again, starts)
	}
	replaced, _ := run(t, "login", "--no-wait", "--new", "--json")
	if !strings.Contains(replaced, "CODE-2") || !strings.Contains(replaced, "login_replaced") {
		t.Fatalf("--new: %s", replaced)
	}

	if out, err := run(t, "login", "--wait"); err != nil {
		t.Fatalf("wait: %v %s", err, out)
	}
	if tok := f.find("POST", "/auth/cli/token"); tok[0].Body["device_code"] != "dev-secret-CODE-2" {
		t.Fatalf("polled with %v", tok[0].Body)
	}
	if !strings.Contains(readConfig(t), "msk_u_agent_key") {
		t.Fatal("the issued key was not stored")
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("the pending sign-in must be removed once used")
	}
	if _, err := run(t, "login", "--wait"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("--wait with nothing pending: %v", err)
	}
}
