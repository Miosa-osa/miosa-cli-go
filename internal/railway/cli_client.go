package railway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// CLIClient runs the same documents through the Railway CLI's own logged-in
// session (`railway api`), so the token never enters this process, this
// program's memory, or a file. It is how an operator imports a project they are
// already logged in to without pasting a token anywhere.
type CLIClient struct {
	// Binary is the Railway CLI to run; empty means "railway" on PATH.
	Binary string
	// DumpDir mirrors Client.DumpDir.
	DumpDir string
	dumps   int
}

// Query runs one GraphQL document through `railway api` and decodes its data.
func (c *CLIClient) Query(ctx context.Context, query string, variables map[string]any, out any) error {
	binary := c.Binary
	if binary == "" {
		binary = "railway"
	}
	args := []string{"api", "--compact"}
	if len(variables) > 0 {
		body, err := json.Marshal(variables)
		if err != nil {
			return err
		}
		args = append(args, "--variables", string(body))
	}
	args = append(args, query)

	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// `railway api` exits non-zero on a GraphQL error and prints only a
		// count on stderr; Railway's own message is in the answer on stdout.
		var answer graphQLResponse
		if json.Unmarshal(stdout.Bytes(), &answer) == nil && len(answer.Errors) > 0 {
			return fmt.Errorf("railway: %s", joinErrors(answer.Errors))
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		if strings.Contains(message, "not logged in") {
			return fmt.Errorf("railway: the Railway CLI is not logged in; run `railway login` or pass --token-file")
		}
		return fmt.Errorf("railway: %s", firstLine(message))
	}

	var resp graphQLResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return fmt.Errorf("railway: unexpected answer from railway api: %w", err)
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("railway: %s", joinErrors(resp.Errors))
	}
	c.dump(query, variables, resp.Data)
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return fmt.Errorf("railway: unexpected answer: %w", err)
	}
	return nil
}

func (c *CLIClient) dump(query string, variables map[string]any, data json.RawMessage) {
	dump(c.DumpDir, &c.dumps, query, variables, data)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
