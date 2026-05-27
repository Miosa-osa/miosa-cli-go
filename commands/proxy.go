package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newProxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "proxy [name|id] <local>:<remote> [<local>:<remote> ...]",
		Short: "Forward local ports to sandbox ports",
		Long: `Forward one or more local TCP ports to ports inside a sandbox.

Each mapping is specified as <local-port>:<remote-port>.
The command blocks until interrupted with Ctrl-C.

Examples:
  miosa proxy my-box 8080:80
  miosa proxy my-box 5432:5432 6379:6379
  miosa proxy 8080:80          # uses current sandbox`,
		Args: cobra.MinimumNArgs(1),
		RunE: runProxy,
	}
}

func runProxy(cmd *cobra.Command, args []string) error {
	_, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	// Args: optional [name|id] followed by one or more local:remote pairs.
	nameOrID, pairs, err := parseProxyArgs(args)
	if err != nil {
		return die(err)
	}
	_ = pairs
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	return die(sandboxNativeFeatureUnavailable(
		"sandbox TCP proxy",
		"use POST /api/v1/sandboxes/{id}/expose for HTTP previews until a native sandbox tunnel endpoint is deployed",
	))
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
