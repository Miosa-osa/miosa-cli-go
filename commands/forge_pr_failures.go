package commands

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/spf13/cobra"
)

// failureLogLines is how much of a failing step's output the triage shows.
const failureLogLines = 20

// Origins of a failing check, compared with the base branch's latest run of
// the same workflow.
const (
	failureNew         = "new"
	failureAlreadyMain = "already_on_main"
	failureUnknown     = "unknown"
)

type automationStep struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code"`
}

type automationRun struct {
	ID         string           `json:"id"`
	Workflow   string           `json:"workflow"`
	WorkflowID string           `json:"workflow_id"`
	CommitSHA  string           `json:"commit_sha"`
	Ref        string           `json:"ref"`
	Status     string           `json:"status"`
	Steps      []automationStep `json:"steps"`
}

func (r automationRun) workflowKey() string {
	if r.WorkflowID != "" {
		return r.WorkflowID
	}
	return r.Workflow
}

// prFailure is one failing check of a pull request's head, as --failures
// prints it and --json returns it.
type prFailure struct {
	Workflow    string   `json:"workflow"`
	RunID       string   `json:"run_id"`
	Step        string   `json:"step,omitempty"`
	StepID      string   `json:"step_id,omitempty"`
	ExitCode    *int     `json:"exit_code,omitempty"`
	Origin      string   `json:"origin"`
	BaseRunID   string   `json:"base_run_id,omitempty"`
	BaseCommit  string   `json:"base_commit_sha,omitempty"`
	Log         []string `json:"log"`
	LogTruncate bool     `json:"log_truncated,omitempty"`
}

type prFailureReport struct {
	Number     string      `json:"number"`
	HeadSHA    string      `json:"head_sha"`
	BaseBranch string      `json:"base_branch"`
	Runs       int         `json:"runs_on_head"`
	Pending    []string    `json:"pending"`
	Failures   []prFailure `json:"failures"`
}

// prStatusFailures is the Before hook of `miosa forge pr status`: with
// --failures it replaces the snapshot with a triage of the failing checks.
func prStatusFailures(cmd *cobra.Command, c *api.Client) error {
	if on, _ := cmd.Flags().GetBool("failures"); !on {
		return nil
	}
	args := argsOf(cmd)
	if len(args) < 2 {
		return usagef("usage: miosa forge pr status <repo> <number> --failures")
	}
	report, err := triagePRFailures(cmd, c, args[0], args[1])
	if err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	if isJSON() {
		if err := p.JSON(report); err != nil {
			return err
		}
		return errRequestHandled
	}
	renderPRFailures(cmd, report)
	return errRequestHandled
}

func triagePRFailures(cmd *cobra.Command, c *api.Client, repo, number string) (*prFailureReport, error) {
	base := "/forge/repositories/" + url.PathEscape(repo)
	var pr struct {
		Data struct {
			HeadSHA    string `json:"head_sha"`
			BaseBranch string `json:"base_branch"`
		} `json:"data"`
	}
	if err := c.Get(cmd.Context(), base+"/pull-requests/"+url.PathEscape(number), nil, &pr); err != nil {
		return nil, fmt.Errorf("pull request #%s: %w", number, err)
	}
	if pr.Data.HeadSHA == "" {
		return nil, errors.New("the pull request has no head commit yet")
	}
	var runs struct {
		Data []automationRun `json:"data"`
	}
	if err := c.Get(cmd.Context(), base+"/automation-runs", url.Values{"limit": {"100"}}, &runs); err != nil {
		return nil, fmt.Errorf("automation runs: %w", err)
	}

	report := &prFailureReport{Number: number, HeadSHA: pr.Data.HeadSHA, BaseBranch: pr.Data.BaseBranch, Pending: []string{}, Failures: []prFailure{}}
	head, onBase := splitRuns(runs.Data, pr.Data.HeadSHA, pr.Data.BaseBranch)
	report.Runs = len(head)
	for _, run := range head {
		switch run.Status {
		case "failed":
		case "queued", "running":
			report.Pending = append(report.Pending, run.Workflow)
			continue
		default:
			continue
		}
		f := prFailure{Workflow: run.Workflow, RunID: run.ID, Origin: failureUnknown, Log: []string{}}
		if mainRun, ok := onBase[run.workflowKey()]; ok {
			f.BaseRunID, f.BaseCommit = mainRun.ID, mainRun.CommitSHA
			f.Origin = failureNew
			if mainRun.Status == "failed" {
				f.Origin = failureAlreadyMain
			}
		}
		if step, ok := failingStep(run); ok {
			f.Step, f.StepID, f.ExitCode = step.Name, step.ID, step.ExitCode
			var receipt struct {
				Data struct {
					Stdout    string `json:"stdout"`
					Stderr    string `json:"stderr"`
					Truncated bool   `json:"truncated"`
				} `json:"data"`
			}
			path := base + "/automation-runs/" + url.PathEscape(run.ID) + "/steps/" + url.PathEscape(step.ID) + "/receipt"
			if err := c.Get(cmd.Context(), path, nil, &receipt); err != nil {
				return nil, fmt.Errorf("log of %s step %q: %w", run.Workflow, step.Name, err)
			}
			f.Log = tailLines(receipt.Data.Stdout+"\n"+receipt.Data.Stderr, failureLogLines)
			f.LogTruncate = receipt.Data.Truncated
		}
		report.Failures = append(report.Failures, f)
	}
	return report, nil
}

// splitRuns keeps, per workflow, the newest run on the head commit and the
// newest finished run on the base branch (runs arrive newest first).
func splitRuns(runs []automationRun, headSHA, baseBranch string) ([]automationRun, map[string]automationRun) {
	var head []automationRun
	seen := map[string]bool{}
	onBase := map[string]automationRun{}
	for _, run := range runs {
		key := run.workflowKey()
		switch {
		case run.CommitSHA == headSHA:
			if !seen[key] {
				seen[key] = true
				head = append(head, run)
			}
		case baseBranch != "" && strings.TrimPrefix(run.Ref, "refs/heads/") == baseBranch:
			if _, ok := onBase[key]; !ok && (run.Status == "passed" || run.Status == "failed") {
				onBase[key] = run
			}
		}
	}
	return head, onBase
}

func failingStep(run automationRun) (automationStep, bool) {
	for _, s := range run.Steps {
		if s.Status == "failed" {
			return s, true
		}
	}
	return automationStep{}, false
}

func tailLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return kept
}

func renderPRFailures(cmd *cobra.Command, r *prFailureReport) {
	p := printerFor(cmd)
	head := formatValue(r.HeadSHA, "sha")
	switch {
	case r.Runs == 0:
		p.Line("Pull request #%s: no automation runs on its head %s yet", r.Number, head)
		return
	case len(r.Failures) == 0:
		p.Success("Pull request #%s: no failing checks on its head %s", r.Number, head)
	default:
		p.Line("Pull request #%s: %d failing check(s) on its head %s, compared with %s", r.Number, len(r.Failures), head, orDefault(r.BaseBranch, "the base branch"))
	}
	for _, f := range r.Failures {
		label := map[string]string{
			failureNew:         "new: passed on " + r.BaseBranch + " at " + formatValue(f.BaseCommit, "sha"),
			failureAlreadyMain: "already on " + r.BaseBranch + ": failed at " + formatValue(f.BaseCommit, "sha") + " too",
			failureUnknown:     "unknown: no finished run of this workflow on " + orDefault(r.BaseBranch, "the base branch"),
		}[f.Origin]
		p.Line("")
		p.Line("FAIL %s [%s]", f.Workflow, label)
		step := orDefault(f.Step, "(no failed step recorded)")
		if f.ExitCode != nil {
			step += fmt.Sprintf(", exit %d", *f.ExitCode)
		}
		p.Line("  step: %s", step)
		p.Line("  run:  %s", f.RunID)
		for _, l := range f.Log {
			p.Line("  | %s", l)
		}
		if f.LogTruncate {
			p.Line("  (the recorded output was cut at its size limit)")
		}
	}
	if len(r.Pending) > 0 {
		p.Line("")
		p.Line("Still running: %s", strings.Join(r.Pending, ", "))
	}
}
