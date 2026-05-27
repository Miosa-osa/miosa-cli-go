package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Manage network policy for a sandbox",
		Long:  `Show or update the egress network policy for a sandbox.`,
	}

	cmd.AddCommand(
		newPolicyShowCmd(),
		newPolicySetCmd(),
	)
	return cmd
}

func newPolicyShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name|id]",
		Short: "Show the current network policy",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runPolicyShow,
	}
}

func runPolicyShow(cmd *cobra.Command, args []string) error {
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
		"sandbox network policy",
		"native sandbox policy routes are not deployed yet, so this command will not fall back to Computer APIs",
	))
}

func newPolicySetCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "set [name|id]",
		Short: "Apply a network policy from a YAML file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runPolicySet(c, args, file)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to policy YAML file (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runPolicySet(cmd *cobra.Command, args []string, file string) error {
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

	data, err := os.ReadFile(file)
	if err != nil {
		return die(fmt.Errorf("reading policy file %q: %w", file, err))
	}

	var policy map[string]interface{}
	if err := yaml.Unmarshal(data, &policy); err != nil {
		// Try JSON as fallback.
		if jsonErr := json.Unmarshal(data, &policy); jsonErr != nil {
			return die(fmt.Errorf("parsing policy file (tried YAML and JSON): %w", err))
		}
	}

	return die(sandboxNativeFeatureUnavailable(
		"sandbox network policy",
		"native sandbox policy routes are not deployed yet, so this command will not fall back to Computer APIs",
	))
}
