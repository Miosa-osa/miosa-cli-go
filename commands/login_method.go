package commands

import (
	"net/url"

	"github.com/spf13/cobra"
)

// signInMethod is the --github/--google/--email/--signup choice shared by
// login and onboard. It travels to the browser as ?method= on the approval
// link, so the sign-in page goes straight to that way in.
type signInMethod struct {
	github, google, email, signup bool
}

func (m *signInMethod) register(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&m.github, "github", false, "Sign in with GitHub")
	cmd.Flags().BoolVar(&m.google, "google", false, "Sign in with Google")
	cmd.Flags().BoolVar(&m.email, "email", false, "Sign in with your email address")
	cmd.Flags().BoolVar(&m.signup, "signup", false, "Create a MIOSA account instead of signing in to one")
}

func (m signInMethod) resolve() (string, error) {
	chosen := ""
	for name, on := range map[string]bool{"github": m.github, "google": m.google, "email": m.email, "signup": m.signup} {
		if !on {
			continue
		}
		if chosen != "" {
			return "", usagef("give one of --github, --google, --email or --signup")
		}
		chosen = name
	}
	return chosen, nil
}

// withSignInMethod adds the chosen method to the approval link.
func withSignInMethod(link, method string) string {
	if method == "" {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	q := u.Query()
	q.Set("method", method)
	u.RawQuery = q.Encode()
	return u.String()
}
