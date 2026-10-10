package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

func profileNames(prefix string) []string {
	f, err := config.LoadFile()
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range f.Names() {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

func validProfileName(n string) error {
	if n == "" || strings.ContainsAny(n, " \t./\\=[]\"'") {
		return fmt.Errorf("invalid profile name %q (use letters, digits, - and _)", n)
	}
	return nil
}

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"profiles"},
		Short:   "Manage config profiles (several accounts or environments)",
		Long: `A profile is a named sign-in and set of defaults in ~/.miosa/config.toml.
The profile in effect is --profile, then MIOSA_PROFILE, then the one set with
'profile use', then "default".

  miosa login --profile work
  miosa profile list
  miosa profile use work
  miosa --profile default list`,
	}
	cmd.AddCommand(newProfileListCmd(), newProfileUseCmd(), newProfileAddCmd(), newProfileRemoveCmd(), newProfileShowCmd())
	return cmd
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List profiles",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			f, err := config.LoadFile()
			if err != nil {
				return die(err)
			}
			active := f.ActiveName()
			var rows [][]string
			var data []map[string]any
			for _, n := range f.Names() {
				c, _ := f.Get(n)
				mark := ""
				if n == active {
					mark = "*"
				}
				signed := "no"
				if c.APIKey != "" {
					signed = "yes"
				}
				url := c.APIURL
				if url == "" {
					url = f.Config.APIURL
				}
				rows = append(rows, []string{mark, n, c.Tenant, signed, url, c.DefaultWorkspace})
				data = append(data, map[string]any{"name": n, "active": n == active, "organization": c.Tenant,
					"signed_in": c.APIKey != "", "api_url": url, "workspace": c.DefaultWorkspace})
			}
			if isJSON() {
				return p.JSON(map[string]any{"data": data, "active": active})
			}
			p.Table([]string{"", "PROFILE", "ORG", "SIGNED-IN", "API", "WORKSPACE"}, rows)
			return nil
		},
	}
}

func newProfileUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Make a profile the default for new commands",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return profileNames(toComplete), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			f, err := config.LoadFile()
			if err != nil {
				return die(err)
			}
			if _, ok := f.Get(args[0]); !ok {
				return die(fmt.Errorf("profile %q not found (see 'miosa profile list')", args[0]))
			}
			f.ActiveProfile = args[0]
			if args[0] == config.DefaultProfile {
				f.ActiveProfile = ""
			}
			if err := config.SaveFile(f); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"active": args[0]})
			}
			p.Success("Active profile is now %q", args[0])
			return nil
		},
	}
}

func newProfileAddCmd() *cobra.Command {
	var apiURL, workspace string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create an empty profile (then run 'miosa login --profile <name>')",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			name := args[0]
			if err := validProfileName(name); err != nil {
				return usagef("%v", err)
			}
			if name == config.DefaultProfile {
				return usagef("%q is the built-in profile", name)
			}
			f, err := config.LoadFile()
			if err != nil {
				return die(err)
			}
			if _, ok := f.Profiles[name]; ok {
				return die(fmt.Errorf("profile %q already exists", name))
			}
			f.Set(name, config.Config{APIURL: apiURL, DefaultWorkspace: workspace})
			if err := config.SaveFile(f); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"created": name})
			}
			p.Success("Created profile %q. Sign in with: miosa login --profile %s", name, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&apiURL, "api-url", "", "API base URL for this profile")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Default workspace slug")
	return cmd
}

func newProfileRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete a profile and its stored key",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			name := args[0]
			if name == config.DefaultProfile {
				return usagef("the default profile cannot be removed; use 'miosa logout'")
			}
			f, err := config.LoadFile()
			if err != nil {
				return die(err)
			}
			if _, ok := f.Profiles[name]; !ok {
				return die(fmt.Errorf("profile %q not found", name))
			}
			delete(f.Profiles, name)
			if f.ActiveProfile == name {
				f.ActiveProfile = ""
			}
			if err := config.SaveFile(f); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"removed": name})
			}
			p.Success("Removed profile %q", name)
			return nil
		},
	}
}

func newProfileShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show a profile's settings (the key is masked)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			f, err := config.LoadFile()
			if err != nil {
				return die(err)
			}
			name := f.ActiveName()
			if len(args) == 1 {
				name = args[0]
			}
			c, ok := f.Get(name)
			if !ok {
				return die(fmt.Errorf("profile %q not found", name))
			}
			url := c.APIURL
			if url == "" {
				url = f.Config.APIURL
			}
			if isJSON() {
				return p.JSON(map[string]any{"name": name, "api_url": url, "key": maskKey(c.APIKey), "organization": c.Tenant,
					"workspace": c.DefaultWorkspace, "current_sandbox": c.CurrentSandbox})
			}
			p.Fields([][2]string{{"Profile", name}, {"Organization", c.Tenant}, {"API", url}, {"Key", maskKey(c.APIKey)},
				{"Workspace", c.DefaultWorkspace}, {"Sandbox", c.CurrentSandbox}})
			return nil
		},
	}
}

// ─── config ───────────────────────────────────────────────────────────────────

var configKeys = map[string]func(c *config.Config) *string{
	"api_url":           func(c *config.Config) *string { return &c.APIURL },
	"default_workspace": func(c *config.Config) *string { return &c.DefaultWorkspace },
	"current_sandbox":   func(c *config.Config) *string { return &c.CurrentSandbox },
}

func configKeyNames() []string {
	names := make([]string, 0, len(configKeys))
	for k := range configKeys {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and change CLI settings",
		Long: `Settings live in ~/.miosa/config.toml. Keys: ` + strings.Join(configKeyNames(), ", ") + `.
The API key is never printed here; sign in with 'miosa login'.`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "path", Short: "Print the config file path", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				path, err := config.Path()
				if err != nil {
					return die(err)
				}
				p := printerFor(cmd)
				if isJSON() {
					return p.JSON(map[string]string{"path": path})
				}
				p.Line("%s", path)
				return nil
			},
		},
		&cobra.Command{
			Use: "list", Aliases: []string{"ls"}, Short: "Show the effective settings of the current profile", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				p := printerFor(cmd)
				c, err := config.Load()
				if err != nil {
					return die(err)
				}
				m := map[string]string{}
				for _, k := range configKeyNames() {
					m[k] = *configKeys[k](&c)
				}
				if isJSON() {
					return p.JSON(m)
				}
				for _, k := range configKeyNames() {
					p.Line("%s = %s", k, m[k])
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "get <key>", Short: "Print one setting", Args: cobra.ExactArgs(1),
			ValidArgs: configKeyNames(),
			RunE: func(cmd *cobra.Command, args []string) error {
				get, ok := configKeys[args[0]]
				if !ok {
					return usagef("unknown key %q (keys: %s)", args[0], strings.Join(configKeyNames(), ", "))
				}
				c, err := config.Load()
				if err != nil {
					return die(err)
				}
				p := printerFor(cmd)
				v := *get(&c)
				if isJSON() {
					return p.JSON(map[string]string{args[0]: v})
				}
				p.Line("%s", v)
				return nil
			},
		},
		&cobra.Command{
			Use: "set <key> <value>", Short: "Change one setting in the current profile", Args: cobra.ExactArgs(2),
			ValidArgs: configKeyNames(),
			RunE: func(cmd *cobra.Command, args []string) error {
				get, ok := configKeys[args[0]]
				if !ok {
					return usagef("unknown key %q (keys: %s)", args[0], strings.Join(configKeyNames(), ", "))
				}
				c, err := config.Load()
				if err != nil {
					return die(err)
				}
				*get(&c) = args[1]
				if err := config.Save(c); err != nil {
					return die(err)
				}
				p := printerFor(cmd)
				if isJSON() {
					return p.JSON(map[string]string{args[0]: args[1]})
				}
				p.Success("%s = %s", args[0], args[1])
				return nil
			},
		},
	)
	return cmd
}
