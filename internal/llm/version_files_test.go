package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/workflow"
)

func TestVersionFilesExposeOnlyVersionDirectives(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module private.invalid/PRIVATE_TOKEN\n\ngo 1.27.1\ntoolchain go1.27.3\n// PRIVATE_TOKEN\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := workflow.Parse("ci.yml", []byte("steps:\n- uses: actions/setup-go@v5\n  with:\n    go-version-file: go.mod\n"))
	if err != nil {
		t.Fatal(err)
	}
	facts := localVersionFiles(root, []*workflow.File{file}, "actions/setup-go", "5")
	if len(facts) != 1 || !facts[0].Available || facts[0].Go != "1.27.1" || facts[0].Toolchain != "go1.27.3" {
		t.Fatalf("missing facts: %+v", facts)
	}
	encoded, _ := json.Marshal(facts)
	if strings.Contains(string(encoded), "PRIVATE_TOKEN") || strings.Contains(string(encoded), "private.invalid") {
		t.Fatal("version evidence exposed unrelated file content")
	}
}

func TestVersionFileAmbiguityFailsClosed(t *testing.T) {
	for _, source := range []string{
		"go 1.27.1\ngo 1.28.0\n", "go 1.27.1\ntoolchain PRIVATE_TOKEN\n", "/*\ngo 1.27.1\n*/\n", "go PRIVATE_TOKEN\n",
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		fact := readVersionFile(root, "go.mod")
		if fact.Available || fact.Go != "" || fact.Toolchain != "" || strings.Contains(fact.Problem, "PRIVATE_TOKEN") {
			t.Fatalf("unsafe facts: %+v", fact)
		}
	}
}

func TestVersionFileCannotLeaveRepository(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(outside, "go.mod")
	if err := os.WriteFile(path, []byte("go 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", path, "../go.mod", ".env", "${{ secrets.VERSION_FILE }}"} {
		if fact := readVersionFile(root, name); fact.Available {
			t.Fatalf("read unsafe file: %+v", fact)
		}
	}
}

func TestRejectedVersionFileDoesNotExposeSecretExpression(t *testing.T) {
	path := "${{ secrets.FILE || 'PRIVATE_FALLBACK_VALUE' }}"
	fact := readVersionFile(t.TempDir(), path)
	encoded, _ := json.Marshal(fact)
	if fact.Available || strings.Contains(string(encoded), "PRIVATE_FALLBACK_VALUE") || strings.Contains(string(encoded), "secrets.FILE") {
		t.Fatalf("rejected path escaped redaction: %s", encoded)
	}
}
