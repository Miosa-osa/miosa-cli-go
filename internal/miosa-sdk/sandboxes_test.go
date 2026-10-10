package miosa

import (
	"encoding/json"
	"testing"
)

func TestSandboxDataAcceptsStructuredMetadata(t *testing.T) {
	var sandbox SandboxData
	input := []byte(`{"id":"sandbox-1","metadata":{"workspace":"workspace-1","labels":{"team":"platform"},"attempt":2}}`)

	if err := json.Unmarshal(input, &sandbox); err != nil {
		t.Fatalf("unmarshal structured metadata: %v", err)
	}

	if got := sandbox.Metadata["workspace"]; got != "workspace-1" {
		t.Fatalf("workspace metadata = %#v, want workspace-1", got)
	}
	if _, ok := sandbox.Metadata["labels"].(map[string]any); !ok {
		t.Fatalf("labels metadata = %#v, want nested object", sandbox.Metadata["labels"])
	}
}
