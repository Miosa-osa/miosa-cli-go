package commands

import (
	"net/url"

	"github.com/spf13/cobra"
)

// Protection (a password on a service or a Deploy target) and the WAF that
// sits in front of it.

func newProtectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "protection <project> <service>",
		Short: "Put a password in front of a service",
		Long: `Without flags, shows whether the service is protected.

  miosa protection shop API --password 'open-sesame'
  miosa protection shop API --off
  miosa protection shop API --share-link --expires 3600
  miosa protection shop API --revoke <link-id>

The password is never printed back. A share link is a way past the password for
one person: the token is shown once, and expires after --expires seconds (the
API's default when omitted).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			password, _ := cmd.Flags().GetString("password")
			off, _ := cmd.Flags().GetBool("off")
			share, _ := cmd.Flags().GetBool("share-link")
			expires, _ := cmd.Flags().GetInt("expires")
			env, _ := cmd.Flags().GetString("deploy-target")

			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			projectID, svc, err := openService(cmd.Context(), c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			path := servicePath(projectID, svc.ID) + "/protection"

			revoke, _ := cmd.Flags().GetString("revoke")
			if revoke != "" {
				linkPath := servicePath(projectID, svc.ID) + "/share-links/" + url.PathEscape(revoke)
				if err := c.API.Delete(cmd.Context(), linkPath, nil, nil); err != nil {
					return die(err)
				}
				p.Line("Share link revoked.")
				return nil
			}

			if share {
				body := map[string]any{}
				if expires > 0 {
					body["expires_in_s"] = expires
				}
				var out struct {
					Data map[string]any `json:"data"`
				}
				if err := c.API.Post(cmd.Context(), servicePath(projectID, svc.ID)+"/share-links", body, &out); err != nil {
					return die(err)
				}
				if isJSON() {
					return p.JSON(out.Data)
				}
				p.Line("Share link token: %s", stringOf(out.Data["token"]))
				p.Line("Shown once. Expires %s.", stringOf(out.Data["expires_at"]))
				return nil
			}

			switch {
			case off:
				if err := c.API.Delete(cmd.Context(), path, nil, nil); err != nil {
					return die(err)
				}
				p.Line("Protection removed.")
				return nil
			case password != "":
				if err := c.API.Put(cmd.Context(), path, map[string]any{"mode": "password", "password": password}, nil); err != nil {
					return die(err)
				}
				p.Line("Protection on: visitors must enter the password.")
				return nil
			}

			var out struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), path, nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Fields([][2]string{
				{"Mode", stringOf(out.Data["mode"])},
				{"Password set", yesNo(truthy(out.Data["password_set"]))},
				{"Unlock cookie (s)", stringOf(out.Data["cookie_ttl_s"])},
				{"Scope", stringOf(out.Data["scope"])},
			})
			return nil
		},
	}

	cmd.Flags().String("password", "", "Password visitors must enter (never printed back)")
	cmd.Flags().Bool("off", false, "Remove protection")
	cmd.Flags().Bool("share-link", false, "Mint a share link that skips the password (token shown once)")
	cmd.Flags().Int("expires", 0, "Share link lifetime in seconds (7 days when omitted, at most 90)")
	cmd.Flags().String("revoke", "", "Revoke this share link id")
	cmd.Flags().String("deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func newWAFCmd() *cobra.Command {
	var mode string
	var allow, deny, countryAllow, countryDeny []string
	var rate int

	cmd := &cobra.Command{
		Use:   "waf <project> <service>",
		Short: "Show or change the service's web application firewall",
		Long: `Without flags, shows the current rules.

  miosa waf shop API --mode enforce --deny 203.0.113.0/24 --rate 120
  miosa waf shop API --allow 198.51.100.0/24 --country-allow US --country-allow CA
  miosa waf shop API --off

--mode is off, observe or enforce. Country rules only apply when something in
front of the service sets the country header; without it they are skipped, which
the help says rather than pretending they work. --rate is requests per minute
per source; 0 clears the limit.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			off, _ := cmd.Flags().GetBool("off")
			attack, _ := cmd.Flags().GetString("attack-mode")
			env, _ := cmd.Flags().GetString("deploy-target")

			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			projectID, svc, err := openService(cmd.Context(), c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			path := servicePath(projectID, svc.ID) + "/waf"

			if off {
				if err := c.API.Delete(cmd.Context(), path, nil, nil); err != nil {
					return die(err)
				}
				p.Line("WAF rules removed.")
				return nil
			}

			body := map[string]any{}
			if cmd.Flags().Changed("mode") {
				body["mode"] = mode
			}
			if len(allow) > 0 {
				body["ip_allow"] = allow
			}
			if len(deny) > 0 {
				body["ip_deny"] = deny
			}
			if len(countryAllow) > 0 {
				body["country_allow"] = countryAllow
			}
			if len(countryDeny) > 0 {
				body["country_deny"] = countryDeny
			}
			if cmd.Flags().Changed("rate") {
				body["rate_limit_per_minute"] = rate
			}

			if attack != "" {
				body["attack_mode"] = attack == "on"
			}

			if len(body) > 0 {
				if err := c.API.Put(cmd.Context(), path, body, nil); err != nil {
					return die(err)
				}
				p.Line("WAF rules updated.")
				return nil
			}

			var out struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), path, nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Fields([][2]string{
				{"Mode", stringOf(out.Data["mode"])},
				{"Allow", listString(out.Data["ip_allow"])},
				{"Deny", listString(out.Data["ip_deny"])},
				{"Countries allowed", listString(out.Data["country_allow"])},
				{"Countries denied", listString(out.Data["country_deny"])},
				{"Rate limit (per minute)", stringOf(out.Data["rate_limit_per_minute"])},
				{"Attack mode", yesNo(truthy(out.Data["attack_mode"]))},
				{"Country rules active", yesNo(truthy(out.Data["country_rules_active"]))},
			})
			p.Line("")
			p.Line("Country rules read only the trusted x-miosa-country header the edge sets; without it they are skipped.")
			return nil
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "", "Mode: off, observe or enforce")
	cmd.Flags().StringArrayVar(&allow, "allow", nil, "Allowed source: IP or CIDR (ip_allow; repeatable)")
	cmd.Flags().StringArrayVar(&deny, "deny", nil, "Blocked source: IP or CIDR (ip_deny; repeatable)")
	cmd.Flags().StringArrayVar(&countryAllow, "country-allow", nil, "Allowed country, alpha-2 (repeatable)")
	cmd.Flags().StringArrayVar(&countryDeny, "country-deny", nil, "Blocked country, alpha-2 (repeatable)")
	cmd.Flags().IntVar(&rate, "rate", 0, "Requests per minute per source (0 clears the limit)")
	cmd.Flags().String("attack-mode", "", "Attack mode: on or off")
	cmd.Flags().Bool("off", false, "Remove every WAF rule")
	cmd.Flags().String("deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func listString(value any) string {
	entries, ok := value.([]any)
	if !ok || len(entries) == 0 {
		return "-"
	}
	out := ""
	for i, entry := range entries {
		if i > 0 {
			out += ", "
		}
		out += stringOf(entry)
	}
	return out
}

var _ = url.PathEscape
