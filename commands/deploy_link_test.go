package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentLinkRoundTrip(t *testing.T) {
	root := t.TempDir()

	if _, ok := readDeploymentLink(root); ok {
		t.Fatal("expected no link before the first deploy")
	}

	if err := writeDeploymentLink(root, deploymentLink{DeploymentID: "abc-123", Name: "site"}); err != nil {
		t.Fatal(err)
	}

	got, ok := readDeploymentLink(root)
	if !ok || got.DeploymentID != "abc-123" || got.Name != "site" {
		t.Fatalf("round trip failed: %+v ok=%v", got, ok)
	}

	if _, err := os.Stat(filepath.Join(root, deploymentLinkDir, deploymentLinkName)); err != nil {
		t.Fatal(err)
	}
}
