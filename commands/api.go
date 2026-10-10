package commands

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func newAPICmd() *cobra.Command {
	var (
		method  string
		data    string
		headers []string
		include bool
	)
	cmd := &cobra.Command{
		Use:   "api [METHOD] <path>",
		Short: "Make an authenticated API request",
		Long: `Send a request to the MIOSA API with your stored credentials and print the
response body. The path is relative to the API base URL (/api/v1). Use it for
anything the CLI has no command for yet.

The method defaults to GET, or POST when --data is given. --data takes a JSON
string, @file to read a file, or - to read standard input. A failing status
exits non-zero (see 'miosa --help' for exit codes) and still prints the body.

  miosa api /credits/balance
  miosa api GET /sandboxes/my-box/ports
  miosa api POST /webhooks -d '{"url":"https://example.com/hook","events":["sandbox.ready"]}'
  miosa api PATCH /sandboxes/my-box/tags -d @tags.json
  echo '{"name":"x"}' | miosa api POST /workspaces -d -`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPI(cmd, args, method, data, headers, include)
		},
	}
	cmd.Flags().StringVarP(&method, "method", "X", "", "HTTP method (default GET, or POST with --data)")
	cmd.Flags().StringVarP(&data, "data", "d", "", "Request body: JSON, @file, or - for stdin")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "Extra header, 'Name: value' (repeatable)")
	cmd.Flags().BoolVarP(&include, "include", "i", false, "Print the status line and response headers first")
	return cmd
}

var httpMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true}

func runAPI(cmd *cobra.Command, args []string, method, data string, headers []string, include bool) error {
	path := args[len(args)-1]
	if len(args) == 2 {
		if !httpMethods[strings.ToUpper(args[0])] {
			return usagef("unknown method %q (use GET, POST, PUT, PATCH or DELETE)", args[0])
		}
		method = strings.ToUpper(args[0])
	}
	var body io.Reader
	switch {
	case data == "-":
		b, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return die(fmt.Errorf("reading standard input: %w", err))
		}
		body = strings.NewReader(string(b))
	case strings.HasPrefix(data, "@"):
		b, err := os.ReadFile(data[1:])
		if err != nil {
			return die(err)
		}
		body = strings.NewReader(string(b))
	case data != "":
		body = strings.NewReader(data)
	}
	if method == "" {
		method = http.MethodGet
		if body != nil {
			method = http.MethodPost
		}
	}
	method = strings.ToUpper(method)
	if !httpMethods[method] {
		return usagef("unknown method %q", method)
	}

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	hdr := map[string]string{}
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return usagef("header %q must look like 'Name: value'", h)
		}
		hdr[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	// Split a query string off the path so it is encoded once.
	q := url.Values{}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		parsed, perr := url.ParseQuery(path[i+1:])
		if perr != nil {
			return usagef("bad query string in %q: %v", path, perr)
		}
		q, path = parsed, path[:i]
	}
	path = "/" + strings.TrimLeft(path, "/")

	req := api.Request{Method: method, Path: path, Query: q, Headers: hdr}
	if body != nil {
		req.Body = body
		req.ContentType = "application/json"
		if hdr["Content-Type"] != "" {
			req.ContentType = hdr["Content-Type"]
		}
	}
	resp, err := c.API.Do(cmd.Context(), req)
	out := cmd.OutOrStdout()
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && len(ae.Body) > 0 {
			if include {
				fmt.Fprintf(out, "HTTP %d\n\n", ae.Status)
			}
			out.Write(ae.Body)
			if ae.Body[len(ae.Body)-1] != '\n' {
				fmt.Fprintln(out)
			}
		}
		return die(err)
	}
	defer resp.Body.Close()
	if include {
		fmt.Fprintf(out, "%s %s\n", resp.Proto, resp.Status)
		for k, v := range resp.Header {
			fmt.Fprintf(out, "%s: %s\n", k, strings.Join(v, ", "))
		}
		fmt.Fprintln(out)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return die(err)
	}
	out.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Fprintln(out)
	}
	return nil
}
