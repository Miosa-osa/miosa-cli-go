package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// sseTicket obtains a one-hour ticket for streaming endpoints, which cannot
// take an Authorization header from a browser and so use ?ticket=.
func sseTicket(ctx context.Context, c *api.Client) (string, error) {
	var out struct {
		Ticket string `json:"ticket"`
	}
	if err := c.Post(ctx, "/sse/ticket", map[string]any{}, &out); err != nil {
		return "", err
	}
	if out.Ticket == "" {
		return "", errors.New("the server returned no stream ticket")
	}
	return out.Ticket, nil
}

// followStream reads a server-sent-event endpoint until the user interrupts
// it or the server ends the stream. A dropped connection is retried a few
// times with a fresh ticket.
func followStream(cmd *cobra.Command, c *api.Client, path string, q url.Values, fn func(api.Event) error) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		ticket, err := sseTicket(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return die(err)
		}
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("ticket", ticket)
		err = c.Events(ctx, path, qq, fn)
		switch {
		case err == nil:
			return nil
		case ctx.Err() != nil:
			return nil // interrupted
		}
		var te *api.TransportError
		if errors.As(err, &te) || errors.Is(err, io.ErrUnexpectedEOF) {
			lastErr = err
			continue
		}
		return die(err)
	}
	return die(lastErr)
}

// followLogLines streams a log endpoint: the text of each line, or with --json
// one {"event","data"} object per line. A build stream ends with a "done" (or
// "cancelled") event, printed as a closing line. A resource with nothing
// running to stream from says so in words.
func followLogLines(cmd *cobra.Command, c *api.Client, path string, q url.Values) error {
	p := printerFor(cmd)
	err := followStream(cmd, c, path, q, func(ev api.Event) error {
		if isJSON() {
			return p.JSONLine(map[string]any{"event": ev.Name, "data": rawOrString(ev.Data)})
		}
		switch ev.Name {
		case "done":
			var v struct {
				Status string `json:"status"`
			}
			_ = jsonUnmarshal(ev.Data, &v)
			fmt.Fprintf(p.Writer(), "build finished: %s\n", orDash(v.Status))
		case "cancelled":
			fmt.Fprintln(p.Writer(), "build canceled")
		default:
			fmt.Fprintln(p.Writer(), logLineOf(ev.Data))
		}
		return nil
	})
	var ae *api.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case "DEPLOYMENT_NOT_RUNNING":
			ae.Message = "no live log to follow: the API has no running instance of this deployment to stream from"
		case "DATABASE_NOT_RUNNING", "NO_VM_IP":
			ae.Message = "no live log to follow: the database is not running; start it, then follow again"
		}
	}
	return err
}

// followRoute builds an Op.Follow that streams format with the resource's id
// from idPath in the snapshot response: stream routes take the id, not a name.
func followRoute(idPath, format string) func(*cobra.Command, *api.Client, any) error {
	return func(cmd *cobra.Command, c *api.Client, resp any) error {
		id, _ := lookupPath(resp, idPath).(string)
		if id == "" {
			return die(errors.New("the server did not return the id to follow; give the id instead of the name"))
		}
		return followLogLines(cmd, c, fmt.Sprintf(format, url.PathEscape(id)), nil)
	}
}

// deployWatchEvery is how often a followed build checks its deployment's
// state; tests shorten it.
var deployWatchEvery = 3 * time.Second

func deployBuilding(state string) bool { return state == "pending" || state == "building" }

// followDeployment follows a deployment. While it builds, the build's stream
// is followed, and the deployment's state is watched so the follow moves on
// when the build ends even if the stream never says so (a build that finished
// before the stream opened). A running app is then followed through its
// runtime stream; a deployment that ended otherwise is reported.
func followDeployment(cmd *cobra.Command, c *api.Client, resp any) error {
	id, _ := lookupPath(resp, "data.deployment_id").(string)
	if id == "" {
		return die(errors.New("the server did not return the deployment id to follow; give the id instead of the name"))
	}
	base := "/deployments/" + url.PathEscape(id)
	state, _ := lookupPath(resp, "data.state").(string)
	if deployBuilding(state) {
		parent := cmd.Context()
		ctx, cancel := context.WithCancel(parent)
		every := deployWatchEvery
		go func() {
			defer cancel()
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(every):
				}
				var d struct {
					Data struct {
						State string `json:"state"`
					} `json:"data"`
				}
				if c.Get(ctx, base, nil, &d) == nil && !deployBuilding(d.Data.State) {
					return
				}
			}
		}()
		cmd.SetContext(ctx)
		err := followLogLines(cmd, c, base+"/logs", url.Values{"follow": {"1"}})
		cmd.SetContext(parent)
		cancel()
		if err != nil {
			return err
		}
		if parent.Err() != nil {
			return nil // Ctrl-C
		}
		var d struct {
			Data struct {
				State string `json:"state"`
			} `json:"data"`
		}
		if err := c.Get(parent, base, nil, &d); err != nil {
			return die(err)
		}
		if d.Data.State != "running" {
			return die(fmt.Errorf("the deployment is %s, so there is no app output to follow; 'miosa deploy get %s' shows why", orDash(d.Data.State), id))
		}
		if !isJSON() {
			fmt.Fprintln(cmd.ErrOrStderr(), "the app is running; following its output")
		}
	}
	return followLogLines(cmd, c, base+"/runtime-logs/stream", nil)
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// rawOrString returns data as parsed JSON when it is JSON, else as a string.
func rawOrString(data string) any {
	var v any
	if json.Unmarshal([]byte(data), &v) == nil {
		return v
	}
	return data
}

func sortRows(rows [][]string) {
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
}
