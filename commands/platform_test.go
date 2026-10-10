package commands_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const (
	depID = "7f9b1e48-787c-4e91-ad3c-cf746cc3da3d"
	wsID  = "4dbd11d9-ee87-4b99-a5dc-c6907e37a526"
)

// platformCase is one command and the request it must produce.
type platformCase struct {
	args     []string
	method   string
	path     string
	reply    string
	query    map[string]string
	body     map[string]any
	wantOut  string
	needsYes bool // the command must refuse without --yes
}

func TestPlatformCommandsSendTheRightRequests(t *testing.T) {
	list := func(row string) string { return `{"data":[` + row + `]}` }
	cases := []platformCase{
		// deployments
		{args: []string{"deploy", "list", "--workspace-id", wsID, "-n", "3"}, method: "GET", path: "/deployments", reply: list(`{"id":"` + depID + `","name":"My App","slug":"my-app","state":"running","public_url":"https://my-app.example"}`), query: map[string]string{"workspace_id": wsID, "limit": "3"}, wantOut: "https://my-app.example"},
		{args: []string{"deploy", "get", "my-app"}, method: "GET", path: "/deployments/" + depID, reply: `{"data":{"id":"` + depID + `","name":"My App","state":"running","branch":"main"}}`, wantOut: "main"},
		{args: []string{"deploy", "logs", "my-app", "-n", "5"}, method: "GET", path: "/deployments/" + depID + "/logs", reply: `{"data":{"lines":["listening on 3000"]}}`, query: map[string]string{"lines": "5"}, wantOut: "listening on 3000"},
		{args: []string{"deploy", "requests", "my-app"}, method: "GET", path: "/deployments/" + depID + "/request-logs", reply: `{"logs":[{"method":"GET","path":"/","status":200,"duration_ms":4}]}`, wantOut: "200"},
		{args: []string{"deploy", "observe", "my-app"}, method: "GET", path: "/deployments/" + depID + "/observability", reply: `{"summary":{"invocations":330,"error_count":0,"duration_ms":{"p50":3,"p95":9}}}`, wantOut: "330"},
		{args: []string{"deploy", "versions", "my-app"}, method: "GET", path: "/deployments/" + depID + "/versions", reply: list(`{"id":"160feacd-0f49-4717-bb09-fff668f8ccc6","version_number":3,"state":"ready"}`), wantOut: "160feacd"},
		{args: []string{"deploy", "releases", "my-app"}, method: "GET", path: "/deployments/" + depID + "/releases", reply: list(`{"id":"e63d6a94-304c-41e4-a028-ef37b2a3d906","state":"ready","port":8080}`), wantOut: "8080"},
		{args: []string{"deploy", "promote", "my-app", "rel-1"}, method: "POST", path: "/deployments/" + depID + "/releases/rel-1/promote", reply: `{}`, wantOut: "Promoted release rel-1"},
		{args: []string{"deploy", "redeploy", "my-app"}, method: "POST", path: "/deployments/" + depID + "/redeploy", reply: `{}`, wantOut: "Redeploying my-app"},
		{args: []string{"deploy", "rollback", "my-app", "--to", "v-1", "--yes"}, method: "POST", path: "/deployments/" + depID + "/rollback", reply: `{}`, body: map[string]any{"version_id": "v-1"}, wantOut: "Rolled back", needsYes: true},
		{args: []string{"deploy", "stop", "my-app", "-y"}, method: "POST", path: "/deployments/" + depID + "/stop", reply: `{}`, wantOut: "Stopped", needsYes: true},
		{args: []string{"deploy", "rm", "my-app", "-y"}, method: "DELETE", path: "/deployments/" + depID, reply: `{}`, wantOut: "Deleted", needsYes: true},
		{args: []string{"deploy", "builds", "my-app"}, method: "GET", path: "/deployments/" + depID + "/builds", reply: `{"data":[]}`, wantOut: "No results"},
		{args: []string{"deploy", "env", "my-app"}, method: "GET", path: "/deployments/" + depID + "/env", reply: `{"env_vars":[{"name":"DATABASE_URL"}]}`, wantOut: "DATABASE_URL"},
		{args: []string{"deploy", "domains", "my-app"}, method: "GET", path: "/deployments/" + depID + "/domains", reply: list(`{"id":"d1d1d1d1-0000-0000-0000-000000000000","hostname":"app.example.com","status":"pending"}`), wantOut: "app.example.com"},
		{args: []string{"deploy", "domain-add", "my-app", "app.example.com"}, method: "POST", path: "/deployments/" + depID + "/domains", reply: `{"data":{"id":"d1"}}`, body: map[string]any{"hostname": "app.example.com"}},
		{args: []string{"deploy", "domain-verify", "my-app", "d1"}, method: "POST", path: "/deployments/" + depID + "/domains/d1/verify", reply: `{}`},
		{args: []string{"deploy", "domain-rm", "my-app", "d1", "-y"}, method: "DELETE", path: "/deployments/" + depID + "/domains/d1", reply: `{}`, wantOut: "Detached", needsYes: true},
		// databases
		{args: []string{"db", "list"}, method: "GET", path: "/databases", reply: list(`{"id":"380330d8-e346-4a0f-950f-000000000000","name":"app-db","engine":"postgresql","state":"running","memory_mb":1024}`), wantOut: "postgresql"},
		{args: []string{"db", "get", "d1"}, method: "GET", path: "/databases/d1", reply: `{"name":"app-db","state":"running","host":"db.example","port":5432}`, wantOut: "db.example"},
		{args: []string{"db", "start", "d1"}, method: "POST", path: "/databases/d1/start", reply: `{}`, wantOut: "Starting d1"},
		{args: []string{"db", "stop", "d1", "-y"}, method: "POST", path: "/databases/d1/stop", reply: `{}`, wantOut: "Stopping d1", needsYes: true},
		{args: []string{"db", "restart", "d1"}, method: "POST", path: "/databases/d1/restart", reply: `{}`},
		{args: []string{"db", "wake", "d1"}, method: "POST", path: "/databases/d1/wake", reply: `{}`},
		{args: []string{"db", "connection", "d1"}, method: "GET", path: "/databases/d1/credentials", reply: `{"url":"postgres://u:p@h/db"}`, wantOut: "postgres://"},
		{args: []string{"db", "logs", "d1"}, method: "GET", path: "/databases/d1/logs", reply: `{"lines":["ready to accept connections"]}`, wantOut: "ready to accept"},
		{args: []string{"db", "backups", "d1"}, method: "GET", path: "/databases/d1/backups", reply: list(`{"id":"b1b1b1b1-0000-0000-0000-000000000000","state":"ready","size_bytes":2048}`), wantOut: "2.0 KB"},
		{args: []string{"db", "backup", "d1"}, method: "POST", path: "/databases/d1/backups", reply: `{}`},
		{args: []string{"db", "rm", "d1", "-y"}, method: "DELETE", path: "/databases/d1", reply: `{}`, wantOut: "Deleted d1", needsYes: true},
		// storage, volumes
		{args: []string{"storage", "list"}, method: "GET", path: "/storage/buckets", reply: list(`{"id":"f94cfb09-5ef0-4c42-a33d-000000000000","name":"assets","object_count":3,"used_bytes":1024,"visibility":"private"}`), wantOut: "assets"},
		{args: []string{"storage", "create", "assets", "--visibility", "public", "--region", "us-mia"}, method: "POST", path: "/storage/buckets", reply: `{"data":{"id":"b1"}}`, body: map[string]any{"name": "assets", "visibility": "public", "region": "us-mia"}},
		{args: []string{"storage", "objects", "b1", "--prefix", "img/", "-n", "10"}, method: "GET", path: "/storage/buckets/b1/objects", reply: list(`{"key":"img/a.png","size":5000}`), query: map[string]string{"prefix": "img/", "limit": "10"}, wantOut: "img/a.png"},
		{args: []string{"storage", "rm", "b1", "-y"}, method: "DELETE", path: "/storage/buckets/b1", reply: `{}`, needsYes: true},
		{args: []string{"volume", "list"}, method: "GET", path: "/volumes", reply: list(`{"id":"87d908f0-520a-4028-b2bc-000000000000","name":"data","status":"in_use","size_bytes":1073741824,"backend":"local"}`), wantOut: "1.0 GB"},
		{args: []string{"volume", "create", "data", "--size-gb", "5"}, method: "POST", path: "/volumes", reply: `{"data":{"id":"v1"}}`, body: map[string]any{"name": "data", "size_gb": float64(5)}},
		{args: []string{"volume", "rm", "v1", "-y"}, method: "DELETE", path: "/volumes/v1", reply: `{}`, needsYes: true},
		// workflows, cron, functions
		{args: []string{"workflow", "list"}, method: "GET", path: "/workflows", reply: list(`{"id":"w1w1w1w1-0000-0000-0000-000000000000","name":"nightly","status":"active"}`), wantOut: "nightly"},
		{args: []string{"workflow", "run", "w1", "--input", `{"branch":"main"}`}, method: "POST", path: "/workflows/w1/run", reply: `{"data":{"run_id":"r1"}}`, body: map[string]any{"input": map[string]any{"branch": "main"}}},
		{args: []string{"workflow", "pause", "w1"}, method: "POST", path: "/workflows/w1/pause", reply: `{}`, wantOut: "Paused w1"},
		{args: []string{"workflow", "resume", "w1"}, method: "POST", path: "/workflows/w1/resume", reply: `{}`},
		{args: []string{"workflow", "runs"}, method: "GET", path: "/workflows/runs", reply: list(`{"id":"r1r1r1r1-0000-0000-0000-000000000000","status":"succeeded"}`), wantOut: "succeeded"},
		{args: []string{"workflow", "run-cancel", "r1"}, method: "POST", path: "/workflows/runs/r1/cancel", reply: `{}`, wantOut: "Canceled r1"},
		{args: []string{"schedule", "list"}, method: "GET", path: "/cron-jobs", reply: list(`{"id":"c1c1c1c1-0000-0000-0000-000000000000","name":"backup","schedule":"0 2 * * *","status":"active"}`), wantOut: "0 2 * * *"},
		{args: []string{"schedule", "run", "c1"}, method: "POST", path: "/cron-jobs/c1/run-now", reply: `{}`},
		{args: []string{"schedule", "pause", "c1"}, method: "POST", path: "/cron-jobs/c1/pause", reply: `{}`},
		{args: []string{"schedule", "executions", "c1"}, method: "GET", path: "/cron-jobs/c1/executions", reply: list(`{"id":"e1e1e1e1-0000-0000-0000-000000000000","status":"ok"}`), wantOut: "e1e1e1e1"},
		{args: []string{"function", "list"}, method: "GET", path: "/functions", reply: list(`{"id":"f1f1f1f1-0000-0000-0000-000000000000","name":"hello","runtime":"node"}`), wantOut: "hello"},
		{args: []string{"function", "invoke", "f1", "-d", `{"name":"ana"}`}, method: "POST", path: "/functions/f1/invoke", reply: `{"result":"hi ana"}`, body: map[string]any{"payload": map[string]any{"name": "ana"}}, wantOut: "hi ana"},
		// templates, projects
		{args: []string{"template", "list"}, method: "GET", path: "/templates", reply: list(`{"id":"nextjs","name":"Next.js","readiness":"fast_ready","default_size":"small"}`), wantOut: "fast_ready"},
		{args: []string{"template", "custom"}, method: "GET", path: "/sandbox-templates", reply: `[{"id":"mine","name":"Mine","status":"ready","public":false}]`, wantOut: "mine"},
		{args: []string{"template", "builds", "mine"}, method: "GET", path: "/sandbox-templates/mine/builds", reply: `{"data":[]}`},
		{args: []string{"template", "build", "mine"}, method: "POST", path: "/sandbox-templates/mine/builds", reply: `{"data":{"id":"b1"}}`},
		{args: []string{"template", "build-logs", "b1"}, method: "GET", path: "/sandbox-template-builds/b1/logs", reply: `{"lines":["step 1"]}`, wantOut: "step 1"},
		{args: []string{"template", "promote", "mine"}, method: "POST", path: "/sandbox-templates/mine/promote", reply: `{}`, wantOut: "Promoted mine"},
		{args: []string{"template", "rollback", "mine", "-y"}, method: "POST", path: "/sandbox-templates/mine/rollback", reply: `{}`, needsYes: true},
		{args: []string{"template", "warm-pool", "mine"}, method: "GET", path: "/sandbox-templates/mine/warm-pool", reply: `{"size":2}`},
		{args: []string{"project", "list"}, method: "GET", path: "/projects", reply: list(`{"id":"6335ed11-9842-4797-9d5e-000000000000","name":"Default","slug":"default","is_default":true}`), wantOut: "Default"},
		{args: []string{"project", "create", "web", "--workspace-id", wsID, "--description", "d"}, method: "POST", path: "/projects", reply: `{"data":{"id":"p1"}}`, body: map[string]any{"name": "web", "workspace_id": wsID, "description": "d"}},
		// billing
		{args: []string{"billing", "balance"}, method: "GET", path: "/credits/balance", reply: `{"balance_credits":4119098,"lifetime_spent":10}`, wantOut: "4119098"},
		{args: []string{"billing", "transactions", "-n", "2", "--page", "2"}, method: "GET", path: "/credits/transactions", reply: `{"transactions":[{"type":"spend","amount_credits":-1,"resource_type":"sandbox","description":"runtime"}]}`, query: map[string]string{"per_page": "2", "page": "2"}, wantOut: "runtime"},
		{args: []string{"billing", "usage"}, method: "GET", path: "/credits/usage", reply: `{"usage":[{"day":"2026-09-15","model":"glm","call_count":3}]}`, wantOut: "glm"},
		{args: []string{"billing", "spend"}, method: "GET", path: "/usage", reply: `{"results":[{"workspace_id":"5d968ccf-7c64-4ca6-b887-a5b62d1d04fe","credit_cents":12676,"sandbox_seconds":7200}]}`, wantOut: "$126.76"},
		{args: []string{"billing", "invoices"}, method: "GET", path: "/billing/invoices", reply: `{"data":[]}`},
		{args: []string{"billing", "upcoming"}, method: "GET", path: "/billing/upcoming", reply: `{"data":{"amount_due_cents":4673,"currency":"usd"}}`, wantOut: "$46.73"},
		{args: []string{"billing", "plan"}, method: "GET", path: "/tenant/plan", reply: `{"plan":{"name":"enterprise","interval":"month"},"usage":{"sandboxes":1}}`, wantOut: "enterprise"},
		// audit, alerts, notifications, members, egress, regions
		{args: []string{"audit", "-n", "5", "--type", "api.get"}, method: "GET", path: "/audit-log", reply: list(`{"ts":"2026-10-09T00:00:00Z","type":"api.get","status":"success","resource":{"type":"credits"}}`), query: map[string]string{"limit": "5", "type": "api.get"}, wantOut: "credits"},
		{args: []string{"alerts", "list"}, method: "GET", path: "/alerts", reply: `{"data":[]}`},
		{args: []string{"alerts", "resolve", "a1"}, method: "POST", path: "/alerts/a1/resolve", reply: `{}`, wantOut: "Resolved a1"},
		{args: []string{"notifications", "list"}, method: "GET", path: "/notifications", reply: `{"data":[]}`},
		{args: []string{"notifications", "unread"}, method: "GET", path: "/notifications/unread-count", reply: `{"count":3}`, wantOut: "3"},
		{args: []string{"notifications", "read", "n1"}, method: "POST", path: "/notifications/n1/read", reply: `{}`},
		{args: []string{"notifications", "read-all"}, method: "POST", path: "/notifications/read-all", reply: `{}`},
		{args: []string{"member", "list"}, method: "GET", path: "/tenant/members", reply: list(`{"id":"2fd706e6-b3cd-4158-86c0-000000000000","email":"a@b.co","role":"owner"}`), wantOut: "a@b.co"},
		{args: []string{"member", "add", "new@b.co", "--role", "viewer"}, method: "POST", path: "/tenant/members", reply: `{"data":{}}`, body: map[string]any{"email": "new@b.co", "role": "viewer"}},
		{args: []string{"member", "role", "m1", "--role", "admin"}, method: "PATCH", path: "/tenant/members/m1/role", reply: `{}`, body: map[string]any{"role": "admin"}},
		{args: []string{"member", "rm", "m1", "-y"}, method: "DELETE", path: "/tenant/members/m1", reply: `{}`, needsYes: true},
		{args: []string{"egress", "policies"}, method: "GET", path: "/egress/policies", reply: list(`{"id":"9ab278a7-e35d-42e8-81b9-000000000000","mode":"audit_only","default_action":"allow"}`), wantOut: "audit_only"},
		{args: []string{"egress", "allowlist", "--policy", "p1"}, method: "GET", path: "/egress/allowlist", reply: `{"data":[{"host":"github.com","port":443}]}`, query: map[string]string{"policy_id": "p1"}, wantOut: "github.com"},
		{args: []string{"egress", "audit"}, method: "GET", path: "/egress/audit", reply: `{"data":[],"total":0}`},
		{args: []string{"api-key", "presets"}, method: "GET", path: "/api-keys/presets", reply: list(`{"name":"cli","description":"The miosa CLI","scopes":["agents:read","connections:write"]}`), wantOut: "cli"},
		// AI gateway
		{args: []string{"gateway", "health"}, method: "GET", path: "/ai-gateway/health", reply: `{"data":{"status":"ok"}}`, wantOut: "ok"},
		{args: []string{"gateway", "policy", "list"}, method: "GET", path: "/ai-gateway/policies", reply: list(`{"id":"9ab278a7-e35d-42e8-81b9-000000000000","name":"fast","alias_name":"fast","primary_model":"claude-haiku","strategy":"priority","priority":100,"enabled":true}`), wantOut: "claude-haiku"},
		{args: []string{"gateway", "policy", "new", "fast", "--primary-model", "claude-haiku", "--fallback-model", "gpt-mini,gemini-flash", "--alias", "fast"}, method: "POST", path: "/ai-gateway/policies", reply: `{"data":{"id":"p1","name":"fast"}}`, body: map[string]any{"name": "fast", "primary_model": "claude-haiku", "fallback_models": []string{"gpt-mini", "gemini-flash"}, "alias_name": "fast"}},
		{args: []string{"gateway", "policy", "update", "p1", "--enabled=false", "--priority", "5"}, method: "PATCH", path: "/ai-gateway/policies/p1", reply: `{"data":{"id":"p1"}}`, body: map[string]any{"enabled": false, "priority": 5}},
		{args: []string{"gateway", "policy", "rm", "p1", "--yes"}, method: "DELETE", path: "/ai-gateway/policies/p1", reply: `{}`, needsYes: true},
		{args: []string{"gateway", "budget", "show"}, method: "GET", path: "/ai-gateway/budget", reply: `{"data":{"monthly_cents":50000}}`, wantOut: "50000"},
		{args: []string{"gateway", "limit", "rm", "key", "k1", "--yes"}, method: "DELETE", path: "/ai-gateway/limits/key/k1", reply: `{}`, needsYes: true},
		{args: []string{"gateway", "requests", "--limit", "5", "--model", "claude-haiku"}, method: "GET", path: "/ai-gateway/requests", reply: list(`{"id":"0f0f0f0f-0000-0000-0000-000000000000","model":"claude-haiku","status":"ok","latency_ms":420}`), query: map[string]string{"limit": "5", "model": "claude-haiku"}, wantOut: "claude-haiku"},
		// service accounts
		{args: []string{"service-account", "list"}, method: "GET", path: "/service-accounts", reply: list(`{"id":"sa1","name":"deploy-pipeline","description":"release"}`), wantOut: "deploy-pipeline"},
		{args: []string{"service-account", "new", "deploy-pipeline", "--description", "release"}, method: "POST", path: "/service-accounts", reply: `{"data":{"id":"sa1","name":"deploy-pipeline"}}`, body: map[string]any{"name": "deploy-pipeline", "description": "release"}},
		{args: []string{"service-account", "update", "sa1", "--name", "bot"}, method: "PATCH", path: "/service-accounts/sa1", reply: `{"data":{"id":"sa1"}}`, body: map[string]any{"name": "bot"}},
		{args: []string{"service-account", "rm", "sa1", "-y"}, method: "DELETE", path: "/service-accounts/sa1", reply: `{}`, wantOut: "Deleted", needsYes: true},
		{args: []string{"service-account", "keys", "sa1"}, method: "GET", path: "/service-accounts/sa1/keys", reply: list(`{"id":"k1","name":"deploy-pipeline key","status":"active"}`), wantOut: "deploy-pipeline key"},
		// batches
		{args: []string{"batch", "status", "b1"}, method: "GET", path: "/sandboxes/batches/b1", reply: `{"data":{"id":"b1","status":"running","requested":3,"live_count":1}}`, wantOut: "running"},
		{args: []string{"batch", "items", "b1", "--limit", "5"}, method: "GET", path: "/sandboxes/batches/b1/items", reply: list(`{"id":"s1","name":"ci-1","state":"running","ready":true,"batch_item_index":0}`), query: map[string]string{"limit": "5"}, wantOut: "ci-1"},
		{args: []string{"batch", "cancel", "b1"}, method: "POST", path: "/sandboxes/batches/b1/cancel", reply: `{"data":{"id":"b1","status":"canceling"}}`, wantOut: "canceling"},
		// chat context and compaction
		{args: []string{"chat", "context", "c1"}, method: "GET", path: "/agents/chats/c1/context", reply: `{"data":{"chat_id":"c1","harness":"claude-code","runs":3,"context":{"used_tokens":90000,"window_tokens":200000,"remaining_tokens":110000,"used_ratio":0.45},"compactions":[{"run_id":"r1"}],"compactable":false,"compactable_reason":"run_in_progress"}}`, wantOut: "run_in_progress"},
		{args: []string{"chat", "compact", "c1", "--focus", "the db schema"}, method: "POST", path: "/agents/chats/c1/compact", reply: `{"data":{"id":"run-9","status":"queued","chat_id":"c1","runner":"claude-code"},"instruction":"/compact the db schema"}`, body: map[string]any{"focus": "the db schema"}, wantOut: "run-9"},
		// cloud (bring your own cloud, read side)
		{args: []string{"cloud", "capabilities"}, method: "GET", path: "/cloud/capabilities", reply: `{"data":{"providers":{"aws":{"provisioning":"private_preview"}}}}`, wantOut: "private_preview"},
		{args: []string{"cloud", "accounts"}, method: "GET", path: "/cloud/accounts", reply: list(`{"id":"f629c724-67ea-4f75-a27e-1d907042827a","display_name":"main","provider":"aws","default_region":"us-east-1","status":"ready","mode":"customer_byoc"}`), wantOut: "customer_byoc"},
		{args: []string{"cloud", "regions"}, method: "GET", path: "/cloud/regions", reply: list(`{"id":"f06cbccc-efff-4c68-b39a-130a82d5611e","display_name":"east","provider":"aws","provider_region":"us-east-1","status":"ready"}`), wantOut: "us-east-1"},
		{args: []string{"cloud", "pools"}, method: "GET", path: "/cloud/pools", reply: list(`{"id":"11be9dae-51e1-4bfc-b34f-1950c6ae0360","pool_kind":"cloudburst","instance_type":"m7i.8xlarge","target_nodes":1,"max_nodes":4,"status":"accepting"}`), wantOut: "m7i.8xlarge"},
		{args: []string{"cloud", "nodes"}, method: "GET", path: "/cloud/nodes", reply: list(`{"id":"4bf93b10-0000-0000-0000-000000000000","provider":"aws","status":"active"}`), wantOut: "active"},
		{args: []string{"cloud", "preflights"}, method: "GET", path: "/cloud/preflights", reply: list(`{"id":"159f677c-6c1d-43d0-856c-8ebf002dbac0","status":"pass"}`), wantOut: "pass"},
		{args: []string{"regions"}, method: "GET", path: "/regions", reply: `{"regions":[{"id":"us-east","label":"US-East","available":true}]}`, wantOut: "US-East"},
		// workspaces (extras)
		{args: []string{"workspace", "get", "acme"}, method: "GET", path: "/workspaces/" + wsID, reply: `{"data":{"id":"` + wsID + `","name":"Acme","slug":"acme","is_default":false}}`, wantOut: "Acme"},
		{args: []string{"workspace", "stats", "acme"}, method: "GET", path: "/workspaces/" + wsID + "/stats", reply: `{"sandboxes":3}`},
		{args: []string{"workspace", "usage", "acme"}, method: "GET", path: "/workspaces/" + wsID + "/usage", reply: `{"credit_cents":1}`},
		{args: []string{"workspace", "activity", "acme"}, method: "GET", path: "/workspaces/" + wsID + "/activity", reply: `{"data":[]}`},
		{args: []string{"workspace", "inventory", "acme"}, method: "GET", path: "/workspaces/" + wsID + "/inventory", reply: `{"sandboxes":[]}`},
		{args: []string{"workspace", "members", "acme"}, method: "GET", path: "/workspaces/" + wsID + "/members", reply: list(`{"email":"a@b.co","role":"admin"}`), wantOut: "a@b.co"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			routes := map[string]any{
				"GET /deployments": `{"data":[{"id":"` + depID + `","name":"My App","slug":"my-app"}]}`,
				"GET /workspaces":  `{"data":[{"id":"` + wsID + `","name":"Acme","slug":"acme"}]}`,
			}
			routes[tc.method+" "+tc.path] = tc.reply // the case's own reply wins
			f := fakeRoutes(t, routes)
			if tc.needsYes {
				noYes := make([]string, 0, len(tc.args))
				for _, a := range tc.args {
					if a != "-y" && a != "--yes" {
						noYes = append(noYes, a)
					}
				}
				if _, err := run(t, noYes...); err == nil || !strings.Contains(err.Error(), "--yes") {
					t.Fatalf("must refuse without --yes: %v", err)
				}
				if len(f.find(tc.method, tc.path)) != 0 {
					t.Fatal("nothing may be sent before confirmation")
				}
			}
			out, err := run(t, tc.args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			calls := f.find(tc.method, tc.path)
			if len(calls) != 1 {
				t.Fatalf("want one %s %s, got calls %+v", tc.method, tc.path, f.calls)
			}
			q, _ := url.ParseQuery(calls[0].Query)
			for k, v := range tc.query {
				if q.Get(k) != v {
					t.Errorf("query[%s] = %q, want %q (query %q)", k, q.Get(k), v, calls[0].Query)
				}
			}
			for k, v := range tc.body {
				got, _ := json.Marshal(calls[0].Body[k])
				want, _ := json.Marshal(v)
				if string(got) != string(want) {
					t.Errorf("body[%s] = %s, want %s", k, got, want)
				}
			}
			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Errorf("output lacks %q:\n%s", tc.wantOut, out)
			}
			// --json always yields valid JSON for a successful call.
			jargs := append(append([]string(nil), tc.args...), "--json")
			jout, jerr := run(t, jargs...)
			var v any
			if jerr != nil || json.Unmarshal([]byte(jout), &v) != nil {
				t.Errorf("--json: %v\n%s", jerr, jout)
			}
		})
	}
}

func TestAPIKeyCreateShowsTheSecretOnceAndMergesScopes(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /api-keys": resp{201, `{"data":{"id":"k1","name":"ci","key":"msk_u_SECRETVALUE","scopes":["a","b"],"expires_at":"2027-01-01T00:00:00Z"}}`}})
	out, err := run(t, "api-key", "create", "ci", "--preset", "ci", "--scope", "agents:read", "--scope", "sandboxes:exec", "--expires-in", "90",
		"--workspace-id", wsID, "--allowed-ip", "10.0.0.0/8", "--rate-limit", "120")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "msk_u_SECRETVALUE") != 1 || !strings.Contains(out, "shown once") {
		t.Fatalf("the secret must appear exactly once with a warning:\n%s", out)
	}
	b := f.find("POST", "/api-keys")[0].Body
	scopes := toStringSlice(b["scopes"])
	if b["name"] != "ci" || b["key_type"] != "user" || b["expires_in_days"] != float64(90) || b["workspace_id"] != wsID || b["rate_limit_rpm"] != float64(120) {
		t.Fatalf("body = %v", b)
	}
	if !contains(scopes, "sandboxes:exec") || !contains(scopes, "agents:read") || !contains(scopes, "deployments:write") {
		t.Fatalf("scopes = %v", scopes)
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		if seen[s] {
			t.Fatalf("scope %q duplicated in %v", s, scopes)
		}
		seen[s] = true
	}
	qout, err := run(t, "api-key", "create", "x", "--quiet-key")
	if err != nil || strings.TrimSpace(qout) != "msk_u_SECRETVALUE" {
		t.Fatalf("--quiet-key must print only the secret: %v %q", err, qout)
	}
	if _, err := run(t, "api-key", "create", "x", "--preset", "everything"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad preset: %v", err)
	}
	ro, _ := run(t, "api-key", "create", "ro", "--preset", "read-only")
	_ = ro
	last := f.find("POST", "/api-keys")
	for _, s := range toStringSlice(last[len(last)-1].Body["scopes"]) {
		if !strings.HasSuffix(s, ":read") {
			t.Fatalf("read-only preset granted %q", s)
		}
	}
}

func toStringSlice(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, e := range l {
		out = append(out, e.(string))
	}
	return out
}

func contains(l []string, s string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}

func TestAPIKeyListHidesRevokedAndRotateRmWork(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /api-keys": `{"data":[{"id":"aaaaaaaa-0000-0000-0000-000000000000","name":"live","status":"active","key_prefix":"msk_u_abc","last_four":"1234","key_type":"user"},
		 {"id":"bbbbbbbb-0000-0000-0000-000000000000","name":"old","status":"revoked","key_prefix":"msk_u_def","last_four":"5678","key_type":"user"}]}`,
		"POST /api-keys/k1/rotate": `{"data":{"id":"k1","key":"msk_u_NEWSECRET"}}`,
		"DELETE /api-keys/k1":      `{}`,
		"PATCH /api-keys/k1":       `{"data":{"id":"k1","name":"renamed"}}`,
		"POST /api-keys/scoped":    resp{201, `{"data":{"id":"s1","token":"msk_s_TOKEN"}}`},
	})
	out, err := run(t, "api-key", "list")
	if err != nil || !strings.Contains(out, "live") || strings.Contains(out, "old") {
		t.Fatalf("%v\n%s", err, out)
	}
	all, _ := run(t, "api-key", "list", "--all")
	if !strings.Contains(all, "old") {
		t.Fatalf("--all must include revoked:\n%s", all)
	}
	if out, err := run(t, "api-key", "rotate", "k1"); err != nil || !strings.Contains(out, "msk_u_NEWSECRET") {
		t.Fatalf("rotate: %v\n%s", err, out)
	}
	if _, err := run(t, "api-key", "rm", "k1"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("rm without --yes: %v", err)
	}
	if _, err := run(t, "api-key", "rm", "k1", "-y"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "api-key", "update", "k1", "--name", "renamed", "--rate-limit", "30"); err != nil {
		t.Fatal(err)
	}
	if u := f.find("PATCH", "/api-keys/k1")[0].Body; u["name"] != "renamed" || u["rate_limit_rpm"] != float64(30) {
		t.Fatalf("update = %v", u)
	}
	sout, err := run(t, "api-key", "scoped", "end-user", "--workspace-id", wsID, "--external-user-id", "u-1", "--scope", "sandboxes:exec", "--expires-at", "2027-01-01T00:00:00Z")
	if err != nil || !strings.Contains(sout, "msk_s_TOKEN") {
		t.Fatalf("scoped: %v\n%s", err, sout)
	}
	if b := f.find("POST", "/api-keys/scoped")[0].Body; b["external_user_id"] != "u-1" || b["label"] != "end-user" {
		t.Fatalf("scoped body = %v", b)
	}
}

func TestWebhookCreateSendsEventsHeadersAndShowsTheSecretOnce(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /webhooks":                         resp{201, `{"data":{"id":"w1","url":"https://example.com/h","events":["sandbox.ready","sandbox.error"],"secret":"whsec_ONCE"}}`},
		"GET /webhooks/w1/deliveries":            `{"data":[{"id":"d1d1d1d1-0000-0000-0000-000000000000","event":"sandbox.ready","status":"delivered","response_status":200,"attempts":1}]}`,
		"POST /webhooks/w1/deliveries/d1/replay": `{}`,
		"PATCH /webhooks/w1":                     `{"data":{"id":"w1"}}`,
		"DELETE /webhooks/w1":                    `{}`,
		"POST /webhooks/w1/test":                 `{"data":{"status":"delivered"}}`,
		"GET /webhooks/w1":                       `{"data":{"id":"w1","url":"https://example.com/h","secret_preview":"whsec_...abcd"}}`,
	})
	out, err := run(t, "webhook", "create", "https://example.com/h", "--event", "sandbox.ready", "--event", "sandbox.error,deployment.ready",
		"--name", "prod", "--retries", "5", "--header", "X-Team=infra", "--workspace-id", wsID)
	if err != nil || strings.Count(out, "whsec_ONCE") != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("POST", "/webhooks")[0].Body
	ev := toStringSlice(b["events"])
	if b["url"] != "https://example.com/h" || len(ev) != 3 || b["retry_count"] != float64(5) || b["headers"].(map[string]any)["X-Team"] != "infra" || b["name"] != "prod" {
		t.Fatalf("body = %v", b)
	}
	if out, err := run(t, "webhook", "get", "w1"); err != nil || strings.Contains(out, "whsec_ONCE") || !strings.Contains(out, "whsec_...abcd") {
		t.Fatalf("get must show only the preview: %v\n%s", err, out)
	}
	for _, args := range [][]string{{"webhook", "test", "w1"}, {"webhook", "deliveries", "w1"}, {"webhook", "replay", "w1", "d1"}, {"webhook", "update", "w1", "--active", "--event", "sandbox.ready"}, {"webhook", "rm", "w1", "-y"}} {
		if out, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if u := f.find("PATCH", "/webhooks/w1")[0].Body; u["active"] != true {
		t.Fatalf("update = %v", u)
	}
	if _, err := run(t, "webhook", "create", "https://x", "--header", "bad"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad header: %v", err)
	}
}

func TestWorkspaceUseStoresTheSlug(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /workspaces":         `{"data":[{"id":"` + wsID + `","name":"Acme Inc","slug":"acme"}]}`,
		"GET /workspaces/" + wsID: `{"data":{"id":"` + wsID + `","name":"Acme Inc","slug":"acme"}}`,
	})
	out, err := run(t, "workspace", "use", "Acme Inc")
	if err != nil || !strings.Contains(out, `"acme"`) {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(readConfig(t), `default_workspace = "acme"`) {
		t.Fatalf("config = %s", readConfig(t))
	}
}

func TestMissingScopeIsNamedInPlatformErrors(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /agents/harnesses": resp{403, `{"error":{"code":"FORBIDDEN","message":"insufficient scope - this action requires the 'agents:read' scope. Mint a key with it."}}`}})
	_, err := run(t, "agent", "harnesses")
	if err == nil || !strings.Contains(err.Error(), "agents:read") || commands.ExitCode(err) != commands.ExitAuth {
		t.Fatalf("err = %v", err)
	}
}

func TestAPIKeyCreateTakesThePresetFromTheServer(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /api-keys/presets": `{"data":[{"name":"cli","scopes":["agents:read","connections:write","zz:server-only"]}]}`,
		"POST /api-keys":        resp{201, `{"data":{"id":"k1","name":"ci","key":"msk_u_SECRET","scopes":[]}}`},
	})
	if _, err := run(t, "api-key", "create", "ci", "--preset", "cli"); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(f.find("POST", "/api-keys")[0].Body["scopes"])
	if string(got) != `["agents:read","connections:write","zz:server-only"]` {
		t.Fatalf("the server preset must win over the built-in copy: %s", got)
	}
	// An older server has no presets route: the built-in copy is used.
	f = fakeRoutes(t, map[string]any{"POST /api-keys": resp{201, `{"data":{"id":"k1","name":"ci","key":"msk_u_SECRET","scopes":[]}}`}})
	if _, err := run(t, "api-key", "create", "ci", "--preset", "cli"); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(f.find("POST", "/api-keys")[0].Body["scopes"]); !strings.Contains(string(got), "connections:read") {
		t.Fatalf("fallback preset: %s", got)
	}
}

func TestFileBodyIsMergedUnderFlagsAndRejectsNonObjects(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"PUT /ai-gateway/budget": `{"data":{}}`})
	p := filepath.Join(t.TempDir(), "budget.json")
	if err := os.WriteFile(p, []byte(`{"monthly_cents":1000,"mode":"block"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "gateway", "budget", "set", "--file", p); err != nil {
		t.Fatal(err)
	}
	if b := f.find("PUT", "/ai-gateway/budget")[0].Body; b["monthly_cents"] != float64(1000) || b["mode"] != "block" {
		t.Fatalf("body = %v", b)
	}
	if _, err := runIn(t, `{"monthly_cents":7}`, "gateway", "budget", "set", "--file", "-"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("PUT", "/ai-gateway/budget")[1].Body; b["monthly_cents"] != float64(7) {
		t.Fatalf("stdin body = %v", b)
	}
	if _, err := runIn(t, `[1,2]`, "gateway", "budget", "set", "--file", "-"); err == nil {
		t.Fatal("a JSON array is not a body")
	}
}
