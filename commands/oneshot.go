package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// attachOneShot makes `miosa run -- <command>` a one-shot: create a sandbox,
// run the command in it, print the output and (with --rm) destroy it. The
// subcommands of `run` (list, get, follow ...) are agent runs and are unchanged.
func attachOneShot(cmd *cobra.Command) {
	var (
		rm             bool
		size, template string
		cwd, name      string
		workspace      string
		vars, tags     []string
	)
	cmd.Args = cobra.ArbitraryArgs
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		if cmd.ArgsLenAtDash() < 0 {
			return usagef("a command goes after --: miosa run --rm -- %s", strings.Join(args, " "))
		}
		command := strings.TrimSpace(joinCommand(args[cmd.ArgsLenAtDash():]))
		if command == "" {
			return usagef("no command given after --")
		}
		body := map[string]any{"command": command, "persistent": !rm}
		if size != "" {
			sz, err := parseSandboxSize(size)
			if err != nil {
				return usagef("%v", err)
			}
			body["size"] = string(sz)
		}
		if template != "" {
			body["template_id"] = template
		}
		if cwd != "" {
			body["cwd"] = cwd
		}
		if name != "" {
			body["name"] = name
		}
		if workspace != "" {
			body["workspace_slug"] = workspace
		}
		if t := commandTimeout(cmd); t > 0 {
			body["timeout"] = t
		}
		if len(vars) > 0 {
			m := map[string]string{}
			for _, kv := range vars {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return usagef("--set expects KEY=VALUE (got %q)", kv)
				}
				m[k] = v
			}
			body["env"] = m
		}
		if len(tags) > 0 {
			m := map[string]string{}
			for _, kv := range tags {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return usagef("--tag expects KEY=VALUE (got %q)", kv)
				}
				m[k] = v
			}
			body["metadata"] = m
		}
		c, _, err := buildClientTimeout(execRequestTimeout(commandTimeout(cmd)) + 120*time.Second)
		if err != nil {
			return die(err)
		}
		resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/sandboxes/run", Body: body, NoRetry: true})
		if err != nil {
			return die(err)
		}
		m, _ := resp.(map[string]any)
		data, _ := m["data"].(map[string]any)
		ex, _ := m["exec"].(map[string]any)
		id := fmt.Sprint(data["id"])

		if rm {
			// Always clean up, even if printing fails.
			defer func() {
				if derr := c.API.Delete(cmd.Context(), "/sandboxes/"+id, nil, nil); derr != nil && !api.IsStatus(derr, 404) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not remove sandbox %s: %v\n", id, derr)
				}
			}()
		}
		code := 0
		if f, ok := toFloat(ex["exit_code"]); ok {
			code = int(f)
		}
		if isJSON() {
			if err := printerFor(cmd).JSON(map[string]any{"sandbox": data, "exec": ex, "removed": rm}); err != nil {
				return err
			}
		} else {
			out := fmt.Sprint(orEmpty(ex["stdout"], ex["output"]))
			fmt.Fprint(cmd.OutOrStdout(), out)
			if e, _ := ex["stderr"].(string); e != "" {
				fmt.Fprint(cmd.ErrOrStderr(), e)
			}
			if !rm {
				label := fmt.Sprint(orEmpty(data["name"], id))
				fmt.Fprintf(cmd.ErrOrStderr(), "kept sandbox %s ('miosa destroy %s' removes it)\n", label, label)
			}
		}
		if code != 0 {
			return &ExitError{Code: code}
		}
		return nil
	}
	f := cmd.Flags()
	f.BoolVar(&rm, "rm", false, "Destroy the sandbox when the command finishes")
	f.StringVar(&size, "size", "", "Sandbox size, as for 'miosa create'")
	f.StringVar(&template, "template", "", "Sandbox template (default miosa-sandbox)")
	f.StringVar(&cwd, "cwd", "", "Working directory for the command")
	f.StringVar(&name, "name", "", "Name for the sandbox")
	f.StringVar(&workspace, "workspace", "", "Workspace slug")
	f.StringArrayVar(&vars, "set", nil, "Environment variable KEY=VALUE for the command (repeatable)")
	f.StringArrayVar(&tags, "meta", nil, "Metadata KEY=VALUE on the sandbox (repeatable)")
}
