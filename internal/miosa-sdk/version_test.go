package miosa

import "testing"

func TestEmbeddedSDKVersion(t *testing.T) {
	if sdkVersion != "2.0.3" {
		t.Fatalf("sdkVersion = %q, want 2.0.3", sdkVersion)
	}
}
