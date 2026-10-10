package commands

import (
	"context"

	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// consolePages maps a resource kind to its console path. {id} is replaced; a
// kind with no id opens the list page.
var consolePages = map[string]struct{ list, item string }{
	"sandbox":    {"/sandboxes", "/sandboxes/{id}"},
	"computer":   {"/computers", "/computers/{id}"},
	"run":        {"/agents/runs", "/agents/runs?run={id}"},
	"agent":      {"/agents/definitions", "/agents/{id}"},
	"deployment": {"/deploy", "/deploy/{id}"},
	"database":   {"/databases", "/databases/{id}"},
	"workflow":   {"/workflows", "/workflows/{id}"},
	"storage":    {"/storage", "/storage/{id}"},
	"template":   {"/templates", "/templates/{id}"},
	"api-keys":   {"/api-keys", "/api-keys"},
	"billing":    {"/billing", "/billing"},
	"usage":      {"/usage", "/usage"},
	"members":    {"/settings/members", "/settings/members"},
	"webhooks":   {"/settings/webhooks", "/settings/webhooks"},
	"settings":   {"/settings", "/settings"},
	"logs":       {"/logs", "/logs"},
	"docs":       {"", ""},
}

func newOpenCmd() *cobra.Command {
	kinds := make([]string, 0, len(consolePages))
	for k := range consolePages {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "open [kind] [id]",
		Short: "Open the web console (or print its URL)",
		Long: `Open the MIOSA console at a resource. With no arguments it opens the dashboard.
Kinds: ` + strings.Join(kinds, ", ") + `.
A sandbox may be given by name.

  miosa open sandbox my-box
  miosa open run 4f2c...
  miosa open billing
  miosa open --print sandbox my-box   # just print the URL`,
		Args:      cobra.MaximumNArgs(2),
		ValidArgs: kinds,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildPublicClient()
			if err != nil {
				return die(err)
			}
			base := consoleURL(c.BaseURL)
			target := base
			if len(args) >= 1 {
				page, ok := consolePages[args[0]]
				if !ok {
					return usagef("unknown kind %q (kinds: %s)", args[0], strings.Join(kinds, ", "))
				}
				if args[0] == "docs" {
					target = strings.Replace(base, "://", "://docs.", 1)
					if base == "https://miosa.ai" {
						target = "https://miosa.ai/docs"
					}
				} else if len(args) == 1 {
					target = base + page.list
				} else {
					id := args[1]
					if args[0] == "sandbox" && !api.IsUUID(id) {
						ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
						resolved, rerr := c.API.ResolveSandbox(ctx, id)
						cancel()
						if rerr != nil {
							return die(rerr)
						}
						id = resolved
					}
					target = base + strings.ReplaceAll(page.item, "{id}", url.QueryEscape(id))
				}
			}
			if isJSON() {
				return p.JSON(map[string]string{"url": target})
			}
			if printOnly {
				p.Line("%s", target)
				return nil
			}
			if err := openBrowser(target); err != nil {
				p.Line("%s", target)
				return nil
			}
			p.Line("Opened %s", target)
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "Print the URL instead of opening it")
	return cmd
}

// cachedSandboxNames returns sandbox names (and ids) for shell completion.
func cachedSandboxNames(ctx context.Context) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, _, err := buildClient()
	if err != nil {
		return nil
	}
	c.API.MaxRetries = 0
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("limit", "100")
	if err := c.API.Get(ctx, "/sandboxes", q, &out); err != nil {
		return nil
	}
	names := []string{"current"}
	for _, s := range out.Data {
		if s.Name != "" {
			names = append(names, s.Name)
		} else {
			names = append(names, s.ID)
		}
	}
	return names
}
