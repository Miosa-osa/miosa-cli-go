package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// streamHostEvents follows an OpenComputers event stream. With --json each
// event is one JSON object on its own line; otherwise it prints "<name>  <data>".
func streamHostEvents(cmd *cobra.Command, path string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	return followStream(cmd, c.API, path, nil, func(ev api.Event) error {
		if isJSON() {
			return p.JSONLine(map[string]any{"event": ev.Name, "id": ev.ID, "data": rawOrString(ev.Data)})
		}
		name := ev.Name
		if name == "" {
			name = "message"
		}
		fmt.Fprintf(p.Writer(), "%s  %s\n", name, ev.Data)
		return nil
	})
}

// hostContainerStreamCmds are the streaming reads for a container that the
// declarative layer cannot express: both endpoints hold the connection open
// and push one event per log line or stats sample.
func hostContainerStreamCmds(host Arg) []*cobra.Command {
	container := Arg{Name: "container"}
	return []*cobra.Command{
		newContainerStreamCmd("logs", "Stream a container's log", "logs", host, container),
		newContainerStreamCmd("stats", "Stream a container's resource usage", "stats", host, container),
	}
}

func newContainerStreamCmd(use, short, suffix string, host, container Arg) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <host> <container>",
		Short: short,
		Long: `Stream until the connection ends or Ctrl-C is pressed.
With --json each event is one JSON object per line.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			path := "/opencomputers/hosts/" + args[0] + "/containers/" + args[1] + "/" + suffix
			return followStream(cmd, c.API, path, nil, func(ev api.Event) error {
				if isJSON() {
					return p.JSONLine(map[string]any{"event": ev.Name, "data": rawOrString(ev.Data)})
				}
				fmt.Fprintln(p.Writer(), logLineOf(ev.Data))
				return nil
			})
		},
	}
}
