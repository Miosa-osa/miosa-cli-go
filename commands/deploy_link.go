package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// A deploy folder records which deployment it belongs to, so a re-run pushes a
// new version to the same deployment instead of creating a duplicate.
const (
	deploymentLinkDir  = ".miosa"
	deploymentLinkName = "deployment.json"
)

type deploymentLink struct {
	DeploymentID string `json:"deployment_id"`
	Name         string `json:"name,omitempty"`
	Tenant       string `json:"tenant,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
}

func deploymentLinkPath(root string) string {
	return filepath.Join(root, deploymentLinkDir, deploymentLinkName)
}

func writeDeploymentLink(root string, link deploymentLink) error {
	dir := filepath.Join(root, deploymentLinkDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(link, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(deploymentLinkPath(root), append(data, '\n'), 0o644)
}

func readDeploymentLink(root string) (deploymentLink, bool) {
	data, err := os.ReadFile(deploymentLinkPath(root))
	if err != nil {
		return deploymentLink{}, false
	}
	var link deploymentLink
	if json.Unmarshal(data, &link) != nil || link.DeploymentID == "" {
		return deploymentLink{}, false
	}
	return link, true
}
