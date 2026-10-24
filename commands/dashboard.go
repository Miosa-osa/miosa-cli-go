package commands

import (
	"context"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// dashboardLink is POST /cli/session/dashboard-link: a one-time, 5-minute
// sign-in link for the person behind this `miosa login` key.
type dashboardLink struct {
	Data struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
		ExpiresIn int    `json:"expires_in"`
	} `json:"data"`
}

func newDashboardCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Open the dashboard, already signed in",
		Long: `Open the MIOSA dashboard in your browser, signed in as you, with a one-time
link that works once within 5 minutes. Your second factor is still asked for
if you have one.

It needs a 'miosa login' session. With any other key (MIOSA_API_KEY, --api-key)
it opens the dashboard's sign-in page instead.

  miosa dashboard
  miosa dashboard --print   # print the one-time link instead of opening it`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildPublicClient()
			if err != nil {
				return die(err)
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			var link dashboardLink
			lerr := c.API.Post(ctx, "/cli/session/dashboard-link", map[string]any{}, &link)
			signedIn := lerr == nil && link.Data.URL != ""
			target := link.Data.URL

			switch {
			case signedIn:
			case lerr == nil || api.IsCode(lerr, "NOT_A_CLI_LOGIN_KEY") || api.IsStatus(lerr, http.StatusNotFound):
				// Not a login key, or an API that predates the link: the plain
				// dashboard still works, it just asks the person to sign in.
				target = consoleURL(c.BaseURL) + "/login"
			default:
				return die(lerr)
			}

			if isJSON() {
				return p.JSON(map[string]any{
					"url":        target,
					"signed_in":  signedIn,
					"expires_at": link.Data.ExpiresAt,
				})
			}
			if !signedIn {
				p.Line("This key cannot sign you in to the dashboard; run 'miosa login' for that.")
			}
			if printOnly {
				p.Line("%s", target)
				if signedIn {
					p.Line("One use, valid until %s. Anyone with this link can sign in as you.", link.Data.ExpiresAt)
				}
				return nil
			}
			if err := openBrowser(target); err != nil {
				p.Line("Open this link in your browser: %s", target)
				return nil
			}
			p.Line("Opened the dashboard in your browser.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "Print the link instead of opening it")
	return cmd
}
