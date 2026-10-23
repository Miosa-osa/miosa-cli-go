package commands

import (
	"context"
	"net/url"
	"strconv"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	"strings"

	"github.com/spf13/cobra"
)

// Log drains: one HTTPS (or TLS syslog) endpoint per Deploy target that
// receives the services' logs as they happen.

func newDrainsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "drains",
		Aliases: []string{"drain"},
		Short:   "Send a Deploy target's logs to your own endpoint",
		Long: `A drain forwards runtime and build logs to an endpoint you control: HTTPS as
one JSON object per line, or syslog over TLS.

  miosa drains list
  miosa drains add --name ops --url https://logs.example.test/ingest
  miosa drains add --name archive --kind syslog --url syslog+tls://logs.example.org:6514
  miosa drains test ops
  miosa drains pause ops
  miosa drains rotate-secret ops
  miosa drains rm ops

An HTTPS drain gets a signing secret once, on add and on rotate: copy it then,
it is not shown again. A syslog drain has no secret, because TLS is the
authenticity.`,
	}
	cmd.PersistentFlags().String("project", "", "Project (name, slug or id; default: the linked one)")
	cmd.PersistentFlags().String("environment", "", "Deploy target (name, slug or id; default: the project's default)")

	cmd.AddCommand(
		drainsListCmd(),
		drainsAddCmd(),
		drainsStateCmd("pause", "paused", "Stop delivering lines, keeping the drain"),
		drainsStateCmd("resume", "active", "Deliver lines again"),
		drainsRotateCmd(),
		drainsTestCmd(),
		drainsRemoveCmd(),
	)
	return cmd
}

type drainsTarget struct {
	projectID string
	envID     string
}

func drainsPath(t drainsTarget, suffix string) string {
	path := "/projects/" + url.PathEscape(t.projectID) + "/environments/" + url.PathEscape(t.envID) + "/log-drains"
	return path + suffix
}

func drainsTargetFor(cmd *cobra.Command) (drainsTarget, error) {
	c, _, err := buildClient()
	if err != nil {
		return drainsTarget{}, die(err)
	}
	ctx := cmd.Context()

	projectRef, _ := cmd.Flags().GetString("project")
	envRef, _ := cmd.Flags().GetString("environment")

	projectID, err := environmentProject(cmd, []string{projectRef})
	if err != nil {
		return drainsTarget{}, err
	}

	var out struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Slug      string `json:"slug"`
			IsDefault bool   `json:"is_default"`
		} `json:"data"`
	}
	if err := c.API.Get(ctx, "/projects/"+url.PathEscape(projectID)+"/environments", nil, &out); err != nil {
		return drainsTarget{}, die(err)
	}

	for _, env := range out.Data {
		if envRef == "" && env.IsDefault {
			return drainsTarget{projectID: projectID, envID: env.ID}, nil
		}
		if envRef != "" && (strings.EqualFold(env.ID, envRef) || strings.EqualFold(env.Name, envRef) || strings.EqualFold(env.Slug, envRef)) {
			return drainsTarget{projectID: projectID, envID: env.ID}, nil
		}
	}

	if envRef == "" {
		return drainsTarget{}, usagef("this project has no default Deploy target yet; name one with --environment")
	}
	return drainsTarget{}, usagef("no Deploy target called %q in this project; 'miosa environment list' shows them", envRef)
}

type drainRow struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	URL            string   `json:"url"`
	Sources        []string `json:"sources"`
	State          string   `json:"state"`
	DeliveredLines int64    `json:"delivered_lines"`
	DroppedLines   int64    `json:"dropped_lines"`
	FailedBatches  int64    `json:"failed_batches"`
	LastError      string   `json:"last_error"`
	LastDelivered  string   `json:"last_delivered_at"`
	Secret         string   `json:"secret"`
}

func drainsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the drains of a Deploy target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			target, err := drainsTargetFor(cmd)
			if err != nil {
				return err
			}

			var out struct {
				Data []drainRow `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), drainsPath(target, ""), nil, &out); err != nil {
				return die(err)
			}

			if isJSON() {
				return p.JSON(out.Data)
			}
			if len(out.Data) == 0 {
				p.Line("No drains. Add one with: miosa drains add --name ops --url https://logs.example.test/ingest")
				return nil
			}

			rows := make([][]string, 0, len(out.Data))
			for _, d := range out.Data {
				rows = append(rows, []string{
					d.Name, d.Kind, d.URL, strings.Join(d.Sources, ","), d.State,
					intString(d.DeliveredLines), intString(d.DroppedLines), intString(d.FailedBatches), orDash(d.LastError),
				})
			}
			p.Table([]string{"NAME", "KIND", "URL", "SOURCES", "STATE", "DELIVERED", "DROPPED", "FAILED", "LAST ERROR"}, rows)
			return nil
		},
	}
}

func drainsAddCmd() *cobra.Command {
	var name, endpoint, kind string
	var sources []string

	cmd := &cobra.Command{
		Use:   "add --name <name> --url <url>",
		Short: "Create a drain and show its secret once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			target, err := drainsTargetFor(cmd)
			if err != nil {
				return err
			}

			body := map[string]any{"name": name, "url": endpoint}
			if kind != "" {
				body["kind"] = kind
			}
			if len(sources) > 0 {
				body["sources"] = sources
			}

			var out struct {
				Data drainRow `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), drainsPath(target, ""), body, &out); err != nil {
				return die(err)
			}

			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Line("Created drain %s (%s) delivering %s.", out.Data.Name, out.Data.Kind, strings.Join(out.Data.Sources, ", "))
			printDrainSecret(p, out.Data.Secret)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Drain name: 1-63 lowercase letters, digits and hyphens")
	cmd.Flags().StringVar(&endpoint, "url", "", "Endpoint: https://... or, with --kind syslog, syslog+tls://host[:port]")
	cmd.Flags().StringVar(&kind, "kind", "http", "Kind: http (default) or syslog")
	cmd.Flags().StringArrayVar(&sources, "source", nil, "Which logs: runtime, build (repeatable; default both)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func drainsStateCmd(use, state, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <name>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchDrain(cmd, args[0], map[string]any{"state": state})
		},
	}
}

func drainsRotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-secret <name>",
		Short: "Replace an HTTPS drain's signing secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchDrain(cmd, args[0], map[string]any{"rotate_secret": true})
		},
	}
}

func patchDrain(cmd *cobra.Command, name string, body map[string]any) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	target, err := drainsTargetFor(cmd)
	if err != nil {
		return err
	}
	id, err := findDrain(cmd, c.API, target, name)
	if err != nil {
		return err
	}

	var out struct {
		Data drainRow `json:"data"`
	}
	if err := c.API.Patch(cmd.Context(), drainsPath(target, "/"+url.PathEscape(id)), body, &out); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(out.Data)
	}
	p.Line("Drain %s is %s.", out.Data.Name, out.Data.State)
	printDrainSecret(p, out.Data.Secret)
	return nil
}

func drainsTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <name>",
		Short: "Send one signed line now and report what the endpoint answered",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			target, err := drainsTargetFor(cmd)
			if err != nil {
				return err
			}
			id, err := findDrain(cmd, c.API, target, args[0])
			if err != nil {
				return err
			}

			var out struct {
				Data struct {
					Delivered bool   `json:"delivered"`
					Error     string `json:"error"`
				} `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), drainsPath(target, "/"+url.PathEscape(id)+"/test"), nil, &out); err != nil {
				return die(err)
			}

			if isJSON() {
				return p.JSON(out.Data)
			}
			if out.Data.Delivered {
				p.Line("Delivered.")
				return nil
			}
			p.Line("Not delivered: %s", out.Data.Error)
			return nil
		},
	}
}

func drainsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete a drain",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			target, err := drainsTargetFor(cmd)
			if err != nil {
				return err
			}
			id, err := findDrain(cmd, c.API, target, args[0])
			if err != nil {
				return err
			}

			if err := c.API.Delete(cmd.Context(), drainsPath(target, "/"+url.PathEscape(id)), nil, nil); err != nil {
				return die(err)
			}
			p.Line("Deleted drain %s.", args[0])
			return nil
		},
	}
}

func findDrain(cmd *cobra.Command, api interface {
	Get(ctx context.Context, path string, q url.Values, out any) error
}, target drainsTarget, name string) (string, error) {
	var out struct {
		Data []drainRow `json:"data"`
	}
	if err := api.Get(cmd.Context(), drainsPath(target, ""), nil, &out); err != nil {
		return "", die(err)
	}
	for _, d := range out.Data {
		if strings.EqualFold(d.Name, name) || d.ID == name {
			return d.ID, nil
		}
	}
	return "", usagef("no drain called %q here; 'miosa drains list' shows them", name)
}

// The signing secret is returned only on create and rotate, so the one place it
// can be seen is printed loudly and never stored.
func printDrainSecret(p *output.Printer, secret string) {
	if secret == "" {
		return
	}
	p.Line("")
	p.Line("Signing secret: %s", secret)
	p.Line("Copy it now: it is not shown again. Rotate with 'miosa drains rotate-secret'.")
}

func intString(n int64) string {
	return strconv.FormatInt(n, 10)
}
