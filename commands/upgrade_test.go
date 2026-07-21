package commands

import (
	"strings"
	"testing"
)

func TestInstallScriptURLUsesPublishedInstaller(t *testing.T) {
	if strings.Contains(installScriptURL, "install.miosa.ai") {
		t.Fatalf("install script URL points at the unconfigured domain: %s", installScriptURL)
	}

	const want = "https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh"
	if installScriptURL != want {
		t.Fatalf("install script URL = %q, want %q", installScriptURL, want)
	}
}
