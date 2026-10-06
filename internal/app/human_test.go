package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/config"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/output"
	"github.com/wallentx/actions-snitch/internal/update"
)

func TestSharedAssessmentGroupsLocationsAndOutcome(t *testing.T) {
	for _, decision := range []string{"review", "allow"} {
		t.Run(decision, func(t *testing.T) {
			first := model.Finding{File: ".github/workflows/ci.yml", Line: 84, Action: "actions/checkout@v4", Repository: "actions/checkout", Current: "4", Latest: "7", CompatibilityScore: 69}
			second, third := first, first
			second.Line = 129
			third.File, third.Line = ".github/workflows/sync.yml", 22
			other := model.Finding{File: first.File, Line: 100, Action: "actions/github-script@v7", Repository: "actions/github-script", Current: "7", Latest: "9", CompatibilityScore: 61}
			findings := []model.Finding{first, other, second, third}
			transcript := &humanTranscript{}
			for i, finding := range findings {
				_, _ = fmt.Fprintf(transcript, "scan diagnostic %d\n", i)
				var findingText bytes.Buffer
				if err := output.Human(&findingText, []model.Finding{finding}, false); err != nil {
					t.Fatal(err)
				}
				_, _ = transcript.Write(findingText.Bytes())
				transcript.mark(findingText.Len())
			}
			_, _ = transcript.WriteString("final scan diagnostic\n")
			assessments := map[string]model.Assessment{
				findingKey(first): {Decision: decision, Summary: "Shared checkout assessment."},
				findingKey(other): {Decision: "review", Summary: "Independent script assessment."},
			}
			var applied []update.Applied
			if decision == "allow" {
				group := update.Group{ActionPath: "actions/checkout", Current: "4", Latest: "7", Target: "v7"}
				applied = []update.Applied{{File: first.File, Group: group, Count: 2}, {File: third.File, Group: group, Count: 1}}
			}
			var result bytes.Buffer
			if err := renderUpdates(&result, transcript, output.Style{}, Options{Update: true}, config.AI{Enabled: true, Threshold: 80}, findings, applied, assessments); err != nil {
				t.Fatal(err)
			}
			for _, once := range []string{
				"Shared checkout assessment.", "Independent script assessment.",
				"Findings in 2 files (3 references):", ".github/workflows/ci.yml: 84, 129", ".github/workflows/sync.yml: 22",
				"scan diagnostic 0", "scan diagnostic 1", "scan diagnostic 2", "scan diagnostic 3", "final scan diagnostic",
			} {
				if strings.Count(result.String(), once) != 1 {
					t.Fatalf("expected %q once:\n%s", once, result.String())
				}
			}
			outcome := "Skipping update of actions/checkout"
			if decision == "allow" {
				outcome = "Updated 3 reference(s) for actions/checkout from 4 to v7"
			}
			if strings.Count(result.String(), outcome) != 1 {
				t.Fatalf("expected one outcome:\n%s", result.String())
			}
		})
	}
}

func TestAssessmentGroupingKeepsPathsAndVersionsSeparate(t *testing.T) {
	findings := []model.Finding{
		{Action: "owner/action@v1", Current: "1", Latest: "3"},
		{Action: "owner/action@v2", Current: "2", Latest: "3"},
		{Action: "owner/action/sub@v1", Current: "1", Latest: "3"},
		{Action: "owner/action@v1", Current: "1", Latest: "4"},
	}
	assessments := map[string]model.Assessment{}
	for _, finding := range findings {
		assessments[findingKey(finding)] = model.Assessment{Decision: "review", Summary: "Identical text."}
	}
	groups, grouped := humanGroups(findings, assessments)
	if grouped || len(groups) != len(findings) {
		t.Fatal("different action paths or versions shared a group")
	}
}

func TestGroupedUpdateIntegration(t *testing.T) {
	r, out, errOut := fixtureRuntime(t, "20", true, true)
	second := filepath.Join(r.Dir, ".github/workflows/other.yml")
	if err := os.WriteFile(second, []byte("steps:\n  - uses: owner/action@v1\n  - uses: owner/action@v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &assessmentModel{response: `{"decision":"allow","confidence":"high","summary":"Shared decision.","findings":[],"remediations":[]}`}
	r.Model = fake
	if code := Run(t.Context(), []string{"-u"}, r); code != 0 {
		t.Fatal(errOut.String())
	}
	if fake.calls != 1 || strings.Count(out.String(), "AI assessment:") != 1 || !strings.Contains(out.String(), "Updated 3 reference(s) for owner/action") {
		t.Fatalf("calls=%d output=%s", fake.calls, out.String())
	}
	data, err := os.ReadFile(second)
	if err != nil || strings.Count(string(data), "owner/action@v2") != 2 {
		t.Fatalf("updates changed behavior: %v %s", err, data)
	}
}
