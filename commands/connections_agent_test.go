package commands_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestConnectionsAddKeyForAnEndUserIsAScopedAgentCredential(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings":              `{"data":{"scope":{"level":"external_user"}}}`,
		"PUT /agents/credentials/anthropic": `{"data":{"id":"anthropic","scope":"external_user"}}`})
	out, err := runIn(t, "sk-ant-alice-1234\n", "connections", "add", "models", "anthropic", "--external-user", "cus_123", "--workspace-id", "w1", "--key-stdin")
	if err != nil || !strings.Contains(out, "Connected anthropic") {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "sk-ant-alice-1234") {
		t.Fatal("the key must never be echoed")
	}
	b := f.find("PUT", "/agents/credentials/anthropic")[0].Body
	if b["api_key"] != "sk-ant-alice-1234" || b["external_user_id"] != "cus_123" || b["workspace_id"] != "w1" {
		t.Fatalf("body = %v", b)
	}
	if len(f.find("PUT", "/settings/provider-keys/anthropic")) != 0 {
		t.Fatal("an end user's key must not become the organization's gateway key")
	}
}

func TestConnectionsAddClaudeKeyForAnEndUserIsTheAnthropicKey(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings":              `{"data":{"scope":{"level":"external_user"}}}`,
		"PUT /agents/credentials/anthropic": `{}`, "PUT /agents/credentials/openai": `{}`})
	if _, err := runIn(t, "k1\n", "connections", "add", "models", "claude", "--external-user", "u", "--key-stdin"); err != nil {
		t.Fatal(err)
	}
	if _, err := runIn(t, "k2\n", "connections", "add", "models", "chatgpt", "--external-user", "u", "--key-stdin"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("PUT", "/agents/credentials/anthropic")) != 1 || len(f.find("PUT", "/agents/credentials/openai")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestConnectionsAddKeyRefusesAProviderThatIsNotAnAgentCredentialAtAScope(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := runIn(t, "k\n", "connections", "add", "models", "groq", "--external-user", "u", "--key-stdin")
	if err == nil || commands.ExitCode(err) != commands.ExitUsage || !strings.Contains(err.Error(), "not an agent credential") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionsAddSubscriptionNoWaitPrintsWhatToShowAndLeavesTheSessionOpen(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings": `{"data":{"scope":{"level":"external_user"}}}`,
		"POST /agent-signins":  resp{201, `{"data":{"id":"s1","provider":"codex","mode":"device_code","status":"awaiting_code","verification_url":"https://auth.openai.com/codex/device","user_code":"ABCD-EFGH","expires_at":"2026-10-16T12:15:00Z"}}`},
	})
	out, err := run(t, "connections", "add", "models", "chatgpt", "--external-user", "cus_123", "--no-wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if json.Unmarshal([]byte(out), &v) != nil || v["id"] != "s1" || v["user_code"] != "ABCD-EFGH" || v["verification_url"] != "https://auth.openai.com/codex/device" {
		t.Fatalf("out = %s", out)
	}
	b := f.find("POST", "/agent-signins")[0].Body
	if b["provider"] != "codex" || b["external_user_id"] != "cus_123" {
		t.Fatalf("body = %v", b)
	}
	if _, ok := b["sandbox_id"]; ok {
		t.Fatalf("an end user's sign-in must not run in a machine of the builder's: %v", b)
	}
	if len(f.find("DELETE", "/agent-signins/s1")) != 0 {
		t.Fatal("--no-wait must leave the session open for the caller's UI")
	}
}

func TestConnectionsAddSubscriptionNoWaitTextTellsWhatToDoNext(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /agents/settings": `{"data":{"scope":{"level":"external_user"}}}`,
		"POST /agent-signins":  resp{201, `{"data":{"id":"s9","provider":"claude_code","mode":"paste_code","status":"awaiting_code","verification_url":"https://claude.ai/oauth/authorize?state=x"}}`},
	})
	out, err := run(t, "connections", "add", "models", "claude", "--external-user", "cus_123", "--no-wait")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://claude.ai/oauth/authorize", "signin code s9 --code-stdin --external-user cus_123"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := run(t, "connections", "add", "models", "anthropic", "--no-wait"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("--no-wait is for plans: %v", err)
	}
}

func TestConnectionsSigninFollowUp(t *testing.T) {
	var polls int32
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/agent-signins/s1"):
			if atomic.AddInt32(&polls, 1) < 3 {
				return 200, `{"data":{"id":"s1","provider":"codex","status":"awaiting_code"}}`
			}
			return 200, `{"data":{"id":"s1","provider":"codex","status":"succeeded","account":{"account_email":"bob@x.example","account_plan":"plus"}}}`
		case c.is("POST", "/agent-signins/s2/code"):
			return 200, `{"data":{"id":"s2","provider":"claude_code","status":"succeeded","account":{"account_email":"alice@customer.example","account_plan":"max"}}}`
		case c.is("DELETE", "/agent-signins/s3"):
			return 200, `{}`
		}
		return 404, `{}`
	})
	defer commands.SetSigninPollForTest(time.Millisecond)()

	out, err := run(t, "connections", "signin", "wait", "s1", "--external-user", "cus_9")
	if err != nil || !strings.Contains(out, "bob@x.example") || !strings.Contains(out, "plus") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range f.find("GET", "/agent-signins/s1") {
		if qv(c).Get("external_user_id") != "cus_9" {
			t.Fatalf("the scope must travel with every poll: %+v", c)
		}
	}

	out, err = runIn(t, "abc#def\n", "connections", "signin", "code", "s2", "--code-stdin", "--external-user", "cus_123")
	if err != nil || !strings.Contains(out, "alice@customer.example") {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("POST", "/agent-signins/s2/code")[0].Body
	if b["code"] != "abc#def" || b["external_user_id"] != "cus_123" {
		t.Fatalf("body = %v", b)
	}

	if _, err := run(t, "connections", "signin", "cancel", "s3", "--external-user", "cus_123"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "connections", "signin", "code", "s2"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("a code must be given: %v", err)
	}
}

func TestConnectionsListAtAScopeReadsTheReadModel(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /connections": `{"data":[
			{"family":"models","provider":"anthropic","id":"anthropic","status":"connected","preview":"sk-ant...1234","scope":{"level":"external_user"},"inherited":false},
			{"family":"models","provider":"openai","id":"openai","status":"connected","preview":"sk-...9999","scope":{"level":"workspace"},"inherited":true}],"total":2}`,
	})
	out, err := run(t, "connections", "list", "--external-user", "cus_123", "--workspace-id", "w1", "--kind", "models")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"anthropic", "sk-ant...1234", "external_user", "workspace (inherited)"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	q := qv(f.find("GET", "/connections")[0])
	if q.Get("external_user_id") != "cus_123" || q.Get("workspace_id") != "w1" || q.Get("family") != "models" {
		t.Fatalf("query = %v", q)
	}
}

func TestConnectionsRmAtAScopeRemovesExactlyThatScope(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings":                           `{"data":{"scope":{"level":"external_user"}}}`,
		"DELETE /agents/credentials/claude_subscription": `{"data":{"scope":"workspace","inherited":true}}`,
		"DELETE /agents/credentials/anthropic":           `{"data":{}}`,
	})
	if _, err := run(t, "connections", "rm", "models", "claude", "--external-user", "cus_123", "-y"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "connections", "rm", "models", "anthropic", "--external-user", "cus_123", "-y"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if c.Method == "DELETE" && qv(c).Get("external_user_id") != "cus_123" {
			t.Fatalf("scope missing from %+v", c)
		}
	}
	if len(f.find("DELETE", "/agents/credentials/claude_subscription")) != 1 {
		t.Fatalf("a plan name maps to its credential: %+v", f.calls)
	}
}

func TestConnectionsTestModelsReportsWhetherTheCredentialWorks(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /agents/credentials/anthropic/test": `{"data":{"credential":"anthropic","ok":true,"check":"provider","http_status":200,"scope":"external_user"}}`,
		"POST /agents/credentials/openai/test":    `{"data":{"credential":"openai","ok":false,"check":"provider","http_status":401,"detail":"rejected","scope":"external_user"}}`,
		"POST /agents/credentials/mistral/test":   `{"data":{"credential":"mistral","ok":null,"check":"unreachable","detail":"provider_unreachable","scope":"workspace"}}`,
	})
	out, err := run(t, "connections", "test", "models", "anthropic", "--external-user", "cus_123")
	if err != nil || !strings.Contains(out, "anthropic works") {
		t.Fatalf("%v\n%s", err, out)
	}
	if b := f.find("POST", "/agents/credentials/anthropic/test")[0].Body; b["external_user_id"] != "cus_123" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "connections", "test", "models", "openai", "--external-user", "cus_123"); err == nil || !strings.Contains(err.Error(), "does not work: rejected") {
		t.Fatalf("a rejected key must fail the command: %v", err)
	}
	if out, err := run(t, "connections", "test", "models", "mistral"); err != nil || !strings.Contains(out, "could not be checked") {
		t.Fatalf("an unreachable provider is not the key's fault: %v\n%s", err, out)
	}
	if _, err := run(t, "connections", "test", "models"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("a provider is required: %v", err)
	}
}

func TestConnectionsToolKeys(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings":                 `{"data":{"scope":{"level":"external_user"}}}`,
		"GET /agents/tool-keys":                `{"data":[{"name":"APIFY_TOKEN","connected":true,"scope":"workspace","inherited":true,"preview":"apify_...cret","servers":[{"name":"Apify"}]},{"name":"REFERO_KEY","connected":false,"servers":[{"name":"Refero"}]}]}`,
		"PUT /agents/tool-keys/APIFY_TOKEN":    `{"data":{"name":"APIFY_TOKEN","scope":"external_user","connected":true}}`,
		"DELETE /agents/tool-keys/APIFY_TOKEN": `{"data":{}}`,
	})
	out, err := run(t, "connections", "tool-keys", "list", "--external-user", "cus_123")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"APIFY_TOKEN", "workspace (inherited)", "REFERO_KEY", "missing", "Apify"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if q := qv(f.find("GET", "/agents/tool-keys")[0]); q.Get("external_user_id") != "cus_123" {
		t.Fatalf("query = %v", q)
	}

	out, err = runIn(t, "apify_api_secret\n", "connections", "tool-keys", "set", "APIFY_TOKEN", "--key-stdin", "--external-user", "cus_123")
	if err != nil || strings.Contains(out, "apify_api_secret") || !strings.Contains(out, "Set APIFY_TOKEN") {
		t.Fatalf("%v\n%s", err, out)
	}
	if b := f.find("PUT", "/agents/tool-keys/APIFY_TOKEN")[0].Body; b["value"] != "apify_api_secret" || b["external_user_id"] != "cus_123" {
		t.Fatalf("body = %v", b)
	}
	if _, err := runIn(t, "", "connections", "tool-keys", "set", "APIFY_TOKEN", "--key-stdin"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("empty value: %v", err)
	}

	if _, err := run(t, "connections", "tool-keys", "rm", "APIFY_TOKEN", "--external-user", "cus_123", "-y"); err != nil {
		t.Fatal(err)
	}
	if q := qv(f.find("DELETE", "/agents/tool-keys/APIFY_TOKEN")[0]); q.Get("external_user_id") != "cus_123" {
		t.Fatalf("query = %v", q)
	}
}

func TestConnectionsHarnessesShowWhichConnectionsPowerEach(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings": `{"data":{"harnesses":[
			{"id":"claude-code","label":"Claude Code","status":{"state":"ready"},"auth_methods":[{"id":"claude_subscription","connected":true},{"id":"anthropic","connected":false}]},
			{"id":"opencode","label":"OpenCode","status":{"state":"ready"},"providers":[{"id":"chatgpt_subscription","connected":true,"enabled":true},{"id":"openrouter","connected":true,"enabled":false}]},
			{"id":"pi","label":"pi","status":{"state":"no_credential"},"providers":[]}]}}`,
	})
	out, err := run(t, "connections", "harnesses", "--external-user", "cus_123")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude-code", "claude_subscription", "opencode", "chatgpt_subscription", "no credential"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "openrouter") {
		t.Errorf("a connected-but-not-enabled provider is not usable:\n%s", out)
	}
	if q := qv(f.find("GET", "/agents/settings")[0]); q.Get("external_user_id") != "cus_123" {
		t.Fatalf("query = %v", q)
	}
}

// qv parses a recorded request's raw query string.
func qv(c call) url.Values {
	v, _ := url.ParseQuery(c.Query)
	return v
}

func TestConnectionsEndUserWritesRefuseAServerThatWouldIgnoreTheEndUser(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings":                           `{"data":{"scope":{"level":"organization"}}}`,
		"PUT /agents/credentials/anthropic":              `{}`,
		"POST /agent-signins":                            resp{201, `{"data":{"id":"s1"}}`},
		"DELETE /agents/credentials/claude_subscription": `{}`,
		"PUT /agents/tool-keys/T":                        `{}`,
		"DELETE /agents/tool-keys/T":                     `{}`,
	})
	for _, args := range [][]string{
		{"connections", "add", "models", "anthropic", "--external-user", "u", "--key-stdin"},
		{"connections", "add", "models", "claude", "--external-user", "u", "--no-wait"},
		{"connections", "rm", "models", "claude", "--external-user", "u", "-y"},
		{"connections", "tool-keys", "set", "T", "--external-user", "u", "--key-stdin"},
		{"connections", "tool-keys", "rm", "T", "--external-user", "u", "-y"},
	} {
		_, err := runIn(t, "value\n", args...)
		if err == nil || !strings.Contains(err.Error(), "does not support connections for end users") {
			t.Fatalf("%v: err = %v", args, err)
		}
	}
	for _, c := range f.calls {
		if c.Method != "GET" {
			t.Fatalf("a write left the client: %+v", c)
		}
	}
}
