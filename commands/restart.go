package commands

import (
	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(sandboxVerb(Op{
		Use: "restart [sandbox]", Short: "Restart a sandbox in place, keeping its disk",
		Long: `Power-cycle a running sandbox under the same id: it stops keeping its disk, then
boots that disk again. Files, packages and enabled services survive; running
processes do not. Use it when a sandbox stops answering. --wait blocks, with
progress, until it accepts commands.

  miosa restart my-box --wait`,
		Method: "POST", Path: "/sandboxes/{0}/restart", Detail: stateRows, Done: "Restarted {0}",
		Flags:   []Flag{{Name: "wait", Type: "bool", Usage: "Wait until it is ready for commands", In: "local"}},
		After:   waitAfterCreate,
		OnError: explainRestartRefusal,
	}))
}

// explainRestartRefusal says what to do next for each refusal the restart
// route names; the sandbox is never lost in any of them.
func explainRestartRefusal(_ *cobra.Command, _ *api.Client, args []string, err error) error {
	box := "<sandbox>"
	if len(args) > 0 && args[0] != "" {
		box = args[0]
	}
	switch {
	case api.IsCode(err, "RESTART_NOT_POWER_CYCLED"):
		return withHints(err, "the sandbox is running again from its saved memory, so its processes were not restarted; to start them fresh, run 'miosa stop "+box+"' then 'miosa resume "+box+"'")
	case api.IsCode(err, "RESTART_BOOT_FAILED"):
		return withHints(err, "the sandbox stopped and kept its disk; start it with 'miosa resume "+box+" --wait'")
	case api.IsCode(err, "RESTART_STOP_IN_PROGRESS"):
		return withHints(err, "it is still stopping; run 'miosa resume "+box+" --wait' in a moment")
	case api.IsCode(err, "RESTART_UNAVAILABLE"):
		return withHints(err, "run 'miosa stop "+box+"' then 'miosa resume "+box+"'")
	case api.IsCode(err, "SANDBOX_NOT_RUNNING"):
		return withHints(err, "only a running sandbox restarts; start it with 'miosa resume "+box+"'")
	}
	return err
}
