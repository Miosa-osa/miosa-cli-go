package commands_test

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestConnectionsListMergesSourcesAndReportsUnavailableOnes(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /settings/provider-keys": `{"data":[{"provider":"anthropic","key_preview":"sk-ant...1234"}],"providers_supported":["anthropic"]}`,
		"GET /integrations":           `{"data":[{"id":"i1","provider":"github","account":"octocat"}]}`,
		"GET /connected-accounts":     resp{403, `{"error":{"code":"FORBIDDEN","message":"requires connections:read"}}`},
		"GET /mcp-servers":            resp{401, `{"error":"Token is invalid or malformed","code":"INVALID_TOKEN"}`},
	})
	out, errOut, err := runSplit(t, "connections", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"anthropic", "api key", "sk-ant...1234", "github", "octocat"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "lacks the connections:read scope") || !strings.Contains(errOut, "--preset cli") {
		t.Errorf("a 403 must name the scope and the preset, got %q", errOut)
	}
	if !strings.Contains(errOut, "only accepts a console session") {
		t.Errorf("a 401 from an older server must say so, got %q", errOut)
	}
	jout, _, _ := runSplit(t, "connections", "list", "--json", "--kind", "apps")
	var v struct {
		Data        []map[string]any  `json:"data"`
		Unavailable map[string]string `json:"unavailable"`
	}
	if json.Unmarshal([]byte(jout), &v) != nil || len(v.Data) != 1 || v.Data[0]["kind"] != "apps" || len(v.Unavailable) != 0 {
		t.Fatalf("json = %s", jout)
	}
	if _, err := run(t, "connections", "list", "--kind", "nope"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad kind: %v", err)
	}
}

func TestConnectionsAddModelsAPIKeyComesFromStdinOnly(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"PUT /settings/provider-keys/anthropic": `{"data":{"provider":"anthropic"}}`})
	out, err := runIn(t, "sk-ant-secret-value\n", "connections", "add", "models", "anthropic", "--key-stdin")
	if err != nil || !strings.Contains(out, "Connected anthropic") {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "sk-ant-secret-value") {
		t.Fatal("the key must never be echoed")
	}
	put := f.find("PUT", "/settings/provider-keys/anthropic")
	if len(put) != 1 || put[0].Body["api_key"] != "sk-ant-secret-value" {
		t.Fatalf("put = %+v", put)
	}
	if _, err := runIn(t, "", "connections", "add", "models", "openai", "--key-stdin"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("empty key: %v", err)
	}
}

func TestConnectionsAddModelsSubscriptionKeyUsesAgentAccounts(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /agent-accounts/api-keys": resp{201, `{"data":{"id":"a1"}}`}})
	if _, err := runIn(t, "sk-live\n", "connections", "add", "models", "claude", "--key-stdin", "--label", "ci", "--workspace-id", "w1"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/agent-accounts/api-keys")[0].Body
	if b["provider"] != "claude_code" || b["api_key"] != "sk-live" || b["label"] != "ci" || b["workspace_id"] != "w1" {
		t.Fatalf("body = %v", b)
	}
}

func TestConnectionsSigninPasteCodeFlow(t *testing.T) {
	var polls int32
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/sandboxes/by-name/my-box"):
			return 200, `{"id":"` + boxID + `"}`
		case c.is("POST", "/agent-signins"):
			return 201, `{"data":{"id":"s1","provider":"claude_code","mode":"paste_code","status":"pending","verification_url":"https://claude.ai/oauth?x=1"}}`
		case c.is("GET", "/agent-signins/s1"):
			switch atomic.AddInt32(&polls, 1) {
			case 1:
				return 200, `{"data":{"id":"s1","provider":"claude_code","status":"awaiting_code"}}`
			case 2:
				return 200, `{"data":{"id":"s1","provider":"claude_code","status":"verifying"}}`
			}
			return 200, `{"data":{"id":"s1","provider":"claude_code","status":"succeeded"}}`
		case c.is("POST", "/agent-signins/s1/code"):
			return 200, `{"data":{"id":"s1","status":"verifying"}}`
		}
		return 404, `{}`
	})
	defer commands.SetSigninPollForTest(time.Millisecond)()
	out, errOut, err := func() (string, string, error) {
		return runSplitIn(t, "PASTED-CODE\n", "connections", "add", "models", "claude", "--sandbox", "my-box")
	}()
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "https://claude.ai/oauth?x=1") || !strings.Contains(out, "Connected claude_code") {
		t.Fatalf("out=%q err=%q", out, errOut)
	}
	if st := f.find("POST", "/agent-signins")[0].Body; st["provider"] != "claude_code" || st["sandbox_id"] != boxID {
		t.Fatalf("start body = %v", st)
	}
	if code := f.find("POST", "/agent-signins/s1/code"); len(code) != 1 || code[0].Body["code"] != "PASTED-CODE" {
		t.Fatalf("code = %+v", code)
	}
}

func TestConnectionsSigninDeviceCodeFailureCancelsTheSession(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/agent-signins"):
			return 201, `{"data":{"id":"s2","provider":"codex","mode":"device_code","status":"pending","verification_url":"https://auth.openai.com/device","user_code":"ABCD-1234"}}`
		case c.is("GET", "/agent-signins/s2"):
			return 200, `{"data":{"id":"s2","status":"expired","error":"code expired"}}`
		case c.is("DELETE", "/agent-signins/s2"):
			return 200, `{}`
		}
		return 404, `{}`
	})
	defer commands.SetSigninPollForTest(time.Millisecond)()
	_, errOut, err := runSplit(t, "connections", "add", "models", "chatgpt", "--computer", boxID)
	if err == nil || !strings.Contains(err.Error(), "expired: code expired") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(errOut, "ABCD-1234") {
		t.Fatalf("user code must be shown: %q", errOut)
	}
	if st := f.find("POST", "/agent-signins")[0].Body; st["provider"] != "codex" || st["computer_id"] != boxID {
		t.Fatalf("start body = %v", st)
	}
	if len(f.find("DELETE", "/agent-signins/s2")) != 1 {
		t.Fatal("a failed sign-in must be cancelled so the guest is cleaned up")
	}
}

func TestConnectionsSigninNeedsATargetAndExplainsSessionOnlyRoutes(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /agent-signins": resp{401, `{"error":"Token is invalid or malformed","code":"INVALID_TOKEN"}`}})
	if _, err := run(t, "connections", "add", "models", "claude"); err == nil || commands.ExitCode(err) != commands.ExitUsage || !strings.Contains(err.Error(), "--sandbox") {
		t.Fatalf("no target: %v", err)
	}
	_, err := run(t, "connections", "add", "models", "claude", "--sandbox", boxID)
	if err == nil || !strings.Contains(err.Error(), "only accepts a console session") {
		t.Fatalf("err = %v", err)
	}
	fakeRoutes(t, map[string]any{"POST /agent-signins": resp{403, `{"error":{"code":"FORBIDDEN","message":"requires connections:write"}}`}})
	_, err = run(t, "connections", "add", "models", "claude", "--sandbox", boxID)
	if err == nil || !strings.Contains(err.Error(), "connections:write") || !strings.Contains(err.Error(), "--preset cli") {
		t.Fatalf("403 must name the scope: %v", err)
	}
}

func TestConnectionsAddApps(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /integrations/github/start": `{"authorize_url":"https://github.com/login/oauth/authorize?client_id=x"}`})
	out, err := run(t, "connections", "add", "apps", "GitHub")
	if err != nil || !strings.Contains(out, "https://github.com/login/oauth/authorize") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestConnectionsAddToolsReadsHeaderSecretsFromTheEnvironment(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /mcp-servers": resp{201, `{"data":{"id":"m1"}}`}})
	t.Setenv("DOCS_TOKEN", "tok-123")
	out, err := run(t, "connections", "add", "tools", "--name", "docs", "--url", "https://mcp.example.com/sse", "--header-env", "Authorization=DOCS_TOKEN")
	if err != nil || strings.Contains(out, "tok-123") {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("POST", "/mcp-servers")[0].Body
	if b["name"] != "docs" || b["url"] != "https://mcp.example.com/sse" || b["headers"].(map[string]any)["Authorization"] != "tok-123" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "connections", "add", "tools", "--name", "x", "--url", "u", "--header-env", "A=UNSET_VAR_XYZ"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("empty env var: %v", err)
	}
	if _, err := run(t, "connections", "add", "tools", "--name", "x", "--url", "u", "--header-env", "novalue"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad header spec: %v", err)
	}
}

func TestConnectionsRmAndTest(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"DELETE /settings/provider-keys/anthropic": `{"deleted":true}`,
		"DELETE /integrations/github":              `{}`,
		"DELETE /mcp-servers/m1":                   `{}`,
		"POST /mcp-servers/m1/test":                `{"data":{"ok":true,"tools":["search"]}}`,
	})
	if _, err := run(t, "connections", "rm", "models", "anthropic"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("rm needs confirmation: %v", err)
	}
	for _, args := range [][]string{{"connections", "rm", "models", "Anthropic", "-y"}, {"connections", "rm", "apps", "github", "-y"}, {"connections", "rm", "tools", "m1", "-y"}} {
		if _, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if len(f.find("DELETE", "/settings/provider-keys/anthropic")) != 1 {
		t.Fatalf("provider names are case-insensitive: %+v", f.calls)
	}
	if out, err := run(t, "connections", "test", "m1"); err != nil || !strings.Contains(out, "search") {
		t.Fatalf("test: %v\n%s", err, out)
	}
	if _, err := run(t, "connections", "rm", "widgets", "x", "-y"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := run(t, "connections", "rm", "models", "claude", "-y"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("a subscription is removed by id: %v", err)
	}
	if _, err := run(t, "connections", "share", "x"); err == nil || !strings.Contains(err.Error(), "not exposed by the API yet") {
		t.Fatalf("share: %v", err)
	}
}
