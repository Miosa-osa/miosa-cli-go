package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

// ciWait bounds how long 'ci connect' waits for the GitHub App install; tests shorten both.
var ciWait = struct{ every, within time.Duration }{3 * time.Second, 10 * time.Minute}

type ciInstallation struct {
	InstallationID      int64  `json:"installation_id"`
	AccountLogin        string `json:"account_login"`
	AccountType         string `json:"account_type"`
	RepositorySelection string `json:"repository_selection"`
	Status              string `json:"status"`
	ConnectedAt         string `json:"connected_at"`
}

type ciState struct {
	Available     bool             `json:"available"`
	Label         string           `json:"label"`
	Installations []ciInstallation `json:"installations"`
	Steps         struct {
		AppInstalled        bool `json:"app_installed"`
		RepositoriesGranted bool `json:"repositories_granted"`
		LabelUsed           bool `json:"label_used"`
		JobPassed           bool `json:"job_passed"`
	} `json:"steps"`
}

type ciJob struct {
	ID              string `json:"id"`
	Repository      string `json:"repository"`
	WorkflowName    string `json:"workflow_name"`
	JobName         string `json:"job_name"`
	Status          string `json:"status"`
	Conclusion      string `json:"conclusion"`
	StartedAt       string `json:"started_at"`
	DurationSeconds *int   `json:"duration_seconds"`
	FailureReason   string `json:"failure_reason"`
}

func newCICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Run GitHub Actions jobs on MIOSA machines",
		Long: `Run GitHub Actions jobs on MIOSA machines. Connect the MIOSA GitHub App, then
change runs-on: ubuntu-latest to runs-on: miosa in a workflow and push: each
job runs in a fresh sandbox, billed per second, destroyed when the job ends.

  miosa ci connect
  miosa ci status
  miosa ci jobs`,
	}
	cmd.AddCommand(newCIConnectCmd(), newCIStatusCmd(), newCIJobsCmd(), newCIDisconnectCmd())
	return cmd
}

// ciError turns the CI error codes into what to do about them.
func ciError(err error) error {
	switch {
	case api.IsCode(err, "CI_NOT_AVAILABLE"):
		return errors.New("CI on MIOSA is not available on this platform yet")
	case api.IsCode(err, "CI_INSTALLATION_NOT_FOUND"):
		return errors.New("no such installation in this organization; 'miosa ci status' lists the connected ones")
	case api.IsCode(err, "CI_INSTALLATION_OWNED_ELSEWHERE"):
		return errors.New("that GitHub installation is already connected to another organization")
	case api.IsCode(err, "CI_STATE_INVALID"):
		return errors.New("the install link expired or is not valid; run 'miosa ci connect' again")
	}
	return err
}

func getCI(ctx context.Context, c *client.Client) (ciState, error) {
	var resp struct {
		Data ciState `json:"data"`
	}
	err := c.API.Get(ctx, "/ci", nil, &resp)
	return resp.Data, err
}

func newCIConnectCmd() *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect a GitHub account to run its Actions jobs on MIOSA",
		Long: `Print the link that installs the MIOSA GitHub App, open it in the browser, and
wait until GitHub reports the installation (up to 10 minutes).

  miosa ci connect
  miosa ci connect --no-wait       # print the link and return`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var intent struct {
				Data struct {
					InstallURL string `json:"install_url"`
					ExpiresAt  string `json:"expires_at"`
				} `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), "/ci/connect-intents", map[string]any{}, &intent); err != nil {
				return die(ciError(err))
			}
			link := intent.Data.InstallURL
			if link == "" {
				return die(errors.New("the server returned no install link"))
			}
			p := printerFor(cmd)
			opened := !noWait && openBrowser(link) == nil
			if !isJSON() {
				if opened {
					p.Line("Opened the GitHub App install page in your browser.")
				}
				p.Line("Install link: %s", link)
			}
			if noWait {
				if isJSON() {
					return p.JSON(map[string]any{"install_url": link, "expires_at": intent.Data.ExpiresAt, "connected": false})
				}
				return nil
			}
			if !isJSON() {
				p.Line("Waiting for the installation (Ctrl-C stops waiting)...")
			}
			st, connected, err := waitForCI(cmd, c)
			if err != nil {
				return die(ciError(err))
			}
			if isJSON() {
				return p.JSON(map[string]any{"install_url": link, "connected": connected, "installations": st.Installations})
			}
			if !connected {
				p.Line("Not connected yet. Finish the install on GitHub, then check 'miosa ci status'.")
				return nil
			}
			for _, in := range st.Installations {
				p.Success("Connected GitHub account %s", in.AccountLogin)
			}
			p.Line("Next: change runs-on: ubuntu-latest to runs-on: %s in a workflow and push.", orDefault(st.Label, "miosa"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Print the install link and return without waiting")
	return cmd
}

// waitForCI polls until an installation appears, the wait ends, or Ctrl-C.
func waitForCI(cmd *cobra.Command, c *client.Client) (ciState, bool, error) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := time.Now().Add(ciWait.within)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ciState{}, false, nil
		case <-time.After(ciWait.every):
		}
		st, err := getCI(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				return ciState{}, false, nil
			}
			if api.IsCode(err, "CI_NOT_AVAILABLE") {
				return ciState{}, false, err
			}
			continue
		}
		if len(st.Installations) > 0 {
			return st, true, nil
		}
	}
	return ciState{}, false, nil
}

func newCIStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the CI on MIOSA setup steps and connected GitHub accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var raw struct {
				Data json.RawMessage `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/ci", nil, &raw); err != nil {
				return die(ciError(err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(raw.Data)
			}
			var st ciState
			if err := json.Unmarshal(raw.Data, &st); err != nil {
				return die(fmt.Errorf("reading the CI state: %w", err))
			}
			if !st.Available {
				p.Line("CI on MIOSA is not available on this platform yet.")
				return nil
			}
			label := orDefault(st.Label, "miosa")
			p.Table([]string{"STEP", "DONE"}, [][]string{
				{"GitHub App installed", yesNo(st.Steps.AppInstalled)},
				{"Repositories granted", yesNo(st.Steps.RepositoriesGranted)},
				{"runs-on: " + label + " used in a workflow", yesNo(st.Steps.LabelUsed)},
				{"A job passed", yesNo(st.Steps.JobPassed)},
			})
			if len(st.Installations) == 0 {
				p.Line("\nNo GitHub account connected. Run: miosa ci connect")
			} else {
				rows := make([][]string, 0, len(st.Installations))
				for _, in := range st.Installations {
					rows = append(rows, []string{strconv.FormatInt(in.InstallationID, 10), in.AccountLogin, in.AccountType, in.RepositorySelection, in.Status})
				}
				p.Line("")
				p.Table([]string{"INSTALLATION", "ACCOUNT", "TYPE", "REPOSITORIES", "STATUS"}, rows)
			}
			p.Line("\nJobs get none of the organization's MIOSA environment, secret files or keys.")
			p.Line("Use GitHub Actions secrets for job secrets.")
			return nil
		},
	}
}

func newCIJobsCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "List recent GitHub Actions jobs that ran on MIOSA",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 {
				return usagef("--limit must be at least 1")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var resp struct {
				Data []ciJob `json:"data"`
			}
			q := url.Values{"limit": {strconv.Itoa(limit)}}
			if err := c.API.Get(cmd.Context(), "/ci/jobs", q, &resp); err != nil {
				return die(ciError(err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(resp.Data)
			}
			if len(resp.Data) == 0 {
				p.Line("No jobs yet. Use runs-on: miosa in a workflow and push.")
				return nil
			}
			rows := make([][]string, 0, len(resp.Data))
			for _, j := range resp.Data {
				name := j.JobName
				if j.WorkflowName != "" {
					name = j.WorkflowName + " / " + j.JobName
				}
				dur := "-"
				if j.DurationSeconds != nil {
					dur = (time.Duration(*j.DurationSeconds) * time.Second).String()
				}
				rows = append(rows, []string{j.Repository, name, j.Status, orDefault(j.Conclusion, "-"), dur, orDefault(formatAge(j.StartedAt), "-")})
			}
			p.Table([]string{"REPOSITORY", "WORKFLOW / JOB", "STATUS", "CONCLUSION", "DURATION", "STARTED"}, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of jobs to show")
	return cmd
}

func newCIDisconnectCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "disconnect <installation_id>",
		Short: "Disconnect a GitHub installation from this organization",
		Long: `Disconnect a GitHub App installation. Jobs already running finish; new jobs
with runs-on: miosa from that account no longer run on MIOSA. The installation
ids are in 'miosa ci status'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if _, err := strconv.ParseInt(id, 10, 64); err != nil {
				return usagef("the installation id is a number (see 'miosa ci status'), got %q", id)
			}
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Disconnect GitHub installation %s? New jobs from it stop running on MIOSA.", id)); err != nil {
					return err
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if err := c.API.Delete(cmd.Context(), "/ci/installations/"+id, nil, nil); err != nil {
				return die(ciError(err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]string{"status": "disconnected", "installation_id": id})
			}
			p.Success("Disconnected installation %s", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	return cmd
}
