package commands_test

import (
	"regexp"
	"strings"
	"testing"
)

func TestWebhookRotateSecretSendsANewSecretAndPrintsItOnce(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"PATCH /webhooks/w1": `{"data":{"id":"w1","url":"https://example.com/h","secret_preview":"whsec_••••••"}}`,
	})
	out, err := run(t, "webhook", "rotate-secret", "w1", "-y")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	sent, _ := f.find("PATCH", "/webhooks/w1")[0].Body["secret"].(string)
	if !regexp.MustCompile(`^whsec_[0-9a-f]{64}$`).MatchString(sent) {
		t.Fatalf("secret sent = %q", sent)
	}
	if strings.Count(out, sent) != 1 {
		t.Fatalf("the new secret must be printed exactly once:\n%s", out)
	}

	out, err = run(t, "webhook", "rotate-secret", "w1", "-y", "--json")
	second, _ := f.find("PATCH", "/webhooks/w1")[1].Body["secret"].(string)
	if err != nil || second == sent || !strings.Contains(out, `"secret": "`+second+`"`) {
		t.Fatalf("each rotation is a new secret, in the JSON too: %v\n%s", err, out)
	}
}

func TestAPIKeyCreateTTL(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /api-keys": `{"data":{"id":"k1","name":"ci","key":"msk_u_x"}}`})
	for _, c := range []struct {
		ttl  string
		days float64
	}{{"30d", 30}, {"36h", 2}, {"720h", 30}} {
		if out, err := run(t, "api-key", "create", "ci", "--ttl", c.ttl); err != nil {
			t.Fatalf("%s: %v\n%s", c.ttl, err, out)
		}
		calls := f.find("POST", "/api-keys")
		if got := calls[len(calls)-1].Body["expires_in_days"]; got != c.days {
			t.Fatalf("--ttl %s sent expires_in_days=%v", c.ttl, got)
		}
	}
}
