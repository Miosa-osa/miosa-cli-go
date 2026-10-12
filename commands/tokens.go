package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(tokenCmd()) }

// Scoped delegation tokens: a platform authenticates with its master key and
// mints short-lived tokens bound to one end user and one workspace, which it
// hands to that user's browser. The token carries only the scopes asked for,
// never more than the minting key holds.
//
// A scoped (or sandbox) token cannot itself mint or manage tokens: the API
// answers 403.
func tokenCmd() *cobra.Command {
	cmd := group("token", "Mint short-lived tokens for your end users",
		`Tokens here are for products that act for their own customers. Your backend
authenticates with its msk_* key and mints a token bound to one end user and one
workspace; that token carries a non-empty subset of your key's own scopes and
expires within a day at most (default one hour). Hand it to the end user's
browser; never log it.

A token minted here cannot mint or manage other tokens.

  miosa token create cus_123 ws_456 --scope sandboxes:read --expires-in 3600
  miosa token list
  miosa token revoke <id>`,
		[]string{"tokens", "delegation"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List the tokens still valid (newest first)",
			Method: "GET", Path: "/tokens/scoped",
			Cols: tokenCols,
			Flags: []Flag{
				{Name: "before", Usage: "Page backwards from this cursor", In: "query"},
			},
		},
		Op{
			Use: "revoke <id>", Aliases: []string{"rm", "delete"}, Short: "Revoke a token (it stops working at once)",
			Method: "DELETE", Path: "/tokens/scoped/{0}", Args: []Arg{{Name: "token"}},
			Confirm: "Revoke token {0}? Anything using it stops working.", Done: "Revoked {0}",
		},
	)
	cmd.AddCommand(newTokenCreateCmd())
	return cmd
}

var tokenCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "END USER", Path: "user_id"},
	{Head: "WORKSPACE", Path: "workspace_id", Fmt: "short"}, {Head: "SCOPES", Path: "scopes", Fmt: "count"},
	{Head: "EXPIRES", Path: "expires_at"}, {Head: "REVOKED", Path: "revoked_at"},
}

func newTokenCreateCmd() *cobra.Command {
	var (
		scopes     []string
		expiresIn  int
		quietToken bool
	)
	cmd := &cobra.Command{
		Use:   "create <user> <workspace>",
		Short: "Mint a short-lived token for one end user and workspace",
		Long: `Mint a delegation token. <user> is your own id for the end user; <workspace> is the
workspace the token is bound to and must belong to your organization. --scope is
required and may not exceed your own key's scopes.

The token is printed once. --quiet-token prints only the token.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			body := map[string]any{
				"user_id":      args[0],
				"workspace_id": args[1],
				"scopes":       scopes,
			}
			if expiresIn > 0 {
				body["expires_in_seconds"] = expiresIn
			}
			resp, err := c.API.JSONAny(cmd.Context(), reqPost("/tokens/scoped", body))
			if err != nil {
				return die(err)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			token, _ := obj["token"].(string)
			switch {
			case quietToken:
				fmt.Fprintln(cmd.OutOrStdout(), token)
			case isJSON():
				return p.JSON(resp)
			default:
				p.Success("Minted a token for %q. It is shown once - hand it to the browser now:", args[0])
				fmt.Fprintln(p.Writer())
				fmt.Fprintf(p.Writer(), "  %s\n\n", token)
				p.Fields([][2]string{
					{"Id", formatValue(obj["id"], "")},
					{"Expires", formatValue(obj["expires_at"], "")},
					{"Scopes", fmt.Sprint(len(toStrings(obj["scopes"])))},
				})
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&scopes, "scope", nil, "A scope to grant, for example sandboxes:read (repeatable)")
	f.IntVar(&expiresIn, "expires-in", 0, "Lifetime in seconds, 1 to 86400 (default 3600)")
	f.BoolVar(&quietToken, "quiet-token", false, "Print only the token")
	_ = cmd.MarkFlagRequired("scope")
	return cmd
}
