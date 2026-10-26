package commands

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// buildVisibility is --build / --no-build on a variable write. A build-visible
// variable reaches the build (a Dockerfile ARG, a framework's public variable)
// as well as the running service; by default a variable is runtime only.
type buildVisibility struct {
	on, off bool
}

func addBuildFlags(cmd *cobra.Command) *buildVisibility {
	b := &buildVisibility{}
	cmd.Flags().BoolVar(&b.on, "build", false, "Make the variables visible to the build too (Dockerfile ARG, build-time framework variables)")
	cmd.Flags().BoolVar(&b.off, "no-build", false, "Make the variables runtime only again")
	cmd.MarkFlagsMutuallyExclusive("build", "no-build")
	return b
}

// value is the build flag to send: true, false, or nil when neither flag was
// given (the API keeps each variable's current setting).
func (b *buildVisibility) value() *bool {
	switch {
	case b.on:
		v := true
		return &v
	case b.off:
		v := false
		return &v
	}
	return nil
}

// variablesBody is the API's variables payload. Without a build flag it is the
// {NAME: value} map; with one it is the entry list, each entry carrying
// build, which is the only shape that sets the flag. A build-visible variable
// must have a value, so an empty one is refused here rather than by the API.
func variablesBody(vars map[string]any, build *bool) (any, error) {
	if build == nil {
		return vars, nil
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]map[string]any, 0, len(names))
	for _, name := range names {
		value, _ := vars[name].(string)
		if *build && strings.TrimSpace(value) == "" {
			return nil, usagef("%s has no value; a build-visible variable needs one", name)
		}
		entries = append(entries, map[string]any{"name": name, "value": vars[name], "build": *build})
	}
	return entries, nil
}

// buildNote is the line printed after a write that changed build visibility.
func buildNote(build *bool) string {
	switch {
	case build == nil:
		return ""
	case *build:
		return "build-visible: the next build sees them (a rebuild, not a redeploy, applies them)"
	default:
		return "runtime only: the next build no longer sees them"
	}
}
