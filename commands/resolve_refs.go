package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type displayArgsKey struct{}

func withDisplayArgs(ctx context.Context, display []string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, displayArgsKey{}, display)
}

// argsOf returns the positional references as the caller typed them, for a
// Before hook. The API client turns each into an id when it is sent.
func argsOf(cmd *cobra.Command) []string {
	if v, ok := cmd.Context().Value(displayArgsKey{}).([]string); ok {
		return v
	}
	return nil
}

// notFoundContext names the resource and the reference on a 404, instead of a
// bare "Not Found": "deployment lunivate-site, domain www.example.com: not found".
func (o Op) notFoundContext(err error, display []string) error {
	ae, ok := asAPIError(err)
	if !ok || ae.Status != 404 || strings.HasPrefix(ae.Message, "no ") {
		return err
	}
	parts := make([]string, 0, len(o.Args))
	for i, a := range o.Args {
		if i < len(display) && display[i] != "" {
			parts = append(parts, a.Name+" "+display[i])
		}
	}
	if len(parts) == 0 {
		return err
	}
	return fmt.Errorf("%s: %w", strings.Join(parts, ", "), err)
}
