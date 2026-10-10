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
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

const tenantJSON = `{"id":"t-1","name":"Acme Inc","slug":"acme","plan":"pro","plan_name":"Pro","credit_balance":12345}`

func readConfig(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".miosa", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoginKeyStdinStoresKeyAndOrg(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /platform/tenants/current": tenantJSON})
	out, err := runIn(t, "msk_u_abcdef123456\n", "login", "--key-stdin")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	cfg := readConfig(t)
	if !strings.Contains(cfg, `api_key = "msk_u_abcdef123456"`) || !strings.Contains(cfg, `tenant = "acme"`) {
		t.Fatalf("config = %s", cfg)
	}
	if strings.Contains(out, "abcdef123456") {
		t.Fatalf("login echoed the key: %s", out)
	}
	if !strings.Contains(out, "Acme Inc") {
		t.Fatalf("output = %s", out)
	}
	if len(f.find("GET", "/platform/tenants/current")) != 1 {
		t.Fatal("key must be verified before it is stored")
	}
}

func TestLoginNonTTYStdinReadsKeyWithoutFlag(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /platform/tenants/current": tenantJSON})
	if _, err := runIn(t, "msk_u_piped\n", "login"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readConfig(t), "msk_u_piped") {
		t.Fatal("key not stored")
	}
}

func TestLoginRejectsBadKeyAndStoresNothing(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /platform/tenants/current": resp{401, `{"error":"Token is invalid or malformed","code":"INVALID_TOKEN"}`}})
	_, err := runIn(t, "msk_u_bad\n", "login", "--key-stdin")
	if err == nil || !strings.Contains(err.Error(), "key validation failed") || !strings.Contains(err.Error(), "INVALID_TOKEN") {
		t.Fatalf("err = %v", err)
	}
	if commands.ExitCode(err) != commands.ExitAuth {
		t.Fatalf("exit = %d", commands.ExitCode(err))
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("HOME"), ".miosa", "config.toml")); statErr == nil {
		t.Fatal("a rejected key must not be saved")
	}
}

func TestLoginEmptyStdinFails(t *testing.T) {
	fakeRoutes(t, nil)
	if _, err := runIn(t, "", "login", "--key-stdin"); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginIntoNamedProfileKeepsDefault(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /platform/tenants/current": tenantJSON})
	writeConfigFile(t, "api_key = \"msk_u_default\"\n")
	if _, err := runIn(t, "msk_u_work\n", "login", "--key-stdin", "--profile", "work"); err != nil {
		t.Fatal(err)
	}
	cfg := readConfig(t)
	if !strings.Contains(cfg, `api_key = "msk_u_default"`) || !strings.Contains(cfg, "[profiles.work]") || !strings.Contains(cfg, "msk_u_work") {
		t.Fatalf("config = %s", cfg)
	}
}

func TestDeviceLoginPollsUntilApproved(t *testing.T) {
	var polls int32
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Method + " " + c.Path {
		case "POST /auth/cli/start":
			return 200, `{"device_code":"dev-secret","user_code":"ABCD-EFGH","verification_uri":"https://miosa.ai/cli/auth","verification_uri_complete":"https://miosa.ai/cli/auth?code=ABCD-EFGH","expires_in":600,"interval":1}`
		case "POST /auth/cli/token":
			if atomic.AddInt32(&polls, 1) < 3 {
				return 428, `{"error":"authorization_pending"}`
			}
			return 200, `{"api_key":"msk_u_issued_by_server","key":{"id":"k1"},"tenant":{"id":"t-1","slug":"acme","name":"Acme Inc"}}`
		case "GET /platform/tenants/current":
			return 200, tenantJSON
		}
		return 404, `{}`
	})
	var slept []time.Duration
	defer commands.SetSleepForTest(func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil })()
	var opened string
	defer commands.SetOpenBrowserForTest(func(u string) error { opened = u; return nil })()

	out, err := runIn(t, "", "login", "--device")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if opened != "https://miosa.ai/cli/auth?code=ABCD-EFGH" {
		t.Fatalf("browser opened %q", opened)
	}
	if len(slept) != 3 || slept[0] != time.Second {
		t.Fatalf("poll sleeps = %v", slept)
	}
	if !strings.Contains(readConfig(t), "msk_u_issued_by_server") {
		t.Fatal("issued key not stored")
	}
	tok := f.find("POST", "/auth/cli/token")
	if len(tok) != 3 || tok[0].Body["device_code"] != "dev-secret" {
		t.Fatalf("token polls = %+v", tok)
	}
}

func TestDeviceLoginFailureModes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"denied", 403, `{"error":"access_denied"}`, "denied"},
		{"expired", 410, `{"error":"expired_token"}`, "expired"},
		{"consumed", 409, `{"error":"already_consumed"}`, "already used"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newFakeAPI(t, func(c call) (int, string) {
				if c.Path == "/auth/cli/start" {
					return 200, `{"device_code":"d","user_code":"U","verification_uri":"https://x/cli","expires_in":600,"interval":1}`
				}
				return tc.status, tc.body
			})
			defer commands.SetSleepForTest(func(context.Context, time.Duration) error { return nil })()
			defer commands.SetOpenBrowserForTest(func(string) error { return nil })()
			_, err := runIn(t, "", "login", "--device", "--no-browser")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDeviceLoginNoBrowserPrintsURL(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		switch c.Path {
		case "/auth/cli/start":
			return 200, `{"device_code":"d","user_code":"WXYZ","verification_uri":"https://miosa.ai/cli/auth","verification_uri_complete":"https://miosa.ai/cli/auth?code=WXYZ","expires_in":600,"interval":1}`
		case "/auth/cli/token":
			return 200, `{"api_key":"msk_u_k","tenant":{"slug":"acme"}}`
		}
		return 200, tenantJSON
	})
	defer commands.SetSleepForTest(func(context.Context, time.Duration) error { return nil })()
	opened := false
	defer commands.SetOpenBrowserForTest(func(string) error { opened = true; return nil })()
	out, err := runIn(t, "", "login", "--device", "--no-browser")
	if err != nil {
		t.Fatal(err)
	}
	if opened {
		t.Fatal("--no-browser must not open a browser")
	}
	if !strings.Contains(out, "WXYZ") {
		t.Fatalf("code not shown: %s", out)
	}
}

func TestLogoutClearsProfileKeyOnly(t *testing.T) {
	fakeRoutes(t, nil)
	writeConfigFile(t, "api_key = \"msk_u_default\"\n[profiles.work]\napi_key = \"msk_u_work\"\n")
	if _, err := run(t, "logout", "--profile", "work"); err != nil {
		t.Fatal(err)
	}
	cfg := readConfig(t)
	if strings.Contains(cfg, "msk_u_work") || !strings.Contains(cfg, "msk_u_default") {
		t.Fatalf("config = %s", cfg)
	}
}

func TestLogoutAllDeletesFile(t *testing.T) {
	fakeRoutes(t, nil)
	path := writeConfigFile(t, "api_key = \"msk_u_default\"\n[profiles.work]\napi_key = \"msk_u_work\"\n")
	if _, err := run(t, "logout", "--all"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config should be gone: %v", err)
	}
}

func TestWhoami(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /platform/tenants/current": tenantJSON})
	t.Setenv("MIOSA_API_KEY", "msk_u_abcdefghijklmnop")
	out, err := run(t, "whoami")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Acme Inc (acme)", "Pro", "12345", "$123.45", "msk_u_...mnop"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami missing %q:\n%s", want, out)
		}
	}
	jout, err := run(t, "whoami", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(jout), &got); err != nil || got["slug"] != "acme" || got["key"] == "msk_u_abcdefghijklmnop" {
		t.Fatalf("json = %s (%v)", jout, err)
	}
}

func TestWhoamiUsesTheKeyEndpoint(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /whoami": `{"user":{"id":"u1","email":"ada@acme.test","name":"Ada"},"organization":{"id":"o1","name":"Acme Inc","slug":"acme"},` +
			`"workspace":{"id":"w1","name":"Prod","slug":"prod"},"plan":{"name":"Enterprise"},` +
			`"auth":{"method":"api_key","scopes":["sandboxes:read","agents:run"],"unrestricted":false,"key":{"id":"k1","name":"ci"}}}`,
		"GET /platform/tenants/current": tenantJSON,
	})
	t.Setenv("MIOSA_API_KEY", "msk_u_abcdefghijklmnop")
	out, err := run(t, "whoami")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ada <ada@acme.test>", "Acme Inc (acme)", "Enterprise", "prod (key is bound to it)", "2 scopes"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami missing %q:\n%s", want, out)
		}
	}
	jout, err := run(t, "whoami", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(jout), &got); err != nil || got["auth"] == nil || got["user"] == nil {
		t.Fatalf("json = %s (%v)", jout, err)
	}
}

func TestWhoamiSignedOut(t *testing.T) {
	fakeRoutes(t, nil)
	t.Setenv("MIOSA_API_KEY", "")
	_, err := run(t, "whoami")
	if err == nil || commands.ExitCode(err) != commands.ExitAuth || !strings.Contains(err.Error(), "miosa login") {
		t.Fatalf("err = %v exit=%d", err, commands.ExitCode(err))
	}
}

func TestProfileLifecycle(t *testing.T) {
	fakeRoutes(t, nil)
	writeConfigFile(t, "api_key = \"msk_u_default\"\ntenant = \"osa\"\n")

	if out, err := run(t, "profile", "add", "work", "--api-url", "https://staging.example/api/v1"); err != nil || !strings.Contains(out, "login --profile work") {
		t.Fatalf("add: %v %s", err, out)
	}
	if _, err := run(t, "profile", "add", "work"); err == nil {
		t.Fatal("duplicate add must fail")
	}
	if _, err := run(t, "profile", "add", "bad name"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad name: %v", err)
	}
	out, err := run(t, "profile", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Active string           `json:"active"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Data) != 2 || list.Active != "default" {
		t.Fatalf("list = %s (%v)", out, err)
	}
	if _, err := run(t, "profile", "use", "work"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readConfig(t), `active_profile = "work"`) {
		t.Fatal("active profile not stored")
	}
	if out, err := run(t, "profile", "show"); err != nil || !strings.Contains(out, "work") || !strings.Contains(out, "staging.example") {
		t.Fatalf("show: %v %s", err, out)
	}
	if _, err := run(t, "profile", "use", "nope"); err == nil {
		t.Fatal("unknown profile must fail")
	}
	if _, err := run(t, "profile", "use", "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "profile", "rm", "default"); err == nil {
		t.Fatal("default cannot be removed")
	}
	if _, err := run(t, "profile", "rm", "work"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readConfig(t), "profiles.work") {
		t.Fatal("profile not removed")
	}
}

func TestProfileFlagSelectsCredentials(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) { return 200, `{"data":[]}` })
	t.Setenv("MIOSA_API_KEY", "")
	t.Setenv("MIOSA_BASE_URL", "")
	writeConfigFile(t, "api_key = \"msk_u_default\"\napi_url = \""+f.srv.URL+"\"\n[profiles.work]\napi_key = \"msk_u_work\"\n")
	if _, err := run(t, "list", "--profile", "work"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("GET", "/sandboxes")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestUnknownProfileFailsInsteadOfFallingBack(t *testing.T) {
	fakeRoutes(t, nil)
	writeConfigFile(t, "api_key = \"msk_u_default\"\n")
	_, err := run(t, "list", "--profile", "typo")
	if err == nil || !strings.Contains(err.Error(), `profile "typo" not found`) {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigGetSetList(t *testing.T) {
	fakeRoutes(t, nil)
	if _, err := run(t, "config", "set", "default_workspace", "acme-prod"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "config", "get", "default_workspace")
	if err != nil || strings.TrimSpace(out) != "acme-prod" {
		t.Fatalf("get: %v %q", err, out)
	}
	if _, err := run(t, "config", "get", "api_key"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("api_key must not be readable here: %v", err)
	}
	if out, err := run(t, "config", "list"); err != nil || strings.Contains(out, "api_key") || !strings.Contains(out, "default_workspace = acme-prod") {
		t.Fatalf("list: %v %s", err, out)
	}
	if out, err := run(t, "config", "path"); err != nil || !strings.HasSuffix(strings.TrimSpace(out), filepath.Join(".miosa", "config.toml")) {
		t.Fatalf("path: %v %s", err, out)
	}
}

func TestConfigSetIsPerProfile(t *testing.T) {
	fakeRoutes(t, nil)
	writeConfigFile(t, "api_key = \"d\"\n[profiles.work]\napi_key = \"w\"\n")
	if _, err := run(t, "config", "set", "current_sandbox", "box", "--profile", "work"); err != nil {
		t.Fatal(err)
	}
	f, _ := config.LoadFile()
	w, _ := f.Get("work")
	if w.CurrentSandbox != "box" || f.Config.CurrentSandbox != "" {
		t.Fatalf("file = %+v", f)
	}
}

func TestDoctorReportsHealthyAndFailing(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Path {
		case "/health":
			return 200, `{"status":"ok"}`
		case "/platform/tenants/current":
			return 200, tenantJSON
		}
		return 404, `{}`
	})
	_ = f
	out, err := run(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	for _, want := range []string{"cli", "api", "auth", "Acme Inc"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}

	newFakeAPI(t, func(c call) (int, string) {
		if c.Path == "/health" {
			return 200, `{}`
		}
		return 401, `{"error":"Token is invalid or malformed","code":"INVALID_TOKEN"}`
	})
	out, err = run(t, "doctor", "--json")
	if err == nil || commands.ExitCode(err) != commands.ExitGeneral {
		t.Fatalf("failing doctor must exit non-zero: %v", err)
	}
	var rep struct {
		OK     bool `json:"ok"`
		Checks []struct{ Name, Status, Detail string }
	}
	if json.Unmarshal([]byte(out), &rep) != nil || rep.OK {
		t.Fatalf("report = %s", out)
	}
	found := false
	for _, c := range rep.Checks {
		if c.Name == "auth" && c.Status == "fail" && strings.Contains(c.Detail, "INVALID_TOKEN") {
			found = true
		}
	}
	if !found {
		t.Fatalf("auth failure missing: %s", out)
	}
}

func TestDoctorSignedOut(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) { return 200, `{}` })
	t.Setenv("MIOSA_API_KEY", "")
	out, err := run(t, "doctor")
	if err == nil || !strings.Contains(out, "not signed in") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestOpenPrintsConsoleURLs(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /sandboxes/by-name/my-box": `{"id":"8ba07e95-5085-4600-b758-60b6951c5786"}`})
	t.Setenv("MIOSA_CONSOLE_URL", "https://console.test")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"open", "--print"}, "https://console.test"},
		{[]string{"open", "--print", "sandbox"}, "https://console.test/sandboxes"},
		{[]string{"open", "--print", "sandbox", "my-box"}, "https://console.test/sandboxes/8ba07e95-5085-4600-b758-60b6951c5786"},
		{[]string{"open", "--print", "run", "r-1"}, "https://console.test/agents/runs?run=r-1"},
		{[]string{"open", "--print", "billing"}, "https://console.test/billing"},
	}
	for _, tc := range cases {
		out, err := run(t, tc.args...)
		if err != nil || strings.TrimSpace(out) != tc.want {
			t.Errorf("%v => %q (%v), want %q", tc.args, out, err, tc.want)
		}
	}
	if _, err := run(t, "open", "--print", "nonsense"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestConsoleURLDerivation(t *testing.T) {
	t.Setenv("MIOSA_CONSOLE_URL", "")
	if got := commands.ConsoleURLForTest("https://api.miosa.ai/api/v1"); got != "https://miosa.ai" {
		t.Fatalf("got %s", got)
	}
	if got := commands.ConsoleURLForTest("http://127.0.0.1:4000/api/v1"); got != "http://127.0.0.1:4000" {
		t.Fatalf("got %s", got)
	}
}

func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"short":                  "****",
		"msk_u_abcdefghijklmnop": "msk_u_...mnop",
	}
	for in, want := range cases {
		if got := commands.MaskKeyForTest(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}
