package commands

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/spf13/cobra"
)

// Forge pull request comments are flat (no threads, no resolved state), so
// --unresolved decides what still needs the author: every comment a person or
// service account wrote, without Forge's own system comments (preview and
// other sticky bot comments), plus the reviews on the current head that ask
// for changes or comment. Approvals, dismissed reviews and reviews of an
// older head count as resolved.

type prComment struct {
	ID                     string `json:"id"`
	Kind                   string `json:"kind"`
	AuthorUserID           string `json:"author_user_id,omitempty"`
	AuthorServiceAccountID string `json:"author_service_account_id,omitempty"`
	Body                   string `json:"body"`
	CreatedAt              string `json:"created_at"`
}

type prReview struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Body     string `json:"body"`
	HeadSHA  string `json:"head_sha"`
	State    string `json:"state"`
	Reviewer *struct {
		Name string `json:"name"`
	} `json:"reviewer"`
}

type prUnresolvedReport struct {
	Number   string      `json:"number"`
	HeadSHA  string      `json:"head_sha"`
	Comments []prComment `json:"comments"`
	Reviews  []prReview  `json:"reviews"`
	Hidden   struct {
		System   int `json:"system_comments"`
		Resolved int `json:"resolved_reviews"`
	} `json:"hidden"`
}

// prCommentsUnresolved is the Before hook of `miosa forge pr comments`.
func prCommentsUnresolved(cmd *cobra.Command, c *api.Client) error {
	if on, _ := cmd.Flags().GetBool("unresolved"); !on {
		return nil
	}
	args := argsOf(cmd)
	if len(args) < 2 {
		return usagef("usage: miosa forge pr comments <repo> <number> --unresolved")
	}
	report, err := unresolvedPRComments(cmd, c, args[0], args[1])
	if err != nil {
		return die(err)
	}
	if isJSON() {
		if err := printerFor(cmd).JSON(report); err != nil {
			return err
		}
		return errRequestHandled
	}
	renderUnresolved(cmd, report)
	return errRequestHandled
}

func unresolvedPRComments(cmd *cobra.Command, c *api.Client, repo, number string) (*prUnresolvedReport, error) {
	base := "/forge/repositories/" + url.PathEscape(repo) + "/pull-requests/" + url.PathEscape(number)
	var snap struct {
		Data struct {
			HeadSHA string     `json:"head_sha"`
			Reviews []prReview `json:"reviews"`
		} `json:"data"`
	}
	if err := c.Get(cmd.Context(), base+"/collaboration", nil, &snap); err != nil {
		return nil, fmt.Errorf("pull request #%s: %w", number, err)
	}
	report := &prUnresolvedReport{Number: number, HeadSHA: snap.Data.HeadSHA, Comments: []prComment{}, Reviews: []prReview{}}
	for _, r := range snap.Data.Reviews {
		if r.State == "active" && r.HeadSHA == snap.Data.HeadSHA && r.Decision != "approved" {
			report.Reviews = append(report.Reviews, r)
		} else {
			report.Hidden.Resolved++
		}
	}

	after := ""
	for {
		q := url.Values{"limit": {"200"}}
		if after != "" {
			q.Set("after", after)
		}
		var page struct {
			Data       []prComment `json:"data"`
			NextCursor string      `json:"next_cursor"`
		}
		if err := c.Get(cmd.Context(), base+"/comments", q, &page); err != nil {
			return nil, fmt.Errorf("comments of #%s: %w", number, err)
		}
		for _, cm := range page.Data {
			if cm.Kind == "system" {
				report.Hidden.System++
				continue
			}
			report.Comments = append(report.Comments, cm)
		}
		if page.NextCursor == "" || page.NextCursor == after {
			break
		}
		after = page.NextCursor
	}
	return report, nil
}

func renderUnresolved(cmd *cobra.Command, r *prUnresolvedReport) {
	p := printerFor(cmd)
	if len(r.Comments) == 0 && len(r.Reviews) == 0 {
		p.Success("Pull request #%s: nothing unresolved on its head %s", r.Number, formatValue(r.HeadSHA, "sha"))
	} else {
		p.Line("Pull request #%s: %d review(s) and %d comment(s) unresolved on its head %s",
			r.Number, len(r.Reviews), len(r.Comments), formatValue(r.HeadSHA, "sha"))
	}
	for _, rv := range r.Reviews {
		who := "a reviewer"
		if rv.Reviewer != nil && rv.Reviewer.Name != "" {
			who = rv.Reviewer.Name
		}
		p.Line("")
		p.Line("REVIEW %s by %s", strings.ReplaceAll(rv.Decision, "_", " "), who)
		quoteBody(p, rv.Body)
	}
	for _, cm := range r.Comments {
		p.Line("")
		p.Line("COMMENT %s, %s", formatValue(cm.ID, "short"), formatValue(cm.CreatedAt, "age"))
		quoteBody(p, cm.Body)
	}
	if r.Hidden.System+r.Hidden.Resolved > 0 {
		p.Line("")
		p.Line("Hidden: %d system comment(s), %d resolved review(s)", r.Hidden.System, r.Hidden.Resolved)
	}
}

func quoteBody(p interface{ Line(string, ...any) }, body string) {
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		p.Line("  | %s", l)
	}
}
