package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugPreservesStructuredOutput(t *testing.T) {
	r, out, errOut := fixtureRuntime(t, "20", true, true)
	args := []string{"-o", "json"}
	if code := Run(context.Background(), args, r); code != 0 {
		t.Fatal(errOut.String())
	}
	want := out.String()
	if strings.Contains(errOut.String(), "debug:") {
		t.Fatal("debug output was enabled without --debug")
	}
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), append(args, "--debug"), r); code != 0 {
		t.Fatal(errOut.String())
	}
	if out.String() != want || !strings.Contains(errOut.String(), "debug: ai enabled=true") {
		t.Fatalf("stdout changed or stderr lacks trace:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
	for _, value := range []string{"hidden-env", "hidden-token", "owner/action@v1"} {
		if strings.Contains(errOut.String(), value) {
			t.Fatal("debug output included workflow contents")
		}
	}
}

func TestEvidenceFailureDoesNotExposeInvalidScalars(t *testing.T) {
	r, out, errOut := fixtureRuntime(t, "20", true, true)
	fake := &assessmentModel{}
	r.Model = fake
	if err := os.WriteFile(filepath.Join(r.Dir, ".github/workflows/broken.yml"), []byte("env:\n  API_TOKEN: !!int PRIVATE_SECRET_MARKER\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := Run(t.Context(), []string{"-uf", "-v", "--debug"}, r); code != 0 {
		t.Fatal(errOut.String())
	}
	if fake.calls != 0 || strings.Contains(out.String()+errOut.String(), "PRIVATE_SECRET_MARKER") {
		t.Fatal("invalid workflow evidence leaked a scalar or reached the model")
	}
	if !strings.Contains(out.String(), "broken.yml") || !strings.Contains(out.String(), "invalid scalar value or type") {
		t.Fatalf("missing actionable failure: %s", out.String())
	}
}
