package commands_test

import (
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// The spec says a refused --org exits 3. The REST client's error type was not
// mapped, so every command exited 1 (QA, CLI 2.3.11 gate).
func TestRefusedOrgExitsThreeOnEveryCommand(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		return 403, `{"error":{"code":"NOT_A_MEMBER","message":"You are not a member of organization osa."}}`
	})
	for _, args := range [][]string{{"--org", "osa", "list"}, {"--org", "osa", "deploy", "list"}, {"--org", "osa", "forge", "repo", "list"}, {"--org", "osa", "db", "list"}} {
		_, err := run(t, args...)
		if code := commands.ExitCode(err); code != 3 {
			t.Errorf("%v exited %d, want 3: %v", args, code, err)
		}
	}
}
