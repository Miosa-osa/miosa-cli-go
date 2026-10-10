package commands_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestGeneratedDocsCoverTheCommandTree(t *testing.T) {
	var ref, routes bytes.Buffer
	if err := commands.WriteCommandReference(&ref); err != nil {
		t.Fatal(err)
	}
	if err := commands.WriteAPIMap(&routes); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`miosa prompt`", "`miosa env set`", "`miosa snapshot fork`", "`miosa whoami`", "--profile"} {
		if !strings.Contains(ref.String(), want) {
			t.Errorf("command reference misses %s", want)
		}
	}
	if !strings.Contains(routes.String(), "/api-keys") || strings.Contains(ref.String(), "`miosa __docs`\n\n") {
		t.Errorf("api map or hidden command leak:\n%s", routes.String()[:200])
	}
}
