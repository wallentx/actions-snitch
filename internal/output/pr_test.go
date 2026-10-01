package output

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/github"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/update"
)

func TestPRBodyDeduplicatesAndIncludesEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			_, _ = fmt.Fprint(w, `{"tag_name":"v2","body":"Release details"}`)
		case r.URL.Path == "/repos/owner/action":
			_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
		case strings.HasSuffix(r.URL.Path, "/CHANGELOG.md"):
			_, _ = fmt.Fprint(w, "Changelog details")
		case strings.Contains(r.URL.Path, "/compare/"):
			_, _ = fmt.Fprint(w, `{"status":"ahead","ahead_by":1,"commits":[{"sha":"abcdef123456","html_url":"https://github.com/owner/action/commit/abcdef1","commit":{"message":"Commit details\nMore text"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := github.New("github.com", "", nil)
	client.APIBase = server.URL + "/"
	client.RawBase = server.URL + "/raw/"
	client.HTTP = server.Client()
	client.PublicHTTP = server.Client()
	group := update.Group{ActionPath: "owner/action/sub", Current: "1", Latest: "2", Target: strings.Repeat("b", 40)}
	applied := []update.Applied{{Group: group, File: "one.yml", Count: 2}, {Group: group, File: "two.yml", Count: 1}}
	assessment := model.Assessment{Decision: "allow", Confidence: "high", Findings: []model.Caution{{Detail: "New input <mode>", Safety: "The input is set.", Evidence: "action_definition"}}, Remediations: []model.Remediation{{File: "one.yml", Line: 2, Input: "mode", Operation: "set", CurrentValue: "old", NewValue: "safe", Reason: "The value preserves behavior."}}}
	findings := []model.Finding{{Action: "owner/action/sub@v1", Current: "1", Latest: "2", LatestSHA: model.String(group.Target), UpdateRef: group.Target}}
	body := PRBody(context.Background(), client, applied, findings, map[string]model.Assessment{AssessmentKey(group.ActionPath, group.Current, group.Latest): assessment})
	for _, want := range []string{"with 1 update", "across 3 workflow entries", "Release details", "Changelog details", "Commit details", "Pinned update", "Investigation: Compatibility & Safety Details", "&lt;mode&gt;", "Remediation:"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "## [owner/action/sub]") != 1 {
		t.Fatal("duplicate PR section")
	}
	if PRTitle(applied) != "Bump GitHub Actions dependencies" {
		t.Fatal("wrong multi-entry title")
	}
}
