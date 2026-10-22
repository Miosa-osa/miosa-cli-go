package commands

import (
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// newEnvironmentCmd is `miosa environment`: the Railway-shaped name for a
// project's Deploy targets. `miosa project target` does the same and keeps
// working.
func newEnvironmentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "environment",
		Aliases: []string{"environments"},
		Short:   "List, create, fork and remove a project's Deploy targets",
		Long: `Deploy targets are what Railway calls environments: production, staging, one
per preview. The linked project is used unless a project is named.

  miosa environment list
  miosa environment new staging
  miosa environment fork production --name staging
  miosa environment rm staging`,
	}
	cmd.AddCommand(
		environmentListCmd(),
		environmentNewCmd(),
		environmentForkCmd(),
		environmentRemoveCmd(),
	)
	return cmd
}

// environmentProject resolves an explicit project or the linked one.
func environmentProject(cmd *cobra.Command, args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		c, _, err := buildClient()
		if err != nil {
			return "", die(err)
		}
		return resolveServiceProject(cmd.Context(), c.API, args[0])
	}
	link, err := readLink()
	if err != nil {
		return "", usagef("%v", err)
	}
	return link.ProjectID, nil
}

func environmentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [project]",
		Short: "List a project's Deploy targets",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			projectID, err := environmentProject(cmd, args)
			if err != nil {
				return err
			}
			envs, err := listServiceEnvironments(cmd.Context(), c.API, projectID)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(envs)
			}
			rows := make([][]string, 0, len(envs))
			for _, e := range envs {
				rows = append(rows, []string{e.Name, e.Slug, yesNo(e.IsDefault), shortID(e.ID)})
			}
			p.Table([]string{"NAME", "SLUG", "DEFAULT", "ID"}, rows)
			return nil
		},
	}
}

func environmentNewCmd() *cobra.Command {
	var slug string
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a Deploy target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			projectID, err := environmentProject(cmd, nil)
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0]}
			if slug != "" {
				body["slug"] = slug
			}
			var out struct {
				Data serviceEnvironment `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), "/projects/"+url.PathEscape(projectID)+"/environments", body, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Created Deploy target %q", out.Data.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "URL-safe name (default: from the name)")
	return cmd
}

func environmentForkCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "fork <from>",
		Short: "Copy a Deploy target's settings into a new one",
		Long: `Copy a Deploy target: its services, variables and settings, without its data.

  miosa environment fork production --name staging`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if strings.TrimSpace(name) == "" {
				return usagef("give the new target a name: --name staging")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			projectID, err := environmentProject(cmd, nil)
			if err != nil {
				return err
			}
			from, err := resolveServiceEnvironment(cmd.Context(), c.API, projectID, args[0])
			if err != nil {
				return err
			}
			var out struct {
				Data serviceEnvironment `json:"data"`
			}
			path := "/projects/" + url.PathEscape(projectID) + "/environments/" + url.PathEscape(from.ID) + "/duplicate"
			if err := c.API.Post(cmd.Context(), path, map[string]any{"name": name}, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Forked %s into %q", from.Name, out.Data.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Name for the new Deploy target")
	return cmd
}

func environmentRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a Deploy target and everything in it",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			projectID, err := environmentProject(cmd, nil)
			if err != nil {
				return err
			}
			env, err := resolveServiceEnvironment(cmd.Context(), c.API, projectID, args[0])
			if err != nil {
				return err
			}
			if env.IsDefault {
				return usagef("%s is the default Deploy target; make another one default first", env.Name)
			}
			if !yes {
				if err := confirmDestructive(cmd, "Delete Deploy target "+env.Name+" and everything in it?"); err != nil {
					return err
				}
			}
			path := "/projects/" + url.PathEscape(projectID) + "/environments/" + url.PathEscape(env.ID)
			if err := c.API.Delete(cmd.Context(), path, nil, nil); err != nil {
				return die(err)
			}
			p.Success("Deleted Deploy target %q", env.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask")
	return cmd
}
