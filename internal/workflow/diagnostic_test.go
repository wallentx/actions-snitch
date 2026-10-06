package workflow

import (
	"strings"
	"testing"
)

func TestParseDiagnosticsDoNotEchoScalars(t *testing.T) {
	for _, source := range []string{
		"env:\n  API_TOKEN: !!int PRIVATE_SECRET_MARKER\n",
		"env:\n  API_TOKEN: !!int 'line 123456789012345'\n",
		"env:\n  PRIVATE_SECRET_MARKER: a\n  PRIVATE_SECRET_MARKER: b\n",
		"env:\n  API_TOKEN: *PRIVATE_SECRET_MARKER\n",
	} {
		_, err := Parse(".github/workflows/ci.yml", []byte(source))
		if err == nil || strings.Contains(err.Error(), "PRIVATE_SECRET_MARKER") || strings.Contains(err.Error(), "123456789012345") || !strings.Contains(err.Error(), "yaml:") {
			t.Fatalf("unsafe or missing parse diagnostic: %v", err)
		}
	}
	_, err := Parse("ci.yml", []byte("jobs:\n  test:\n    steps: [\n"))
	if err == nil || !strings.Contains(err.Error(), "line ") {
		t.Fatalf("missing parser location: %v", err)
	}
}
