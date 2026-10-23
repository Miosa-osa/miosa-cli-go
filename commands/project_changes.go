package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// Staged changes are the console's "Apply N changes" bar: edits to a Deploy
// target's services and variables that wait until they are applied together.
// The CLI reads, applies and discards them; staging happens in the console.

type stagedChange struct {
	ID      string `json:"id"`
	Seq     int    `json:"seq"`
	Op      string `json:"op"`
	Summary string `json:"summary"`
}

type changeSet struct {
	ID       string         `json:"id"`
	State    string         `json:"state"`
	Stale    bool           `json:"stale"`
	Changes  []stagedChange `json:"changes"`
	Releases any            `json:"releases"`
}

func projectChangesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Staged changes of a Deploy target: list, apply or discard them",
		Long: `Changes staged in the console (services created or edited, variables set or
removed) wait in a set until they are applied together. Apply writes all of them or
none, and by default deploys every service the set touched, once each.

  miosa project changes list shop
  miosa project changes apply shop
  miosa project changes apply shop --no-deploy
  miosa project changes rm shop <change-id>
  miosa project changes discard shop --yes`,
	}
	cmd.AddCommand(projectChangesListCmd(), projectChangesApplyCmd(), projectChangesRmCmd(), projectChangesDiscardCmd())
	return cmd
}

// changesTarget resolves the project and returns the base path and the
// Deploy target query every changes route takes.
func changesTarget(ctx context.Context, c *api.Client, projectRef, env string) (string, url.Values, error) {
	projectID, err := resolveServiceProject(ctx, c, projectRef)
	if err != nil {
		return "", nil, err
	}
	q := url.Values{}
	if env != "" {
		q.Set("env", env)
	}
	return "/projects/" + url.PathEscape(projectID) + "/changes", q, nil
}

func printChangeSet(cmd *cobra.Command, set changeSet) {
	p := printerFor(cmd)
	if len(set.Changes) == 0 {
		p.Line("No staged changes.")
		return
	}
	rows := make([][]string, 0, len(set.Changes))
	for _, ch := range set.Changes {
		rows = append(rows, []string{strconv.Itoa(ch.Seq), ch.Summary, ch.Op, ch.ID})
	}
	p.Table([]string{"#", "CHANGE", "OP", "ID"}, rows)
	if set.Stale {
		p.Line("The Deploy target changed since these were staged; review them before applying.")
	}
}

func projectChangesListCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:     "list <project>",
		Aliases: []string{"ls"},
		Short:   "List the staged changes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			path, q, err := changesTarget(cmd.Context(), c.API, args[0], env)
			if err != nil {
				return err
			}
			var out struct {
				Data changeSet `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), path, q, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(out.Data)
			}
			printChangeSet(cmd, out.Data)
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func projectChangesApplyCmd() *cobra.Command {
	var env string
	var noDeploy bool
	cmd := &cobra.Command{
		Use:   "apply <project>",
		Short: "Apply the staged changes (all or none), then deploy what they touched",
		Long: `Apply every staged change at once: a change that fails stops the whole set and
nothing is written. Then each service the set touched is deployed once, unless
--no-deploy. A set staged before another apply answers that it is stale; list it,
review, and apply again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			path, q, err := changesTarget(cmd.Context(), c.API, args[0], env)
			if err != nil {
				return err
			}
			if noDeploy {
				q.Set("deploy", "false")
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: http.MethodPost, Path: path + "/apply", Query: q, Body: map[string]any{}})
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(resp)
			}
			n, _ := lookupPath(resp, "data.changes").([]any)
			p.Success("Applied %s", plural(len(n), "change"))
			if releases, _ := lookupPath(resp, "data.releases").([]any); len(releases) > 0 {
				p.Line("Deploying %s; 'miosa service releases <project> <service>' shows each.", plural(len(releases), "service"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	cmd.Flags().BoolVar(&noDeploy, "no-deploy", false, "Apply without deploying the services the changes touched")
	return cmd
}

func projectChangesRmCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:     "rm <project> <change-id>",
		Aliases: []string{"unstage"},
		Short:   "Drop one staged change",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			path, q, err := changesTarget(cmd.Context(), c.API, args[0], env)
			if err != nil {
				return err
			}
			if _, err := c.API.JSONAny(cmd.Context(), api.Request{Method: http.MethodDelete, Path: path + "/" + url.PathEscape(args[1]), Query: q}); err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(map[string]any{"removed": args[1]})
			}
			printerFor(cmd).Success("Dropped change %s", args[1])
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func projectChangesDiscardCmd() *cobra.Command {
	var env string
	var yes bool
	cmd := &cobra.Command{
		Use:   "discard <project>",
		Short: "Drop every staged change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Discard every staged change of %s?", args[0])); err != nil {
					return err
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			path, q, err := changesTarget(cmd.Context(), c.API, args[0], env)
			if err != nil {
				return err
			}
			if _, err := c.API.JSONAny(cmd.Context(), api.Request{Method: http.MethodPost, Path: path + "/discard", Query: q, Body: map[string]any{}}); err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(map[string]any{"discarded": true})
			}
			printerFor(cmd).Success("Discarded the staged changes of %s", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	return cmd
}
