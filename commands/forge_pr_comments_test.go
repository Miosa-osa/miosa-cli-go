package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prCommentsAPI serves a pull request at head "head2" with two comment pages
// (a person, the preview system comment, a service account) and four reviews:
// changes requested on the head (open), an approval on the head, a comment on
// an older head, and a dismissed change request.
func prCommentsAPI(t *testing.T) *httptest.Server {
	t.Helper()
	comment := func(id, kind, body string) map[string]any {
		return map[string]any{"id": id, "kind": kind, "body": body, "created_at": "2026-10-30T12:00:00Z"}
	}
	review := func(id, decision, head, state, body string) map[string]any {
		return map[string]any{"id": id, "decision": decision, "head_sha": head, "state": state, "body": body,
			"reviewer": map[string]any{"name": "Owner"}}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/forge/repositories":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{forgeRepoJSON()}})
		case "/forge/repositories/repo-1/pull-requests/12/collaboration":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"head_sha": "head2", "reviews": []any{
				review("rv-open", "changes_requested", "head2", "active", "Handle the nil head."),
				review("rv-ok", "approved", "head2", "active", "LGTM"),
				review("rv-old", "commented", "head1", "active", "Old nit."),
				review("rv-gone", "changes_requested", "head2", "dismissed", "Dismissed."),
			}}})
		case "/forge/repositories/repo-1/pull-requests/12/comments":
			if r.URL.Query().Get("limit") != "200" {
				t.Errorf("comments limit = %q", r.URL.Query().Get("limit"))
			}
			if r.URL.Query().Get("after") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"next_cursor": "c-preview", "data": []any{
					comment("c-person", "user", "Why a second cache?"),
					comment("c-preview", "system", "Preview deployed at https://pr-12.example"),
				}})
				return
			}
			if r.URL.Query().Get("after") != "c-preview" {
				t.Errorf("after = %q", r.URL.Query().Get("after"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"next_cursor": nil, "data": []any{
				comment("c-bot", "user", "Review agent: one finding."),
			}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func TestForgePRCommentsUnresolvedHidesSystemAndResolved(t *testing.T) {
	srv := prCommentsAPI(t)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := run(t, "forge", "pr", "comments", "platform", "12", "--unresolved", "--json")
	if err != nil {
		t.Fatalf("comments --unresolved --json: %v\n%s", err, out)
	}
	var envelope struct {
		Data struct {
			HeadSHA  string `json:"head_sha"`
			Comments []struct {
				ID string `json:"id"`
			} `json:"comments"`
			Reviews []struct {
				ID string `json:"id"`
			} `json:"reviews"`
			Hidden struct {
				System   int `json:"system_comments"`
				Resolved int `json:"resolved_reviews"`
			} `json:"hidden"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	d := envelope.Data
	if d.HeadSHA != "head2" || len(d.Comments) != 2 || d.Comments[0].ID != "c-person" || d.Comments[1].ID != "c-bot" {
		t.Fatalf("comments = %+v\n%s", d, out)
	}
	if len(d.Reviews) != 1 || d.Reviews[0].ID != "rv-open" || d.Hidden.System != 1 || d.Hidden.Resolved != 3 {
		t.Fatalf("reviews = %+v\n%s", d, out)
	}

	text, err := run(t, "forge", "pr", "comments", "platform", "12", "--unresolved")
	if err != nil {
		t.Fatalf("comments --unresolved: %v\n%s", err, text)
	}
	for _, want := range []string{
		"1 review(s) and 2 comment(s) unresolved", "REVIEW changes requested by Owner", "| Handle the nil head.",
		"| Why a second cache?", "Hidden: 1 system comment(s), 3 resolved review(s)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"Preview deployed", "LGTM", "Old nit.", "Dismissed."} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("text output shows resolved or system %q:\n%s", unwanted, text)
		}
	}
}
