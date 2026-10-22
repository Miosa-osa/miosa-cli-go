package commands

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// linkedEnv resolves the linked service's variables to their real values, in
// memory only: nothing is written to disk or printed. It needs env:reveal,
// because a deploy is the only other place a value is read in the clear.
func linkedEnv(ctx context.Context, c *api.Client, link linkFile) ([]string, error) {
	if link.ServiceID == "" {
		return nil, usagef("the link names no service; run `miosa link <project> --service <name>`")
	}
	rows, err := listServiceVariables(ctx, c, link.ProjectID, link.ServiceID)
	if err != nil {
		return nil, err
	}
	env := make([]string, 0, len(rows))
	for _, row := range rows {
		var out struct {
			Data struct {
				Value string `json:"value"`
			} `json:"data"`
		}
		path := servicePath(link.ProjectID, link.ServiceID) + "/variables/" + url.PathEscape(row.Name) + "/reveal"
		if err := c.Get(ctx, path, nil, &out); err != nil {
			return nil, fmt.Errorf("reading %s: %w (a local run needs env:reveal)", row.Name, err)
		}
		env = append(env, row.Name+"="+out.Data.Value)
	}
	return env, nil
}

// execWithEnv runs argv with the service's variables added to this process's
// environment, passing output and the exit code through unchanged.
func execWithEnv(cmd *cobra.Command, env []string, argv []string) error {
	if len(argv) == 0 {
		return usagef("no command given")
	}
	child := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	child.Env = append(os.Environ(), env...)
	child.Stdin = os.Stdin
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()
	err := child.Run()
	var exit *exec.ExitError
	if err == nil {
		return nil
	}
	if asExit(err, &exit) {
		return &ExitError{Code: exit.ExitCode()}
	}
	return die(err)
}

// runLinkedLocal is `miosa run -- <cmd>` in a linked directory: the command runs
// here with the service's variables, which is what Railway's `run` does.
func runLinkedLocal(cmd *cobra.Command, argv []string) error {
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	link, err := readLink()
	if err != nil {
		return usagef("%v", err)
	}
	env, err := linkedEnv(cmd.Context(), c.API, link)
	if err != nil {
		return die(err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "running with %d variable(s) from %s\n", len(env), link.Service)
	return execWithEnv(cmd, env, argv)
}

// shellLinkedLocal opens the user's shell with the linked service's variables.
func shellLinkedLocal(cmd *cobra.Command) error {
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	link, err := readLink()
	if err != nil {
		return usagef("%v", err)
	}
	env, err := linkedEnv(cmd.Context(), c.API, link)
	if err != nil {
		return die(err)
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "shell with %d variable(s) from %s; exit to return\n", len(env), link.Service)
	return execWithEnv(cmd, env, []string{shell})
}

// asExit is errors.As for *exec.ExitError without importing errors here twice.
func asExit(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
