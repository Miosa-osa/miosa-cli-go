package commands

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

func newProxyCmd() *cobra.Command {
	var bind string
	cmd := &cobra.Command{
		Use:     "proxy [name|id] <local>:<remote> [<local>:<remote> ...]",
		Aliases: []string{"forward"},
		Short:   "Forward local ports to sandbox ports",
		Long: `Forward one or more local TCP ports to ports inside a sandbox, over the control
plane's WebSocket tunnel. Any TCP service works (a database, a dev server, a
debugger), not only HTTP; use 'miosa preview' to publish an HTTP port at a URL
instead.

Each mapping is <local-port>:<remote-port>. The command blocks until
interrupted with Ctrl-C. It listens on 127.0.0.1 unless --bind says otherwise.

A key needs the sandboxes:exec scope, because a tunnel reaches every service in
the sandbox. The sandbox must be running; a paused one is not woken.

  miosa proxy my-box 8080:80
  miosa proxy my-box 5432:5432 6379:6379
  miosa proxy 8080:80          # the current sandbox`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProxy(cmd, args, bind) },
	}
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "Local address to listen on")
	return cmd
}

func runProxy(cmd *cobra.Command, args []string, bind string) error {
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ref, pairs, err := parseProxyArgs(args)
	if err != nil {
		return usagef("%v", err)
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}
	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return die(err)
	}

	p := printerFor(cmd)
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, len(pairs))
	for _, pair := range pairs {
		local, remote := pair[0], pair[1]
		p.Line("Forwarding %s:%d to %s port %d", bind, local, ref, remote)
		go func() {
			errs <- c.Proxy.Forward(ctx, id, local, remote, client.ForwardOptions{
				Bind: bind,
				Logf: func(format string, a ...any) { p.Line(format, a...) },
			})
		}()
	}
	p.Line("Press Ctrl-C to stop.")
	select {
	case err := <-errs:
		stop()
		if err != nil {
			return die(err)
		}
	case <-ctx.Done():
	}
	return nil
}

func parseProxyArgs(args []string) (nameOrID string, pairs [][2]int, err error) {
	// If first arg looks like a port mapping (contains ":"), no sandbox name.
	start := 0
	if len(args) > 0 && !strings.Contains(args[0], ":") {
		nameOrID = args[0]
		start = 1
	}

	for _, arg := range args[start:] {
		parts := strings.SplitN(arg, ":", 2)
		if len(parts) != 2 {
			return "", nil, fmt.Errorf("invalid port mapping %q: expected <local>:<remote>", arg)
		}
		local, err := strconv.Atoi(parts[0])
		if err != nil || local < 1 || local > 65535 {
			return "", nil, fmt.Errorf("invalid local port %q", parts[0])
		}
		remote, err := strconv.Atoi(parts[1])
		if err != nil || remote < 1 || remote > 65535 {
			return "", nil, fmt.Errorf("invalid remote port %q", parts[1])
		}
		pairs = append(pairs, [2]int{local, remote})
	}

	if len(pairs) == 0 {
		return "", nil, fmt.Errorf("at least one <local>:<remote> port mapping required")
	}
	return nameOrID, pairs, nil
}
