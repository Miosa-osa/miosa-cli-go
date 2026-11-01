package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// servicePreDeployLog is one page of a release's pre-deploy log.
type servicePreDeployLog struct {
	Deployment serviceRelease `json:"deployment"`
	Text       string         `json:"text"`
	Offset     int64          `json:"offset"`
	Complete   bool           `json:"complete"`
}

// servicePreDeployLogs reads what a release's pre-deploy command printed (the
// newest release with one, or the release named), and with follow keeps reading
// from the offset the server reports until the command has run.
func servicePreDeployLogs(ctx context.Context, cmd *cobra.Command, c *api.Client, projectID string, svc *projectServiceRow, releaseRef string, tail int, follow bool) error {
	p := printerFor(cmd)
	target := "latest"
	if releaseRef != "" {
		release, err := latestOrNamedRelease(ctx, c, projectID, svc.ID, releaseRef)
		if err != nil {
			return err
		}
		target = release.ID
	}
	path := servicePath(projectID, svc.ID) + "/deployments/" + url.PathEscape(target) + "/pre-deploy/logs"
	after := int64(-1)
	shownState := ""

	for {
		q := url.Values{}
		if after >= 0 {
			q.Set("after", strconv.FormatInt(after, 10))
		} else if tail > 0 {
			q.Set("tail", strconv.Itoa(tail))
		}

		var page struct {
			Data servicePreDeployLog `json:"data"`
		}
		if err := c.Get(ctx, path, q, &page); err != nil {
			return servicePreDeployLogError(err, target)
		}

		if isJSON() {
			if err := p.JSONLine(page.Data); err != nil {
				return err
			}
		} else {
			// The log is the output and goes to stdout; the release it belongs to goes
			// to stderr, so `... --pre-deploy | grep` reads only the log.
			if d := page.Data.Deployment; d.State != shownState {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Release %d %s %s\n", d.Number, d.State, d.ID)
				shownState = d.State
			}
			if page.Data.Text != "" {
				_, _ = fmt.Fprint(p.Writer(), page.Data.Text)
			}
		}

		if !follow || page.Data.Complete {
			return nil
		}
		after = page.Data.Offset

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// servicePreDeployLogError names what a 404 means here.
func servicePreDeployLogError(err error, target string) error {
	var ae *api.Error
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		message := "no release of this service has a pre-deploy command"
		if target != "latest" {
			message = "no such release for this service"
		}
		return die(&api.Error{Status: http.StatusNotFound, Code: "PRE_DEPLOY_LOG_NOT_FOUND", Message: message})
	}
	return die(err)
}
