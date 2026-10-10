package miosa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recorded struct {
	Method, Path, Query string
	Body                map[string]interface{}
}

func envServer(t *testing.T, reply func(r recorded) (int, string)) (*Client, *[]recorded) {
	t.Helper()
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.Body)
		}
		calls = append(calls, rec)
		status, body := reply(rec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient("msk_test", WithBaseURL(srv.URL), WithMaxRetries(0)), &calls
}

func TestEnvironmentsRoutes(t *testing.T) {
	c, calls := envServer(t, func(r recorded) (int, string) {
		return 200, `{"environment":{"id":"e1","name":"staging","latest_version":2},"versions":[{"version":2}],"value":"v"}`
	})
	ctx := context.Background()
	tr := true

	type step struct {
		name       string
		do         func() error
		method     string
		path       string
		query      string
		wantBody   map[string]interface{}
		noBodyWant bool
	}
	steps := []step{
		{"get", func() error { _, e := c.Environments.Get(ctx, "my env"); return e }, "GET", "/environments/my%20env", "", nil, true},
		{"create", func() error {
			_, e := c.Environments.Create(ctx, CreateEnvironmentInput{Name: "staging", SafeForThirdParties: &tr})
			return e
		}, "POST", "/environments", "", map[string]interface{}{"name": "staging", "safe_for_third_parties": true}, false},
		{"update", func() error {
			_, e := c.Environments.Update(ctx, "staging", UpdateEnvironmentInput{PassGithub: &tr})
			return e
		}, "PATCH", "/environments/staging", "", map[string]interface{}{"pass_github": true}, false},
		{"rename", func() error { _, e := c.Environments.Rename(ctx, "a", "b"); return e }, "POST", "/environments/a/rename", "", map[string]interface{}{"name": "b"}, false},
		{"default", func() error { _, e := c.Environments.SetDefault(ctx, "a"); return e }, "POST", "/environments/a/default", "", nil, true},
		{"delete", func() error { return c.Environments.Delete(ctx, "a") }, "DELETE", "/environments/a", "", nil, true},
		{"set var", func() error { return c.Environments.SetVariable(ctx, "a", "K", "v") }, "PUT", "/environments/a/variables/K", "", map[string]interface{}{"value": "v"}, false},
		{"del var", func() error { return c.Environments.DeleteVariable(ctx, "a", "K") }, "DELETE", "/environments/a/variables/K", "", nil, true},
		{"reveal var", func() error { _, e := c.Environments.RevealVariable(ctx, "a", "K"); return e }, "GET", "/environments/a/variables/K/reveal", "", nil, true},
		{"set file", func() error { return c.Environments.SetSecretFile(ctx, "a", "x/.env", "S=1") }, "PUT", "/environments/a/files", "", map[string]interface{}{"path": "x/.env", "contents": "S=1"}, false},
		{"del file", func() error { return c.Environments.DeleteSecretFile(ctx, "a", "x/.env") }, "DELETE", "/environments/a/files", "path=x%2F.env", nil, true},
		{"add repo", func() error {
			return c.Environments.AddRepository(ctx, "a", AddRepositoryInput{Repo: "o/r", Source: "github", BaseBranch: "dev", SetupBlocking: &tr})
		}, "POST", "/environments/a/repositories", "", map[string]interface{}{"repo": "o/r", "source": "github", "base_branch": "dev", "setup_blocking": true}, false},
		{"rm repo", func() error { return c.Environments.RemoveRepository(ctx, "a", "o/r") }, "DELETE", "/environments/a/repositories", "repo=o%2Fr", nil, true},
		{"versions", func() error { _, e := c.Environments.Versions(ctx, "a"); return e }, "GET", "/environments/a/versions", "", nil, true},
		{"upgrade all", func() error { _, e := c.Environments.Upgrade(ctx, "a", nil); return e }, "POST", "/environments/a/upgrade", "", nil, true},
		{"upgrade ids", func() error { _, e := c.Environments.Upgrade(ctx, "a", []string{"m1"}); return e }, "POST", "/environments/a/upgrade", "", map[string]interface{}{"machine_ids": []interface{}{"m1"}}, false},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			*calls = nil
			if err := s.do(); err != nil {
				t.Fatalf("error: %v", err)
			}
			if len(*calls) != 1 {
				t.Fatalf("want 1 call, got %d", len(*calls))
			}
			got := (*calls)[0]
			if got.Method != s.method || got.Path != s.path || got.Query != s.query {
				t.Fatalf("got %s %s?%s want %s %s?%s", got.Method, got.Path, got.Query, s.method, s.path, s.query)
			}
			if s.noBodyWant && got.Body != nil {
				t.Fatalf("want no body, got %v", got.Body)
			}
			if !s.noBodyWant {
				gb, _ := json.Marshal(got.Body)
				wb, _ := json.Marshal(s.wantBody)
				if string(gb) != string(wb) {
					t.Fatalf("body %s want %s", gb, wb)
				}
			}
		})
	}
}

func TestEnvironmentsDefault(t *testing.T) {
	c, _ := envServer(t, func(recorded) (int, string) {
		return 200, `{"environments":[{"id":"e1","name":"base","is_default":true},{"id":"e2","name":"x"}],"default_environment_id":"e1"}`
	})
	e, err := c.Environments.Default(context.Background())
	if err != nil || e.Name != "base" {
		t.Fatalf("got %+v %v", e, err)
	}
}

func TestUpdateEnvironmentInputOmitsUnset(t *testing.T) {
	f := false
	b, _ := json.Marshal(UpdateEnvironmentInput{PassSecrets: &f})
	if string(b) != `{"pass_secrets":false}` {
		t.Fatalf("false must be sent, unset must be omitted: %s", b)
	}
}

func TestEnvironmentErrorsAreTyped(t *testing.T) {
	c, _ := envServer(t, func(recorded) (int, string) {
		return 409, `{"error":{"code":"ENVIRONMENT_IS_DEFAULT","message":"make another default first"}}`
	})
	err := c.Environments.Delete(context.Background(), "base")
	var base *MiosaError
	if !asMiosa(err, &base) || base.Code != "ENVIRONMENT_IS_DEFAULT" || base.Message != "make another default first" {
		t.Fatalf("got %#v", err)
	}
}

func asMiosa(err error, target **MiosaError) bool {
	if v, ok := err.(*MiosaError); ok {
		*target = v
		return true
	}
	return false
}

func TestMachineEnvironmentDecodes(t *testing.T) {
	var s SandboxData
	raw := `{"id":"s1","environment":"prod","environment_id":"e1","environment_version":2,"environment_latest_version":3,"environment_upgrade_available":true,"environment_status":"applied","environment_error":null,"setup_status":"failed","setup_error":"exit code 1"}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if s.Environment != "prod" || s.EnvironmentVersion != 2 || !s.EnvironmentUpgradeAvailable || s.SetupStatus != "failed" || s.SetupError != "exit code 1" {
		t.Fatalf("got %+v", s.MachineEnvironment)
	}
}

func TestEnvironmentsWorkspaceScope(t *testing.T) {
	c, calls := envServer(t, func(r recorded) (int, string) {
		return 200, `{"environment":{"id":"e1","name":"x"},"environments":[]}`
	})
	ctx := context.Background()
	scoped := c.Environments.ForWorkspace("ws-1")

	_, _ = scoped.List(ctx)
	_, _ = scoped.Get(ctx, "x")
	_ = scoped.DeleteSecretFile(ctx, "x", "a/.env")
	_, _ = scoped.Create(ctx, CreateEnvironmentInput{Name: "n"})
	_, _ = c.Environments.List(ctx)

	wantQuery := []string{"workspace_id=ws-1", "workspace_id=ws-1", "path=a%2F.env&workspace_id=ws-1", "workspace_id=ws-1", ""}
	for i, w := range wantQuery {
		if (*calls)[i].Query != w {
			t.Errorf("call %d (%s): query %q want %q", i, (*calls)[i].Path, (*calls)[i].Query, w)
		}
	}
	if (*calls)[3].Body["workspace_id"] != "ws-1" {
		t.Errorf("create must also carry workspace_id in the body: %v", (*calls)[3].Body)
	}
	if c.Environments.WorkspaceID() != "" || scoped.WorkspaceID() != "ws-1" {
		t.Error("ForWorkspace must not mutate the original service")
	}
}
