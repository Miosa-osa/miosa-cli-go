package commands

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"
)

// Edge middleware: rules and an optional function that run at the edge in
// front of a server service, stored in the service's settings.

func newMiddlewareCmd() *cobra.Command {
	var from string
	var off bool

	cmd := &cobra.Command{
		Use:   "middleware <project> <service>",
		Short: "Show, set or clear a service's edge middleware",
		Long: `Edge middleware runs in front of a server service: ordered path rules
(redirect, rewrite, request and response headers) and, optionally, a published
function that can answer, continue or rewrite.

  miosa middleware shop API --from middleware.json
  miosa middleware shop API            # what is configured now
  miosa middleware shop API --off

The file is the rule set exactly as the API takes it:

  {
    "rules": [
      {"path": "/old/*", "redirect": {"to": "/new", "status": 308}},
      {"path": "/api/*", "request_headers": {"set": {"x-plan": "pro"}}}
    ],
    "function": {"slug": "gate", "paths": ["/app/*"], "timeout_ms": 250, "on_error": "closed"}
  }

Rules and the function are validated by the API; its error names the path
(settings.edge_middleware.<field>). Changes apply within about five seconds.
Protected header names (host, content-length, x-miosa-*, x-forwarded-* and
the rest) are refused.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			env, _ := cmd.Flags().GetString("deploy-target")

			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			projectID, svc, err := openService(cmd.Context(), c.API, args[0], args[1], env)
			if err != nil {
				return err
			}

			if off {
				body := map[string]any{
					"lock_version": svc.LockVersion,
					"settings":     map[string]any{"edge_middleware": nil},
				}
				if err := c.API.Patch(cmd.Context(), servicePath(projectID, svc.ID), body, nil); err != nil {
					return die(err)
				}
				p.Line("Edge middleware cleared.")
				return nil
			}

			if from != "" {
				raw, err := os.ReadFile(from)
				if err != nil {
					return die(err)
				}
				var rules map[string]any
				if err := json.Unmarshal(raw, &rules); err != nil {
					return usagef("%s is not valid JSON: %v", from, err)
				}
				body := map[string]any{
					"lock_version": svc.LockVersion,
					"settings":     map[string]any{"edge_middleware": rules},
				}
				if err := c.API.Patch(cmd.Context(), servicePath(projectID, svc.ID), body, nil); err != nil {
					return die(err)
				}
				p.Line("Edge middleware set from %s.", from)
				return nil
			}

			if isJSON() {
				return p.JSON(svc.Settings["edge_middleware"])
			}
			current, ok := svc.Settings["edge_middleware"]
			if !ok || current == nil {
				p.Line("No edge middleware on this service.")
				return nil
			}
			encoded, _ := json.MarshalIndent(current, "", "  ")
			p.Line("%s", string(encoded))
			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "JSON file holding the rule set")
	cmd.Flags().BoolVar(&off, "off", false, "Remove every rule and the function")
	cmd.Flags().String("deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}
