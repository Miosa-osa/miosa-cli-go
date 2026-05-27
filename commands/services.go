package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newServicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Manage supervised services in a sandbox",
		Long:  `Create, list, start, stop, and tail logs for supervised processes inside a sandbox.`,
	}

	cmd.AddCommand(
		newServicesListCmd(),
		newServicesCreateCmd(),
		newServicesStartCmd(),
		newServicesStopCmd(),
		newServicesRestartCmd(),
		newServicesDeleteCmd(),
		newServicesLogsCmd(),
	)
	return cmd
}

func newServicesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list [name|id]",
		Aliases: []string{"ls"},
		Short:   "List services in a sandbox",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runServicesList,
	}
}

func runServicesList(cmd *cobra.Command, args []string) error {
	_, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID := ""
	if len(args) > 0 {
		nameOrID = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	return die(sandboxNativeFeatureUnavailable(
		"sandbox supervised services",
		"use sandbox exec/template start today; native service routes will ship as sandbox APIs before this command is enabled",
	))
}

func newServicesCreateCmd() *cobra.Command {
	var (
		name    string
		command string
		restart string
	)
	cmd := &cobra.Command{
		Use:   "create [name|id]",
		Short: "Create a supervised service",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runServicesCreate(c, args, name, command, restart)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Service name (required)")
	cmd.Flags().StringVar(&command, "command", "", "Command to run (required)")
	cmd.Flags().StringVar(&restart, "restart", "on-failure", "Restart policy: always, on-failure, or no")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("command")
	return cmd
}

func runServicesCreate(cmd *cobra.Command, args []string, name, command, restart string) error {
	_, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID := ""
	if len(args) > 0 {
		nameOrID = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	switch restart {
	case "always", "on-failure", "no":
	default:
		return die(fmt.Errorf("invalid restart policy %q: must be always, on-failure, or no", restart))
	}

	return die(sandboxNativeFeatureUnavailable(
		"sandbox supervised services",
		"use sandbox exec/template start today; native service routes will ship as sandbox APIs before this command is enabled",
	))
}

func newServicesStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <service>",
		Short: "Start a service",
		Args:  cobra.ExactArgs(1),
		RunE:  runServicesLifecycle("start"),
	}
}

func newServicesStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <service>",
		Short: "Stop a service",
		Args:  cobra.ExactArgs(1),
		RunE:  runServicesLifecycle("stop"),
	}
}

func newServicesRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <service>",
		Short: "Restart a service",
		Args:  cobra.ExactArgs(1),
		RunE:  runServicesLifecycle("restart"),
	}
}

func newServicesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <service>",
		Short: "Delete a service",
		Args:  cobra.ExactArgs(1),
		RunE:  runServicesLifecycle("delete"),
	}
}

func runServicesLifecycle(action string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		_, cfg, err := buildClient()
		if err != nil {
			return die(err)
		}

		nameOrID, err := requireSandbox("", cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		_ = nameOrID
		return die(sandboxNativeFeatureUnavailable(
			"sandbox supervised services",
			"use sandbox exec/template start today; native service routes will ship as sandbox APIs before this command is enabled",
		))
	}
}

func newServicesLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Tail service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runServicesLogs(c, args, follow)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream logs continuously")
	return cmd
}

func runServicesLogs(cmd *cobra.Command, args []string, follow bool) error {
	_, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID, err := requireSandbox("", cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}
	_ = nameOrID
	_ = follow

	return die(sandboxNativeFeatureUnavailable(
		"sandbox supervised service logs",
		"use GET /api/v1/sandboxes/{id}/logs for template/app logs until native service routes are deployed",
	))
}
