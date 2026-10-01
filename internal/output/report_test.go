package output

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/model"
)

func TestReportsAgainstBash(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq oracle unavailable")
	}
	source, err := os.ReadFile("../../testdata/oracle/actions-snitch.bash")
	if err != nil {
		t.Fatal(err)
	}
	s := string(source)
	start := strings.Index(s, "markdown_link_text_escape() {")
	end := strings.Index(s, "is_interactive() {")
	functions := s[start:end]
	for _, format := range []string{"json", "md", "yaml"} {
		t.Run(format, func(t *testing.T) {
			finding := model.Finding{File: ".github/workflows/test.yml", Line: 12, Action: "owner/action@v1", Repository: "owner/action", Current: "1", Latest: "2", UpdateRef: "v2", CompatibilityScore: 79, VerifiedCreator: true, ReleaseNotes: model.String("https://github.com/owner/action/releases/tag/v2")}
			var got bytes.Buffer
			if err := Report(&got, format, "my repo", "https://github.com/owner/repo", []model.Finding{finding}); err != nil {
				t.Fatal(err)
			}
			prefix := `current_repo_name() { printf 'my repo'; }
current_repo_url() { printf 'https://github.com/owner/repo'; }
release_notes_url() { printf 'https://github.com/owner/action/releases/tag/v2'; }
verified_label() { printf '☑️ %s' "$1"; }
`
			cmd := exec.CommandContext(t.Context(), "bash")
			cmd.Stdin = strings.NewReader(prefix + functions + "\nOUTPUT_FORMAT=" + format + "\nrecord_finding .github/workflows/test.yml 12 owner/action@v1 owner/action 1 2 79 true '' '' '' v2\nrender_report\n")
			want, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("oracle: %v: %s", err, want)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Go:\n%s\nBash:\n%s", got.Bytes(), want)
			}
		})
	}
}

func TestEmptyReports(t *testing.T) {
	for _, format := range []string{"json", "yaml", "md"} {
		var b bytes.Buffer
		if err := Report(&b, format, "repo", "", nil); err != nil || b.Len() != 0 {
			t.Fatalf("%s: %s %v", format, b.String(), err)
		}
	}
}
