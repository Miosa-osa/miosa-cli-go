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
	var reverse bool
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

--reverse turns it around: a port inside the sandbox reaches a port on your
machine (an ssh -R over the sandbox's SSH tunnel; needs the OpenSSH client).
The mapping is still <local>:<remote>: the sandbox's localhost:<remote>
connects to <local> on --bind (default 127.0.0.1) here.

A connection that cannot reach the sandbox (it is moving between hosts, or the
network blinked) is retried up to 5 times before it is dropped.

  miosa proxy my-box 8080:80
  miosa proxy my-box 5432:5432 6379:6379
  miosa proxy 8080:80          # the current sandbox
  miosa forward my-box --reverse 3000:8080   # the sandbox's :8080 reaches your :3000`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if reverse {
				return runReverseProxy(cmd, args, bind)
			}
			return runProxy(cmd, args, bind)
		},
	}
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "Local address to listen on (with --reverse: to connect to)")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Forward sandbox ports to ports on this machine instead")
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

// runReverseProxy makes ports inside the sandbox reach ports on this machine:
// one ssh session over the sandbox's SSH tunnel with an -R per mapping, held
// open (-N) until Ctrl-C.
func runReverseProxy(cmd *cobra.Command, args []string, bind string) error {
	ref, pairs, err := parseProxyArgs(args)
	if err != nil {
		return usagef("%v", err)
	}
	sshBin, keygenBin, ok := lookSSH()
	if !ok {
		return usagef("--reverse needs the OpenSSH client (ssh and ssh-keygen) on this machine")
	}
	extra := []string{"-N", "-T", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=15"}
	for _, pair := range pairs {
		local, remote := pair[0], pair[1]
		extra = append(extra, "-R", fmt.Sprintf("%d:%s:%d", remote, bind, local))
		if !isJSON() {
			fmt.Fprintf(cmd.ErrOrStderr(), "Forwarding the sandbox's port %d to %s:%d here\n", remote, bind, local)
		}
	}
	if !isJSON() {
		fmt.Fprintln(cmd.ErrOrStderr(), "Press Ctrl-C to stop.")
	}
	return runSSHClient(cmd, ref, "", false, sshBin, keygenBin, extra...)
}
