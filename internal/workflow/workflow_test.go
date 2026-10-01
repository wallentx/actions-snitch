package workflow

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, root, path, content string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryAndIgnoreOracle(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{".github/workflows/a.yml", ".github/workflows/nested/b.yaml", "action.yaml", "nested/action.yml", ".git/action.yml", "node_modules/action.yml", "fixture space/action.yml", ".github/workflows/keep.yml", "ignored-by-git/action.yml"} {
		fixture(t, root, p, "runs:\n  steps:\n    - uses: actions/checkout@v2\n")
	}
	fixture(t, root, ".gitignore", "ignored-by-git/\n")
	fixture(t, root, ".snitchignore", "fixture space/\r\n.github/workflows/*.yml\n!.github/workflows/keep.yml")
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".github/workflows/keep.yml", ".github/workflows/nested/b.yaml", "action.yaml", "ignored-by-git/action.yml", "nested/action.yml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v != %v", got, want)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("Bash oracle unavailable")
	}
	original, err := os.ReadFile("../../testdata/oracle/actions-snitch.bash")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(original), "snitch_ignored() {")
	end := strings.Index(string(original), "scan_files() {")
	cmd := exec.CommandContext(t.Context(), "bash")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(string(original[start:end]) + "\nfind_action_files\n")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("oracle: %v: %s", err, b)
	}
	if string(b) != strings.Join(got, "\n")+"\n" {
		t.Fatalf("oracle=%s; Go=%v", b, got)
	}
}

func TestIgnoreBashGlobs(t *testing.T) {
	rules := ParseIgnore([]byte("# comment\nfoo*/\n!foo/keep/action.yml\n[ab]?/[[:digit:]].yml\nliteral\\*.yml\n"))
	for p, want := range map[string]bool{"foo/deep/action.yml": true, "foo/keep/action.yml": false, "az/2.yml": true, "az/x.yml": false, "literal*.yml": true, "literalx.yml": false} {
		if got := rules.Matches(p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
}

func TestPositionsAgainstYQ(t *testing.T) {
	if _, err := exec.LookPath("yq"); err != nil {
		t.Skip("yq oracle unavailable")
	}
	path := "../../.github/scripts/fixtures/update-layout.yml"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse("layout.yml", b)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "yq", "-r", `.. | select(tag == "!!map" and has("uses")) | [(.uses | line), (.uses | column), .uses] | @tsv`, path)
	oracle, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, u := range f.Usages {
		_, _ = fmt.Fprintf(&got, "%d\t%d\t%s\n", u.Line, u.Column, u.Action)
	}
	if got.String() != string(oracle) {
		t.Fatalf("positions differ:\nGo:\n%s\nBash/yq:\n%s", got.String(), oracle)
	}
}

func TestMultiDocumentAndUnicode(t *testing.T) {
	b := []byte("steps: [{name: café, uses: 'owner/repo@v1'}]\n---\nruns:\n  steps:\n    - uses: owner/repo/sub@v2\n")
	f, err := Parse("action.yml", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Documents) != 2 || len(f.Usages) != 2 || f.Usages[0].Column != 28 || f.Usages[1].Document != 1 || f.Usages[1].Line != 5 {
		t.Fatalf("%+v", f.Usages)
	}
	if !bytes.Equal(f.Original, b) {
		t.Fatal("snapshot changed")
	}
}

func TestCyclicAliasRejected(t *testing.T) {
	if _, err := Parse("action.yml", []byte("a: &a [*a]\n")); err == nil {
		t.Fatal("recursive alias accepted")
	}
}

func TestExtendedGlobsAgainstBash(t *testing.T) {
	patterns := []string{"@(fixtures|examples)/*", "+(ab|c)", "?(a|b)c", "*(ab|c)", "!(ab|c)", "a!(b|c)d", "@(@(a|b)|c)*", "[!a-z]*", "[[:digit:]]?", "literal\\*"}
	paths := []string{"fixtures/action.yml", "examples/nested/action.yaml", "action.yml", "", "ab", "abc", "abab", "c", "ac", "ad", "abd", "acd", "aXd", "1x", "literal*"}
	for _, pattern := range patterns {
		for _, path := range paths {
			cmd := exec.CommandContext(t.Context(), "bash", "-c", `[[ "$1" == $2 ]]`, "oracle", path, pattern)
			want := cmd.Run() == nil
			got := globMatches(parseGlob([]rune(pattern)), []rune(path))
			if got != want {
				t.Errorf("pattern %q path %q: Go=%v Bash=%v", pattern, path, got, want)
			}
		}
	}
}

func TestUnreadableUnrelatedDirectoryDoesNotHideWorkflows(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	fixture(t, root, ".github/workflows/main.yml", "uses: owner/action@v1\n")
	fixture(t, root, ".snitchignore", "blocked/\n")
	dir := filepath.Join(root, "blocked")
	if err := os.Mkdir(dir, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	paths, warnings, err := DiscoverWithWarnings(root)
	if err != nil || !reflect.DeepEqual(paths, []string{".github/workflows/main.yml"}) || len(warnings) == 0 {
		t.Fatalf("paths=%v warnings=%v err=%v", paths, warnings, err)
	}
}
