package railway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDumpRedactsLiteralVariableValues(t *testing.T) {
	data := json.RawMessage(`{"variables":{"STRIPE_KEY":"sk_live_abc","DATABASE_URL":"${{Postgres.DATABASE_URL}}"}}`)
	out := string(redactVariables(data))
	if strings.Contains(out, "sk_live_abc") {
		t.Fatal("a recorded fixture must never carry a literal secret")
	}
	if !strings.Contains(out, "${{Postgres.DATABASE_URL}}") {
		t.Fatal("a reference is not a secret and must be kept")
	}
	if !strings.Contains(out, "redacted") {
		t.Fatalf("out = %s", out)
	}
}
