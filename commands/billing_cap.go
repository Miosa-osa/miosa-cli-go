package commands

import (
	"fmt"
	"math"
	"net/url"

	"github.com/spf13/cobra"
)

// capSettings is the part of GET /tenants/{id}/sandbox-spend the cap commands read.
type capSettings struct {
	Data struct {
		Org struct {
			CapCents   *int64 `json:"cap_cents"`
			Action     string `json:"action"`
			SpentCents int64  `json:"spent_cents"`
			Status     string `json:"status"`
		} `json:"org"`
		Workspaces []struct {
			WorkspaceID     string `json:"workspace_id"`
			Name            string `json:"name"`
			CapCents        *int64 `json:"cap_cents"`
			TotalCapCents   *int64 `json:"total_cap_cents"`
			TotalSpentCents int64  `json:"total_spent_cents"`
			Action          string `json:"action"`
		} `json:"workspaces"`
	} `json:"data"`
}

func billingCapCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "cap",
		Short: "Monthly spending caps for the organization and its workspaces",
		Long: `A cap is a monthly spending limit. Reaching it either pauses (new spend is refused and
running workloads are stopped, never destroyed, and nothing resumes by itself) or only alerts.
Alerts go out at the organization's thresholds (50, 80, 90 and 100 percent by default) by email and
the billing.spend_threshold_reached webhook.`,
	}
	parent.AddCommand(capShowCmd(), capSetCmd())
	return parent
}

func capShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the organization cap and every workspace cap",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			limits, err := c.GetLimits(cmd.Context())
			if err != nil {
				return die(err)
			}
			var s capSettings
			if err := c.API.Get(cmd.Context(), capPath(limits.Organization.ID), nil, &s); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(s.Data)
			}
			p.Line("Organization  %s of %s this period, action %s", formatCents(s.Data.Org.SpentCents), capCentsLabel(s.Data.Org.CapCents), orDefault(s.Data.Org.Action, "pause"))
			for _, w := range s.Data.Workspaces {
				if w.TotalCapCents == nil && w.CapCents == nil {
					continue
				}
				if w.TotalCapCents != nil {
					p.Line("%-13s %s of %s across all products, action %s", w.Name, formatCents(w.TotalSpentCents), capCentsLabel(w.TotalCapCents), orDefault(w.Action, "pause"))
				}
				if w.CapCents != nil {
					p.Line("%-13s sandboxes capped at %s, action %s", w.Name, capCentsLabel(w.CapCents), orDefault(w.Action, "pause"))
				}
			}
			return nil
		},
	}
}

func capSetCmd() *cobra.Command {
	var org, clear bool
	var workspace, action, scope string
	var monthly float64
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set or clear a monthly cap",
		Long: `Set the organization cap or one workspace's cap, in US dollars a month.

  miosa billing cap set --org --monthly 500 --action pause
  miosa billing cap set --workspace web --monthly 100 --action alert
  miosa billing cap set --workspace web --scope sandboxes --monthly 40
  miosa billing cap set --workspace web --clear

The organization cap and a workspace's default scope ("all") count every product. A workspace
action applies to both of its caps. Only owners and admins can change caps.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org == (workspace != "") {
				return die(fmt.Errorf("pass exactly one of --org or --workspace <workspace>"))
			}
			if !clear && !cmd.Flags().Changed("monthly") && action == "" {
				return die(fmt.Errorf("pass --monthly <usd>, --action pause|alert, or --clear"))
			}
			if clear && cmd.Flags().Changed("monthly") {
				return die(fmt.Errorf("--clear and --monthly cannot be used together"))
			}
			if action != "" && action != "pause" && action != "alert" {
				return die(fmt.Errorf("--action must be pause or alert"))
			}
			if scope != "all" && scope != "sandboxes" {
				return die(fmt.Errorf("--scope must be all or sandboxes"))
			}
			if cmd.Flags().Changed("monthly") && monthly <= 0 {
				return die(fmt.Errorf("--monthly must be more than 0 dollars"))
			}
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			limits, err := c.GetLimits(cmd.Context())
			if err != nil {
				return die(err)
			}
			body := map[string]any{}
			if org {
				if clear {
					body["org_cap_cents"] = nil
				} else if cmd.Flags().Changed("monthly") {
					body["org_cap_cents"] = capCents(monthly)
				}
				if action != "" {
					body["org_cap_action"] = action
				}
			} else {
				id, err := c.API.ResolveWorkspace(cmd.Context(), workspace)
				if err != nil {
					return die(err)
				}
				entry := map[string]any{"workspace_id": id, "scope": scope}
				switch {
				case clear:
					entry["cap_cents"] = nil
				case cmd.Flags().Changed("monthly"):
					entry["cap_cents"] = capCents(monthly)
				default:
					return die(fmt.Errorf("a workspace needs --monthly or --clear; to change only the action, pass the cap again"))
				}
				if action != "" {
					entry["action"] = action
				}
				body["workspace_caps"] = []any{entry}
			}
			var out capSettings
			if err := c.API.Patch(cmd.Context(), capPath(limits.Organization.ID), body, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			if org {
				p.Line("Organization cap is now %s a month, action %s", capCentsLabel(out.Data.Org.CapCents), orDefault(out.Data.Org.Action, "pause"))
			} else {
				p.Line("Updated the cap for %s", workspace)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&org, "org", false, "Set the organization cap")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Set this workspace's cap (slug, name or id)")
	cmd.Flags().Float64Var(&monthly, "monthly", 0, "Cap in US dollars a month")
	cmd.Flags().StringVar(&action, "action", "", "At the cap: pause (refuse new spend, stop running work) or alert")
	cmd.Flags().StringVar(&scope, "scope", "all", "For a workspace: all products, or sandboxes only")
	cmd.Flags().BoolVar(&clear, "clear", false, "Remove the cap")
	return cmd
}

func capPath(orgID string) string { return "/tenants/" + url.PathEscape(orgID) + "/sandbox-spend" }

func capCents(usd float64) int64 { return int64(math.Round(usd * 100)) }
