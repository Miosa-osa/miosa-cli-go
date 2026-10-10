package commands

import (
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// steerTarget resolves the machine a steering command acts on and returns the
// API path prefix for it.
func steerTarget(cmd *cobra.Command, ref string, computer bool) (prefix string, label string, err error) {
	c, cfg, err := buildClient()
	if err != nil {
		return "", "", die(err)
	}
	if computer {
		if ref == "" {
			return "", "", usagef("name a computer: miosa run steer --computer <name> \"...\"")
		}
		id, err := c.API.ResolveComputer(cmd.Context(), ref)
		if err != nil {
			return "", "", die(err)
		}
		return "/computers/" + id, ref, nil
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return "", "", usagef("%v", err)
	}
	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return "", "", die(err)
	}
	return "/sandboxes/" + id, ref, nil
}

func newRunSteerCmd() *cobra.Command {
	var sandbox, computer string
	cmd := &cobra.Command{
		Use:   "steer <prompt>",
		Short: "Queue a prompt for the agent already running on a machine",
		Long: `Adds a prompt as the next turn of the agent run attached to a machine. The
agent finishes what it is doing, then a run that continues the same chat starts
with your prompt. With no agent run attached on that machine this fails with
NO_AGENT_RUN. The prompt is the arguments, or "-" for standard input; it can be
up to 64 KiB.

This is what the miosa-queue-prompt helper inside a machine calls.

  miosa run steer --sandbox my-box "also add a changelog entry"
  git diff | miosa run steer --sandbox my-box -`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			if prompt == "-" {
				b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 65536+1))
				if err != nil {
					return die(err)
				}
				prompt = string(b)
			}
			if strings.TrimSpace(prompt) == "" {
				return usagef("the prompt is empty")
			}
			ref := sandbox
			prefix, label, err := steerTarget(cmd, firstNonEmptyStr(computer, ref), computer != "")
			if err != nil {
				return err
			}
			c, _, _ := buildClient()
			resp, err := c.API.JSONAny(cmd.Context(), reqPost(prefix+"/agent/prompts", map[string]string{"prompt": prompt}))
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			printerFor(cmd).Success("Queued for %s after run %s (%s)", label, formatValue(obj["after_run_id"], "short"), formatValue(obj["runner"], ""))
			return nil
		},
	}
	cmd.Flags().StringVarP(&sandbox, "sandbox", "s", "", "Sandbox to steer (default: the current one)")
	cmd.Flags().StringVar(&computer, "computer", "", "Computer to steer")
	return cmd
}

func newRunInterruptCmd() *cobra.Command {
	var sandbox, computer string
	cmd := &cobra.Command{
		Use:   "interrupt",
		Short: "Stop the agent run attached to a machine",
		Long: `Stops the newest queued or running agent run on a machine, the same as
'miosa run stop <id>' without having to know the id. With no agent run attached
this fails with NO_AGENT_RUN. This is what the miosa-stop-agent helper inside a
machine calls.

  miosa run interrupt --sandbox my-box`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			prefix, label, err := steerTarget(cmd, firstNonEmptyStr(computer, sandbox), computer != "")
			if err != nil {
				return err
			}
			c, _, _ := buildClient()
			resp, err := c.API.JSONAny(cmd.Context(), reqPost(prefix+"/agent/stop", nil))
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			printerFor(cmd).Success("Stopped run %s on %s (%s)", formatValue(obj["id"], "short"), label, formatValue(obj["status"], ""))
			return nil
		},
	}
	cmd.Flags().StringVarP(&sandbox, "sandbox", "s", "", "Sandbox (default: the current one)")
	cmd.Flags().StringVar(&computer, "computer", "", "Computer")
	return cmd
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
