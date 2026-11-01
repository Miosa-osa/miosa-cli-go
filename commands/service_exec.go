package commands

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// serviceExecMaxTimeout is the longest a command may run in a replica (the
// host exec's ceiling); serviceExecDefaultTimeout is what the API uses when
// --timeout is not given.
const (
	serviceExecMaxTimeout     = 300
	serviceExecDefaultTimeout = 60
)

// serviceExecResult is the API's answer to POST .../services/:sid/exec.
type serviceExecResult struct {
	ReplicaID       string `json:"replica_id"`
	DeploymentID    string `json:"deployment_id"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	DurationMS      int64  `json:"duration_ms"`
}

func serviceExecCmd() *cobra.Command {
	var env, replica string
	cmd := &cobra.Command{
		Use:   "exec <project> <service> -- <cmd> [args...]",
		Short: "Run a one-off command in a running replica of a service",
		Long: `Run a command in one running replica of the service, the way its start command
runs (the image's user and working directory, the service's variables), and
print its output. miosa exits with the command's exit code, so a migration or a
seed drops straight into a script.

The -- separator is required before the command. Several words run as they are,
each one argument; a single word runs in the shell, so quote it to use pipes,
&& or variables:

  miosa service exec shop api -- pnpm db:migrate
  miosa service exec shop api -- 'pnpm db:migrate && pnpm db:seed'

--replica runs it in that replica (an id from 'miosa service get'); otherwise a
healthy replica of the active release is used. --timeout bounds the command in
seconds, 1-300 (default 60). A stopped or sleeping service has no replica to run
in.

Every exec is recorded in the organization's audit log (who ran it, in which
service, and a hash of the command; never the output). Output that contains one
of the service's variable values shows *** in its place.`,
		Example: `  miosa service exec shop api -- pnpm db:migrate
  miosa service exec shop api --timeout 300 -- python manage.py migrate
  miosa service exec shop api -- 'ls -la /app | head'
  miosa service exec shop api --replica 6f1c... -- cat /tmp/debug.log`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash != 2 || len(args) < 3 {
				return usagef("use: miosa service exec <project> <service> -- <cmd> [args...]")
			}
			timeout := serviceExecDefaultTimeout
			if timeoutGiven(cmd) {
				timeout = globalFlags.Timeout
				if timeout < 1 || timeout > serviceExecMaxTimeout {
					return usagef("--timeout for service exec is 1 to %d seconds", serviceExecMaxTimeout)
				}
			}

			// The request waits for the command: give it the command's time and the
			// host's margin on top.
			c, _, err := buildClientTimeout(time.Duration(timeout+60) * time.Second)
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}

			argv := args[2:]
			body := map[string]any{"timeout": timeout}
			if len(argv) == 1 {
				body["command"] = argv[0]
			} else {
				body["command"] = argv
			}
			if replica != "" {
				body["replica_id"] = replica
			}

			var res struct {
				Data serviceExecResult `json:"data"`
			}
			if err := c.API.Post(ctx, servicePath(projectID, svc.ID)+"/exec", body, &res); err != nil {
				return serviceExecError(err)
			}
			return printServiceExec(cmd, res.Data)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	fs.StringVar(&replica, "replica", "", "Run it in this replica (its id)")
	return cmd
}

// printServiceExec writes the command's output where a local command's would go,
// says on stderr when the replica kept only part of it, and turns a non-zero exit
// into the CLI's exit code.
func printServiceExec(cmd *cobra.Command, r serviceExecResult) error {
	if isJSON() {
		if err := printerFor(cmd).JSON(r); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), r.Stdout)
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), r.Stderr)
		if r.StdoutTruncated || r.StderrTruncated {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "miosa: the replica kept only the first part of the output")
		}
	}
	if r.ExitCode != 0 {
		return &ExitError{Code: r.ExitCode}
	}
	return nil
}

// serviceExecError says what to do about the refusals a person can act on.
func serviceExecError(err error) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case "SERVICE_NOT_RUNNING":
			return die(withHints(err, "miosa service start <project> <service>", "miosa service deploy <project> <service>"))
		case "EXEC_OUTCOME_UNKNOWN":
			return die(&api.Error{
				Status:  http.StatusGatewayTimeout,
				Code:    ae.Code,
				Message: "the replica did not say how the command ended; it may still be running",
			})
		}
	}
	return die(err)
}
