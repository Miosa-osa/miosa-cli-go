package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vercelJSON(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vercel.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportVercelPrintsThePlan(t *testing.T) {
	path := vercelJSON(t, `{"regions":["iad1"],"env":{"API_URL":"https://x"}}`)

	out, err := run(t, "import", "vercel", "--from-file", path, "--name", "shop", "--repo", "https://github.com/acme/shop")
	if err != nil {
		t.Fatalf("import vercel: %v\n%s", err, out)
	}

	for _, want := range []string{"shop", "service web", "API_URL", "Nothing was created"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

func TestImportVercelWritesThePlan(t *testing.T) {
	dir := t.TempDir()
	path := vercelJSON(t, `{"env":{"API_URL":"https://x"}}`)
	dest := filepath.Join(dir, "plan.json")

	if out, err := run(t, "import", "vercel", "--from-file", path, "--name", "shop", "--out", dest); err != nil {
		t.Fatalf("import vercel: %v\n%s", err, out)
	}

	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"kind": "vercel"`) {
		t.Fatalf("plan = %s", body)
	}
}

func TestImportVercelNeedsAPlanToApply(t *testing.T) {
	path := vercelJSON(t, `{}`)

	_, err := run(t, "import", "vercel", "--from-file", path, "--apply")
	if err == nil || !strings.Contains(err.Error(), "--plan") {
		t.Fatalf("err = %v", err)
	}
}
