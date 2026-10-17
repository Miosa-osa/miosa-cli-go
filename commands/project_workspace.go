package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
	"github.com/spf13/cobra"
)

// settleProjectWorkspace fills --workspace-id for 'project create', which the
// API refuses without one (WORKSPACE_REQUIRED). A given slug or name becomes
// its id. Without the flag it picks what the console would: the profile's
// default workspace, else the organization's default, else its only one.
func settleProjectWorkspace(cmd *cobra.Command, c *api.Client) error {
	flag := cmd.Flags().Lookup("workspace-id")
	if flag == nil {
		return nil
	}
	if ref := strings.TrimSpace(flag.Value.String()); ref != "" {
		id, err := c.ResolveWorkspace(cmd.Context(), ref)
		if err != nil {
			return die(err)
		}
		return cmd.Flags().Set("workspace-id", id)
	}

	var resp struct {
		Data []struct {
			ID        string `json:"id"`
			Slug      string `json:"slug"`
			IsDefault bool   `json:"is_default"`
		} `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/workspaces", nil, &resp); err != nil {
		return die(fmt.Errorf("finding the default workspace: %w", err))
	}
	list := resp.Data

	pick := func(match func(i int) bool) (string, string, bool) {
		for i, w := range list {
			if match(i) {
				return w.ID, w.Slug, true
			}
		}
		return "", "", false
	}
	cfg, _ := config.Load()
	id, slug, ok := "", "", false
	if want := strings.TrimSpace(cfg.DefaultWorkspace); want != "" {
		id, slug, ok = pick(func(i int) bool { return list[i].Slug == want || list[i].ID == want })
	}
	if !ok {
		id, slug, ok = pick(func(i int) bool { return list[i].IsDefault })
	}
	if !ok && len(list) == 1 {
		id, slug, ok = list[0].ID, list[0].Slug, true
	}
	if !ok {
		slugs := make([]string, 0, len(list))
		for _, w := range list {
			slugs = append(slugs, w.Slug)
		}
		sort.Strings(slugs)
		return usagef("this organization has no default workspace; pass --workspace-id (one of: %s)", strings.Join(slugs, ", "))
	}
	if !isJSON() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Using workspace %s (pass --workspace-id to choose another).\n", slug)
	}
	return cmd.Flags().Set("workspace-id", id)
}
