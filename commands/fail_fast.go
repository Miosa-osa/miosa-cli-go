package commands

import (
	"net/http"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// failFast is --fail-fast on create, run, fork and resume: start only if a warm
// machine is ready now, else the API answers 503 NO_READY_MACHINE within about a
// second and starts, bills and counts nothing. It travels as X-Miosa-Fail-Fast
// on exactly the request that starts the machine (failFastPath), never on the
// calls that follow it (exec, files, wait).
var failFast bool

const failFastHeader = "X-Miosa-Fail-Fast"

const failFastUsage = "Start only if a machine is ready now; otherwise fail at once and start, bill and count nothing"

var failFastPath = regexp.MustCompile(`/sandboxes(/run)?$|/sandboxes/[^/]+/(fork|resume)$`)

func failFastRequest(req *http.Request) bool {
	return failFast && req.Method == http.MethodPost && failFastPath.MatchString(req.URL.Path)
}

// failFastFlag is the --fail-fast of a declarative (Op) command.
var failFastFlag = Flag{Name: "fail-fast", Type: "bool", Usage: failFastUsage, In: "local"}

// setFailFast is the Before hook that turns --fail-fast on for an Op command.
func setFailFast(cmd *cobra.Command, _ *api.Client) error {
	failFast, _ = cmd.Flags().GetBool("fail-fast")
	return nil
}
