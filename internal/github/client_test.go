package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/cache"
	"github.com/wallentx/actions-snitch/internal/workflow"
)

const oldSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const newSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c := New("github.com", "test-token", nil)
	c.APIBase = s.URL + "/"
	c.HTTP = s.Client()
	c.RawBase = s.URL + "/raw/"
	c.WebBase = s.URL + "/web/"
	c.BadgeBase = s.URL + "/badge"
	c.PublicHTTP = s.Client()
	return c
}

func TestSHAAncestryAndAllTags(t *testing.T) {
	for _, status := range []string{"ahead", "behind", "identical", "diverged", "invalid"} {
		t.Run(status, func(t *testing.T) {
			pages := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing API credentials")
				}
				switch r.URL.Path {
				case "/repos/owner/action/releases/latest":
					_, _ = fmt.Fprint(w, `{"tag_name":"v4.2.1"}`)
				case "/repos/owner/action/commits/v4.2.1":
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, newSHA)
				case "/repos/owner/action/compare/" + oldSHA + "..." + newSHA:
					_, _ = fmt.Fprintf(w, `{"status":%q,"ahead_by":302,"behind_by":0}`, status)
				case "/repos/owner/action/tags":
					pages++
					if r.URL.Query().Get("page") == "2" {
						_, _ = fmt.Fprintf(w, `[{"name":"v3.1.0","commit":{"sha":%q}},{"name":"v3.2.0","commit":{"sha":%q}}]`, oldSHA, oldSHA)
					} else {
						w.Header().Set("Link", `</repos/owner/action/tags?per_page=100&page=2>; rel="next"`)
						_, _ = fmt.Fprintf(w, `[{"name":"v3","commit":{"sha":%q}}]`, oldSHA)
					}
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.NotFound(w, r)
				}
			})
			u := workflow.Usage{File: "action.yml", Line: 2, Action: "owner/action@" + oldSHA, ActionPath: "owner/action", Repository: "owner/action", Current: oldSHA, Ref: oldSHA}
			result := c.Resolve(context.Background(), u, false)
			if status != "ahead" {
				if result.Finding != nil {
					t.Fatal("unsafe ancestry allowed")
				}
				return
			}
			f := result.Finding
			if f == nil || f.CurrentTag == nil || *f.CurrentTag != "v3.1.0" || f.CommitsSince == nil || *f.CommitsSince != 302 || pages != 2 {
				t.Fatalf("result=%+v pages=%d", result, pages)
			}
		})
	}
}

func TestTagPaginationFailsClosed(t *testing.T) {
	for _, next := range []string{"https://hostile.invalid/tags", "/failure"} {
		t.Run(next, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/failure" {
					http.Error(w, "bad", 500)
					return
				}
				w.Header().Set("Link", "<"+next+">; rel=\"next\"")
				_, _ = fmt.Fprintf(w, `[{"name":"v1.0.0","commit":{"sha":%q}}]`, oldSHA)
			})
			if _, err := c.Tags(context.Background(), "owner/action"); err == nil {
				t.Fatal("accepted partial or hostile pages")
			}
		})
	}
}

func TestCacheScopeAndCorruptData(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
	})
	c.Cache = &cache.Store{Dir: t.TempDir()}
	ctx := context.Background()
	if _, err := c.Repository(ctx, "owner/action"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Repository(ctx, "owner/action"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	c.Token = "other-token"
	if _, err := c.Repository(ctx, "owner/action"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("auth cache collision")
	}
	if err := c.Cache.Put(c.key("repos/owner/action"), []byte("broken")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Repository(ctx, "owner/action"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("corrupt cache trusted")
	}
}

func TestPublicEvidenceHasNoCredentials(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential disclosure")
		}
		_, _ = fmt.Fprint(w, "ok")
	})
	if _, err := c.Raw(context.Background(), "owner/action", "v1", "action.yml"); err != nil {
		t.Fatal(err)
	}
}

func TestRefPolicies(t *testing.T) {
	cases := []struct {
		current, latest string
		pin             bool
		want            string
		wantCurrent     bool
	}{
		{"v3", "v3.2.1", false, "", true},
		{"v3", "v3", false, "", true},
		{"v3", "release-stable", false, "", false},
		{"v3", "v4.2.1", false, "v4", false},
		{"3.1", "4.2.1", false, "4.2.1", false},
		{"v4.2.1", "v4.2.1", true, newSHA, false},
		{"release-stable", "release-stable", true, newSHA, false},
		{"main", "v4", true, "", false},
		{"v4.0-rc1", "v4.0-rc2", false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.current+"_"+tc.latest, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "commits") {
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, newSHA)
				} else {
					_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tc.latest)
				}
			})
			u := workflow.Usage{Action: "owner/action@" + tc.current, ActionPath: "owner/action", Repository: "owner/action", Current: strings.TrimPrefix(tc.current, "v"), Ref: tc.current}
			result := c.Resolve(context.Background(), u, tc.pin)
			got := ""
			if result.Finding != nil {
				got = result.Finding.UpdateRef
			}
			if got != tc.want {
				t.Fatalf("%s != %s", got, tc.want)
			}
			if result.Current != tc.wantCurrent {
				t.Fatalf("current status = %v, want %v", result.Current, tc.wantCurrent)
			}
		})
	}
}

func TestComparisonCountValidation(t *testing.T) {
	for raw, want := range map[string]bool{"302": true, "302.0": true, "3.02e2": true, "0": false, "-1": false, "1.5": false, `"302"`: false, "null": false, "true": false, "1e999999999": false} {
		if _, ok := positiveCount([]byte(raw)); ok != want {
			t.Errorf("%s: %v", raw, ok)
		}
	}
}

func TestGitHubHostClassification(t *testing.T) {
	for host, want := range map[string]string{"github.com": "https://api.github.com/", "tenant.ghe.com": "https://api.tenant.ghe.com/", "github.internal": "https://github.internal/api/v3/"} {
		if got := New(host, "", nil).APIBase; got != want {
			t.Errorf("%s: %s != %s", host, got, want)
		}
	}
}

func TestVerifiedCreatorUsesCompleteMetadataAndFallback(t *testing.T) {
	for _, first := range []string{"name: Fixture Action\ndescription: \"" + strings.Repeat("x", 15000) + "\"\n", "description: no name\n", "name: [invalid"} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/owner/action":
				_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
			case "/raw/owner/action/main/action.yml":
				_, _ = fmt.Fprint(w, first)
			case "/raw/owner/action/main/action.yaml":
				_, _ = fmt.Fprint(w, "name: Fixture Action\n")
			case "/web/marketplace/actions/fixture-action":
				_, _ = fmt.Fprint(w, verifiedSentence)
			default:
				http.NotFound(w, r)
			}
		})
		if !c.Verified(context.Background(), "owner/action") {
			t.Fatal("valid verified creator was rejected")
		}
	}
}
