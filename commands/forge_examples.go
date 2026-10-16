package commands

import (
	"strings"

	"github.com/spf13/cobra"
)

// forgeExamples gives every Forge command an Examples section (tester item 15,
// 2026-10-16: 20 Forge commands had none while every deploy command did).
// Keys are the command path under `miosa forge`.
var forgeExamples = map[string]string{
	"repo list":      "  miosa forge repo list\n  miosa forge repo list --json",
	"repo show":      "  miosa forge repo show platform\n  miosa forge repo show acme/platform --json",
	"repo update":    "  miosa forge repo update platform --name \"Marketing site\"\n  miosa forge repo update platform --visibility private",
	"repo delete":    "  miosa forge repo delete platform --yes",
	"pr list":        "  miosa forge pr list platform\n  miosa forge pr list platform --state all --author dana --base main",
	"pr new":         "  miosa forge pr new platform --head fix-redirect --title \"Fix redirect loop\"\n  miosa forge pr new platform --head docs --base release --title \"Docs\" --draft",
	"pr show":        "  miosa forge pr show platform 12\n  miosa forge pr show platform 12 --json",
	"pr review":      "  miosa forge pr review platform 12 --decision approved\n  miosa forge pr review platform 12 --decision changes_requested --body \"Needs a test\"",
	"pr check":       "  miosa forge pr check platform 12 --head-sha <sha> --name unit-tests --conclusion passed --file evidence.json",
	"pr merge":       "  miosa forge pr merge platform 12\n  miosa forge pr merge platform 12 --squash\n  miosa forge pr merge platform 12 --rebase --auto",
	"pr close":       "  miosa forge pr close platform 12",
	"pr reopen":      "  miosa forge pr reopen platform 12",
	"pr comments":    "  miosa forge pr comments platform 12",
	"pr comment":     "  miosa forge pr comment platform 12 --body \"Looks good once CI passes\"",
	"pr previews":    "  miosa forge pr previews platform 12",
	"check list":     "  miosa forge check list platform\n  miosa forge check list platform --commit <sha>",
	"check suite":    "  miosa forge check suite platform <commit>",
	"check show":     "  miosa forge check show platform <check-id>",
	"check evidence": "  miosa forge check evidence platform <check-id>",
	"check report":   "  miosa forge check report platform --commit <sha> --name unit-tests --conclusion passed --file evidence.json",
	"release list":   "  miosa forge release list platform",
	"release show":   "  miosa forge release show platform v1.2.0",
	"release new":    "  miosa forge release new platform --tag v1.2.0 --name \"1.2.0\" --body \"Fixes the redirect\"",
	"release tag":    "  miosa forge release tag platform --name v1.2.0 --target main --message \"1.2.0\"",
	"policy show":    "  miosa forge policy show platform\n  miosa forge policy show platform --branch release",
	"policy set":     "  miosa forge policy set platform --branch main --approvals 2 --check unit-tests\n  miosa forge policy set platform --branch main --approvals 0 --independent=false",
	"policy owners":  "  miosa forge policy owners platform",
	"grant set":      "  miosa forge grant set platform <user-id> --role write",
	"fork":           "  miosa forge fork acme/platform --name platform-fork",
}

// applyForgeExamples fills in Example on every Forge command that has none.
func applyForgeExamples(forge *cobra.Command) {
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			p := append(append([]string(nil), path...), sub.Name())
			if sub.Example == "" {
				if ex, ok := forgeExamples[strings.Join(p, " ")]; ok {
					sub.Example = ex
				}
			}
			walk(sub, p)
		}
	}
	walk(forge, nil)
}
