package commands

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReadSecretValueFromStdin(t *testing.T) {
	cases := []struct {
		in, want, err string
	}{
		{in: "npm_abc\n", want: "npm_abc"},
		{in: "line one\nline two", want: "line one\nline two"},
		{in: "token\r\n", want: "token"},
		{in: "\n", err: "no value given"},
		{in: strings.Repeat("x", 8193), err: "longer than 8192 bytes"},
	}
	for _, c := range cases {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader(c.in))
		got, err := readSecretValue(cmd, "NPM_TOKEN")
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: want error %q, got %v", c.in, c.err, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestForgeSecretNames(t *testing.T) {
	for name, ok := range map[string]bool{"NPM_TOKEN": true, "A": true, "npm_token": false, "9X": false, "NPM-TOKEN": false} {
		if forgeSecretName.MatchString(name) != ok {
			t.Errorf("%s: want valid=%v", name, ok)
		}
	}
}
