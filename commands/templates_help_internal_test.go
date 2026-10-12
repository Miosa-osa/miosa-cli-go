package commands

import (
	"strings"
	"testing"
)

// The create and catalog help must name templates the catalog actually lists,
// and must not name retired image ids as choices.
func TestTemplateHelpNamesListedTemplates(t *testing.T) {
	listed := []string{"miosa-sandbox", "nextjs", "static-html", "fastapi-auth"}
	retired := []string{"hono", "miosa-sandbox-prod-1", "miosa-sandbox-docker", "instant-code", "agent-browser", "debian-12"}

	create := newCreateCmd().Flags().Lookup("template")
	if create == nil {
		t.Fatal("create: --template flag is missing")
	}
	for _, want := range listed {
		if !strings.Contains(create.Usage, want) {
			t.Errorf("create --template usage does not name %q: %s", want, create.Usage)
		}
	}
	for _, gone := range retired {
		if strings.Contains(create.Usage, gone) {
			t.Errorf("create --template usage still names %q: %s", gone, create.Usage)
		}
	}

	catalog := newCatalogCmd().Flags().Lookup("template")
	if catalog == nil {
		t.Fatal("catalog: --template flag is missing")
	}
	for _, want := range []string{"miosa-sandbox", "nextjs", "static-html"} {
		if !strings.Contains(catalog.Usage, want) {
			t.Errorf("catalog --template usage does not name %q: %s", want, catalog.Usage)
		}
	}
}
