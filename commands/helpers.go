package commands

import (
	"fmt"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// saveCurrentConfig writes cfg back to disk, silently ignoring errors
// since it's used for best-effort state updates (e.g., clearing current sandbox).
func saveCurrentConfig(cfg config.Config) error {
	return config.Save(cfg)
}

// requireSandbox returns nameOrID if non-empty, falls back to cfg.CurrentSandbox,
// and returns an error if both are empty.
func requireSandbox(nameOrID, current string) (string, error) {
	if nameOrID != "" {
		return nameOrID, nil
	}
	if current != "" {
		return current, nil
	}
	return "", fmt.Errorf("no sandbox specified and no current sandbox set (run 'miosa use <name>')")
}

func sandboxNativeFeatureUnavailable(feature, alternative string) error {
	if alternative == "" {
		return fmt.Errorf("%s is not available on the native sandbox API yet", feature)
	}
	return fmt.Errorf("%s is not available on the native sandbox API yet; %s", feature, alternative)
}
