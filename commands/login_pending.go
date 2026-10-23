package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// A two-step sign-in for agents: `login --no-wait` starts the browser flow and
// keeps its device code in ~/.miosa/pending-login.json (0600, never printed);
// `login --wait` finishes it. The code is good only until the person approves
// it once or it expires.

type pendingLogin struct {
	DeviceCode string    `json:"device_code"`
	UserCode   string    `json:"user_code"`
	URL        string    `json:"url"`
	Interval   int       `json:"interval"`
	ExpiresAt  time.Time `json:"expires_at"`
	APIURL     string    `json:"api_url"`
	Profile    string    `json:"profile"`
}

func pendingLoginPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pending-login.json"), nil
}

func loadPendingLogin() (pendingLogin, bool) {
	var pl pendingLogin
	p, err := pendingLoginPath()
	if err != nil {
		return pl, false
	}
	data, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(data, &pl) != nil || pl.DeviceCode == "" {
		return pendingLogin{}, false
	}
	if time.Now().After(pl.ExpiresAt) || pl.Profile != profileLabel() {
		return pendingLogin{}, false
	}
	return pl, true
}

func savePendingLogin(pl pendingLogin) error {
	p, err := pendingLoginPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(pl)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func clearPendingLogin() {
	if p, err := pendingLoginPath(); err == nil {
		_ = os.Remove(p)
	}
}

func loginBaseURL() string {
	if u := globalFlags.APIURL; u != "" {
		return u
	}
	if u := os.Getenv("MIOSA_BASE_URL"); u != "" {
		return u
	}
	if cur, err := config.Load(); err == nil && cur.APIURL != "" {
		return cur.APIURL
	}
	return config.DefaultBaseURL
}

// startPendingLogin is `login --no-wait`: print the link and code for a person
// and return. A sign-in already pending for this profile is shown again
// unless --new.
func startPendingLogin(cmd *cobra.Command, o loginOptions) error {
	pl, ok := loadPendingLogin()
	replaced := false
	if !ok || o.fresh {
		replaced = ok
		ac := api.New(loginBaseURL(), "")
		ac.UserAgent = "miosa-cli/" + cliVersion
		ac.MaxRetries = 2
		orgHeaderOff = true
		st, err := startDevice(cmd, ac, o)
		orgHeaderOff = false
		if err != nil {
			return die(err)
		}
		url := st.VerificationURIComplete
		if url == "" {
			url = st.VerificationURI
		}
		expires := time.Duration(st.ExpiresIn) * time.Second
		if expires <= 0 {
			expires = 10 * time.Minute
		}
		pl = pendingLogin{DeviceCode: st.DeviceCode, UserCode: st.UserCode, URL: url, Interval: st.Interval,
			ExpiresAt: time.Now().Add(expires).UTC(), APIURL: loginBaseURL(), Profile: profileLabel()}
		if err := savePendingLogin(pl); err != nil {
			return die(fmt.Errorf("saving the pending sign-in: %w", err))
		}
	}
	p := printerFor(cmd)
	if isJSON() {
		out := map[string]any{"status": "pending", "url": pl.URL, "user_code": pl.UserCode, "expires_at": pl.ExpiresAt.Format(time.RFC3339), "next": "miosa login --wait"}
		if replaced {
			out["event"] = "login_replaced"
		}
		return p.JSON(out)
	}
	p.Line("Ask the person who owns the account to open this link and confirm the code %s:", pl.UserCode)
	p.Line("  %s", pl.URL)
	p.Line("It expires at %s. Then run: miosa login --wait", pl.ExpiresAt.Local().Format("15:04"))
	return nil
}

// finishPendingLogin is the polling half of `login --wait`.
func finishPendingLogin(cmd *cobra.Command, ac *api.Client) (deviceToken, error) {
	pl, ok := loadPendingLogin()
	if !ok {
		return deviceToken{}, usageErr{errors.New("no sign-in is pending for this profile (or it expired); start one with 'miosa login --no-wait'")}
	}
	if pl.APIURL != "" {
		ac.BaseURL = pl.APIURL
	}
	if !isJSON() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Waiting for approval of code %s (until %s)...\n", pl.UserCode, pl.ExpiresAt.Local().Format("15:04"))
	}
	tok, err := pollDevice(cmd, ac, pl.DeviceCode, pl.Interval, pl.ExpiresAt)
	if err == nil {
		clearPendingLogin()
	}
	return tok, err
}
