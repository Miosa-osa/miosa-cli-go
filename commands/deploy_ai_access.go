package commands

import (
	"context"
	"net/url"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

// checkAIAccess validates --ai-access before anything is created. Empty means "leave it alone".
func checkAIAccess(value string) error {
	switch value {
	case "", "allow", "block":
		return nil
	}
	return usagef("--ai-access must be allow or block (got %q)", value)
}

// setAIAccess sets whether agents may read a published site. allow is the default: the static edge
// then serves a generated robots.txt and llms.txt unless the upload has its own. block asks agents to
// stay away (robots.txt Disallow, X-Robots-Tag noai) and answers 403 to a request whose User-Agent names
// one. That enforcement is UA-based, so it is a strong request and a best-effort gate, not a guarantee.
func setAIAccess(ctx context.Context, c *client.Client, deployment, value string) error {
	return c.API.Patch(ctx, "/deployments/"+url.PathEscape(deployment), map[string]any{"ai_access": value}, nil)
}
