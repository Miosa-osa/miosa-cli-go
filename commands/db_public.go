package commands

import (
	"net/url"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	"github.com/spf13/cobra"
)

// Public networking: an opt-in TCP endpoint for one managed database, so a
// laptop or an outside service can reach it. TLS only.

func newDBPublicCmd() *cobra.Command {
	var allow []string

	cmd := &cobra.Command{
		Use:   "public <database> [on|off]",
		Short: "Show, turn on or turn off a database's public endpoint",
		Long: `Without on/off, prints the state. Turning it on opens a TCP endpoint on a
port from 30000-39999, one per database, and prints the host and port.

  miosa db public app-db
  miosa db public app-db on
  miosa db public app-db on --allow 203.0.113.0/24 --allow 198.51.100.7
  miosa db public app-db off

The allowlist REPLACES the stored one; on with no --allow means any source. TLS
is required (Postgres needs sslmode=require), the certificate is self-signed,
and the allowlist closes disallowed sources before any TLS is attempted. MySQL,
Postgres, Redis and MongoDB are supported; other engines answer
ENGINE_NOT_SUPPORTED.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}

			id, err := resolveDatabaseID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			path := "/databases/" + url.PathEscape(id) + "/public-networking"

			action := ""
			if len(args) == 2 {
				action = args[1]
			}

			var out struct {
				Data map[string]any `json:"data"`
			}

			switch action {
			case "":
				if err := c.API.Get(cmd.Context(), path, nil, &out); err != nil {
					return die(err)
				}
			case "on":
				body := map[string]any{}
				if allow != nil {
					body["allowlist"] = allow
				}
				if err := c.API.Post(cmd.Context(), path, body, &out); err != nil {
					return die(err)
				}
			case "off":
				if err := c.API.Delete(cmd.Context(), path, nil, &out); err != nil {
					return die(err)
				}
			default:
				return usagef("expected on or off, got %q", action)
			}

			if isJSON() {
				return p.JSON(out.Data)
			}
			return printPublicNetworking(p, out.Data)
		},
	}

	cmd.Flags().StringArrayVar(&allow, "allow", nil, "Allowed source: an IP or CIDR (repeatable; empty means any source)")
	return cmd
}

func printPublicNetworking(p *output.Printer, data map[string]any) error {
	if enabled, _ := data["enabled"].(bool); !enabled {
		p.Line("Public networking is off.")
		if supported, ok := data["supported"].(bool); ok && !supported {
			p.Line("This engine has no public endpoint.")
		}
		return nil
	}

	p.Fields([][2]string{
		{"Host", stringOf(data["host"])},
		{"Port", stringOf(data["port"])},
		{"Allowlist", allowlistString(data["allowlist"])},
	})
	p.Line("")
	p.Line("TLS only. Postgres: add ?sslmode=require. The certificate is self-signed.")
	return nil
}

func allowlistString(value any) string {
	entries, ok := value.([]any)
	if !ok || len(entries) == 0 {
		return "any source"
	}
	out := ""
	for i, entry := range entries {
		if i > 0 {
			out += ", "
		}
		out += stringOf(entry)
	}
	return out
}
