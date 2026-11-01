package commands_test

import (
	"os"
	"strings"
	"testing"
)

func TestPlansListsWhatThePricingAPIPublishes(t *testing.T) {
	pricing, err := os.ReadFile("testdata/pricing.live-2026-10-23.json")
	if err != nil {
		t.Fatal(err)
	}
	fakeRoutes(t, map[string]any{"GET /pricing": string(pricing)})

	out, err := run(t, "billing", "plans")
	if err != nil {
		t.Fatalf("plans: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Free", "$0/mo", "$1 of usage every month",
		"Developer", "$20/mo", "$20 of usage every month",
		"Business", "$500/mo",
		"Enterprise", "$2,000/mo", "$2,000 of usage every month",
		"$5", "500 credits", "$100", "10000 credits",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}
