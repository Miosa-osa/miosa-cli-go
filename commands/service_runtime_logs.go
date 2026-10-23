package commands

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A service's runtime log is what its process writes (stdout and stderr), per
// replica. The API serves the tail of each replica's log and no cursor, so
// following polls the tail and prints what is new on each replica.

// serviceLogPollEvery is how often a followed runtime log is read; tests
// shorten it.
var serviceLogPollEvery = 2 * time.Second

type serviceReplicaLog struct {
	ReplicaID string   `json:"replica_id"`
	Slot      *int     `json:"slot"`
	State     string   `json:"state"`
	Lines     []string `json:"lines"`
	Error     *string  `json:"error"`
}

type serviceRuntimeLog struct {
	Source   string              `json:"source"`
	Replicas []serviceReplicaLog `json:"replicas"`
}

func fetchServiceRuntimeLog(ctx context.Context, c *api.Client, projectID, serviceID, replica string, tail int) (serviceRuntimeLog, error) {
	q := url.Values{}
	if tail > 0 {
		q.Set("tail", strconv.Itoa(tail))
	}
	if replica != "" {
		q.Set("replica", replica)
	}
	var out struct {
		Data serviceRuntimeLog `json:"data"`
	}
	err := c.Get(ctx, servicePath(projectID, serviceID)+"/logs", q, &out)
	return out.Data, err
}

// printServiceRuntimeLogs prints a service's runtime log and, with follow,
// keeps printing new lines until Ctrl-C. The first read's error is returned as
// the API's, so a caller may fall back on an older server.
func printServiceRuntimeLogs(cmd *cobra.Command, c *api.Client, projectID, serviceID, replica string, tail int, follow bool) error {
	p := printerFor(cmd)
	ctx := cmd.Context()
	if follow {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	logs, err := fetchServiceRuntimeLog(ctx, c, projectID, serviceID, replica, tail)
	if err != nil {
		return err
	}
	if isJSON() && !follow {
		return p.JSON(logs)
	}
	many := len(logs.Replicas) > 1
	seen := map[string][]string{}
	emit := func(r serviceReplicaLog, lines []string) error {
		for _, line := range lines {
			if isJSON() {
				if err := p.JSONLine(map[string]any{"replica_id": r.ReplicaID, "slot": r.Slot, "line": line}); err != nil {
					return err
				}
				continue
			}
			if many {
				fmt.Fprintf(p.Writer(), "[%s] %s\n", replicaLabel(r), line)
			} else {
				fmt.Fprintln(p.Writer(), line)
			}
		}
		return nil
	}
	report := func(r serviceReplicaLog) {
		if r.Error != nil && *r.Error != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s (%s): %s\n", replicaLabel(r), orDash(r.State), *r.Error)
		}
	}
	errored := map[string]bool{}
	for _, r := range logs.Replicas {
		report(r)
		errored[r.ReplicaID] = r.Error != nil && *r.Error != ""
		if err := emit(r, r.Lines); err != nil {
			return err
		}
		seen[r.ReplicaID] = r.Lines
	}
	if len(logs.Replicas) == 0 && !isJSON() {
		fmt.Fprintln(cmd.ErrOrStderr(), "the service has no replicas running, so there is no runtime log yet")
	}
	if !follow {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(serviceLogPollEvery):
		}
		logs, err := fetchServiceRuntimeLog(ctx, c, projectID, serviceID, replica, tail)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return die(err)
		}
		many = many || len(logs.Replicas) > 1
		for _, r := range logs.Replicas {
			if r.Error != nil && *r.Error != "" {
				if !errored[r.ReplicaID] {
					report(r)
					errored[r.ReplicaID] = true
				}
				continue
			}
			errored[r.ReplicaID] = false
			if err := emit(r, newLogLines(seen[r.ReplicaID], r.Lines)); err != nil {
				return err
			}
			seen[r.ReplicaID] = r.Lines
		}
	}
}

// newLogLines returns the lines of cur that follow what prev already showed:
// the longest end of prev that cur starts with is the overlap. With no overlap
// (more lines arrived than the tail holds, or the log was rotated) all of cur
// is new.
func newLogLines(prev, cur []string) []string {
	for k := min(len(prev), len(cur)); k > 0; k-- {
		if equalLines(prev[len(prev)-k:], cur[:k]) {
			return cur[k:]
		}
	}
	return cur
}

func equalLines(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func replicaLabel(r serviceReplicaLog) string {
	if r.Slot != nil {
		return "replica " + strconv.Itoa(*r.Slot)
	}
	return "replica " + shortID(r.ReplicaID)
}
