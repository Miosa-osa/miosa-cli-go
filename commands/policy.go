package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// Network policy is a computer feature (/computers/:id/network-policy).
// Sandboxes are governed by the organization's egress policies instead.

func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Show or change a computer's network policy",
		Long: `Show, apply or reset the egress network policy of a computer (a name or id).
Sandboxes use organization-wide egress policies instead; see 'miosa egress'.`,
	}
	cmd.AddCommand(newPolicyShowCmd(), newPolicySetCmd(), newPolicyResetCmd())
	return cmd
}

func policyNotComputer(err error, ref string) error {
	if api.IsStatus(err, 400) || api.IsStatus(err, 404) {
		return &api.Error{Status: 404, Code: "NOT_FOUND", Message: fmt.Sprintf("no computer %q: network policy is a computer setting (sandboxes use 'miosa egress')", ref)}
	}
	return err
}

func newPolicyShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <computer>",
		Short: "Show the current network policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: "/computers/" + args[0] + "/network-policy"})
			if err != nil {
				return die(policyNotComputer(err, args[0]))
			}
			return p.JSON(resp)
		},
	}
}

func newPolicySetCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "set <computer>",
		Short: "Apply a network policy from a YAML or JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			data, err := os.ReadFile(file)
			if err != nil {
				return die(fmt.Errorf("reading policy file %q: %w", file, err))
			}
			var policy map[string]interface{}
			if err := yaml.Unmarshal(data, &policy); err != nil {
				if jsonErr := json.Unmarshal(data, &policy); jsonErr != nil {
					return die(fmt.Errorf("parsing policy file (tried YAML and JSON): %w", err))
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if err := c.API.Put(cmd.Context(), "/computers/"+args[0]+"/network-policy", policy, nil); err != nil {
				return die(policyNotComputer(err, args[0]))
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "applied", "computer": args[0]})
			}
			p.Success("Applied network policy to %s", args[0])
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to the policy YAML or JSON file (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newPolicyResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset <computer>",
		Short: "Remove the policy and return to the default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if err := c.API.Delete(cmd.Context(), "/computers/"+args[0]+"/network-policy", nil, nil); err != nil {
				return die(policyNotComputer(err, args[0]))
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "reset", "computer": args[0]})
			}
			p.Success("Reset network policy of %s", args[0])
			return nil
		},
	}
}
