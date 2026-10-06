package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/workflow"
)

func TestActionReleaseFamilySelection(t *testing.T) {
	for _, tc := range []struct {
		name, ref string
		pin       bool
	}{{"tag", "v4.38.1", false}, {"pin", "v4.38.1", true}, {"sha", oldSHA, false}} {
		t.Run(tc.name, func(t *testing.T) {
			pages := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/github/codeql-action/releases/latest":
					_, _ = fmt.Fprint(w, `{"tag_name":"codeql-bundle-v2.27.1"}`)
				case "/repos/github/codeql-action/releases":
					pages++
					if r.URL.Query().Get("page") == "2" {
						_, _ = fmt.Fprint(w, `[{"tag_name":"v4.38.2"},{"tag_name":"v4.38.1"},{"tag_name":"v3.99.0"}]`)
					} else {
						w.Header().Set("Link", `</repos/github/codeql-action/releases?per_page=100&page=2>; rel="next"`)
						_, _ = fmt.Fprint(w, `[{"tag_name":"codeql-bundle-v2.27.1"},{"tag_name":"v90.0.0","draft":true},{"tag_name":"v80.0.0","prerelease":true},{"tag_name":"v70.0.0-beta.1"}]`)
					}
				case "/repos/github/codeql-action/tags":
					_, _ = fmt.Fprintf(w, `[{"name":"v4.38.1","commit":{"sha":%q}}]`, oldSHA)
				case "/repos/github/codeql-action/commits/v4.38.2":
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, newSHA)
				case "/repos/github/codeql-action/compare/" + oldSHA + "..." + newSHA:
					_, _ = fmt.Fprint(w, `{"status":"ahead","ahead_by":7,"behind_by":0}`)
				default:
					t.Errorf("unexpected or cross-product request: %s", r.URL)
					http.NotFound(w, r)
				}
			})
			usage := workflow.Usage{File: ".github/workflows/codeql.yml", Line: 66, Action: "github/codeql-action/init@" + tc.ref, ActionPath: "github/codeql-action/init", Repository: "github/codeql-action", Current: strings.TrimPrefix(tc.ref, "v"), Ref: tc.ref}
			result := client.Resolve(t.Context(), usage, tc.pin)
			if result.Finding == nil {
				t.Fatalf("no action update: %+v", result)
			}
			finding := result.Finding
			wantRef := "v4.38.2"
			if tc.pin || tc.name == "sha" {
				wantRef = newSHA
			}
			if finding.Latest != "4.38.2" || finding.UpdateRef != wantRef || pages != 2 {
				t.Fatalf("wrong release family: %+v pages=%d", finding, pages)
			}
			if finding.ReleaseNotes == nil || *finding.ReleaseNotes != "https://github.com/github/codeql-action/releases/tag/v4.38.2" {
				t.Fatal("wrong release notes")
			}
			if tc.name == "sha" && (finding.CurrentTag == nil || *finding.CurrentTag != "v4.38.1") {
				t.Fatal("lost SHA tag identity")
			}
		})
	}
}

func TestUncertainReleaseFamilyFailsClosed(t *testing.T) {
	for _, failure := range []string{"no-matching-release", "failed-page", "untagged-sha", "failed-tags", "older-release"} {
		t.Run(failure, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/action/releases/latest":
					_, _ = fmt.Fprint(w, `{"tag_name":"other-product-v9.0.0"}`)
				case "/repos/owner/action/releases":
					switch failure {
					case "failed-page":
						w.Header().Set("Link", `</failed>; rel="next"`)
						_, _ = fmt.Fprint(w, `[{"tag_name":"v4.38.2"}]`)
					case "older-release":
						_, _ = fmt.Fprint(w, `[{"tag_name":"v3.0.0"}]`)
					default:
						_, _ = fmt.Fprint(w, `[{"tag_name":"other-product-v9.0.0"}]`)
					}
				case "/repos/owner/action/tags":
					if failure == "failed-tags" {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
					} else {
						_, _ = fmt.Fprint(w, `[]`)
					}
				case "/failed":
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				default:
					t.Errorf("unsafe target request: %s", r.URL)
					http.NotFound(w, r)
				}
			})
			ref := "v4.38.1"
			if failure == "untagged-sha" || failure == "failed-tags" {
				ref = oldSHA
			}
			usage := workflow.Usage{Action: "owner/action@" + ref, ActionPath: "owner/action", Repository: "owner/action", Ref: ref, Current: strings.TrimPrefix(ref, "v")}
			result := client.Resolve(context.Background(), usage, true)
			if result.Finding != nil {
				t.Fatalf("unsafe family accepted: %+v", result.Finding)
			}
			if failure != "older-release" && len(result.Warnings) == 0 {
				t.Fatal("uncertainty was not reported")
			}
		})
	}
}

func TestReleaseFamilySelectionIsNotCodeQLSpecific(t *testing.T) {
	for _, tag := range []string{"cli-v20.0.0", "compiler/2.0.0", "tool_bundle_v3.4.0"} {
		if !productReleaseTag.MatchString(tag) {
			t.Errorf("product tag not recognized: %s", tag)
		}
	}
	for _, tag := range []string{"v4.38.2", "4.38.2", "v4", "release-stable"} {
		if productReleaseTag.MatchString(tag) {
			t.Errorf("ordinary tag misclassified: %s", tag)
		}
	}
}
