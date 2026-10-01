package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/cache"
	"github.com/wallentx/actions-snitch/internal/github"
)

// TestGoParityHelper runs the production Go runner in a separate executable,
// substituting only HTTP origins. No fixture switches enter the shipped CLI.
func TestGoParityHelper(t *testing.T) {
	if os.Getenv("SNITCH_PARITY_HELPER") != "1" {
		return
	}
	base := os.Getenv("SNITCH_FIXTURE_URL")
	store := &cache.Store{Dir: filepath.Join(os.Getenv("XDG_CACHE_HOME"), "actions-snitch")}
	c := github.New("github.com", "", store)
	c.APIBase = base + "/api/"
	c.RawBase = base + "/raw/"
	c.WebBase = base + "/web/"
	c.BadgeBase = base + "/badge"
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Exit(Run(context.Background(), args, Runtime{Client: c}))
}

const parityGH = `#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" != api ]]; then exit 1; fi
endpoint="$2"; shift 2
query=""; paginate=false
while (($#)); do
 case "$1" in
 --jq) query="$2"; shift 2 ;;
 --paginate) paginate=true; shift ;;
 --slurp) shift ;;
 --method) shift 2 ;;
 -f) shift 2 ;;
 *) shift ;;
 esac
done
result=$("$SNITCH_REAL_CURL" -fsS "$SNITCH_FIXTURE_URL/api/$endpoint")
if $paginate; then result=$(printf '%s' "$result" | jq -s '.'); fi
if [[ -n "$query" ]]; then printf '%s' "$result" | jq -r "$query"; else printf '%s\n' "$result"; fi
`

const parityCurl = `#!/usr/bin/env bash
set -euo pipefail
url=""
for arg in "$@"; do url="$arg"; done
case "$url" in
 https://raw.githubusercontent.com/*) url="$SNITCH_FIXTURE_URL/raw/${url#https://raw.githubusercontent.com/}" ;;
 https://github.com/*) url="$SNITCH_FIXTURE_URL/web/${url#https://github.com/}" ;;
 https://dependabot-badges.githubapp.com/*) url="$SNITCH_FIXTURE_URL/badge?${url#*?}" ;;
 *) exit 1 ;;
esac
exec "$SNITCH_REAL_CURL" -fsS "$url"
`

func TestBashGoExecutableParity(t *testing.T) {
	for _, tool := range []string{"bash", "jq", "yq", "curl", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("oracle tool unavailable: " + tool)
		}
	}
	oracle, err := filepath.Abs("../../testdata/oracle/actions-snitch.bash")
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realCurl, err := exec.LookPath("curl")
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("a", 40)
	latestSHA := strings.Repeat("b", 40)
	cases := []struct {
		name, ref, release, status, score string
		verified                          bool
		args                              []string
		ignore                            string
	}{
		{"colored-help", "v1", "v2", "ahead", "95", true, []string{"-h"}, ""},
		{"colored-scan", "v1", "v2.3.4", "ahead", "95", true, nil, ""},
		{"colored-update", "v1", "v2.3.4", "ahead", "95", true, []string{"-u"}, ""},
		{"verbose-current", "v2.3.4", "v2.3.4", "ahead", "95", true, []string{"-v"}, ""},
		{"verbose-current-sha", latestSHA, "v2.3.4", "ahead", "95", true, []string{"-v"}, ""},
		{"unavailable", "v1", "", "ahead", "95", true, []string{"-v"}, ""},
		{"human-scan", "v1", "v2.3.4", "ahead", "95", true, nil, ""},
		{"verbose-scan", "v1", "v2.3.4", "ahead", "95", true, []string{"-v"}, ""},
		{"json-scan", "v1", "v2.3.4", "ahead", "95", true, []string{"-o", "json"}, ""},
		{"markdown-scan", "v1.2.3", "v2.3.4", "ahead", "79", true, []string{"-o", "md"}, ""},
		{"yaml-scan", "v1", "v2.3.4", "ahead", "Unknown", false, []string{"-o", "yaml"}, ""},
		{"human-update", "v1", "v2.3.4", "ahead", "95", true, []string{"-u"}, ""},
		{"human-gated", "v1", "v2.3.4", "ahead", "20", true, []string{"-u"}, ""},
		{"tag-update", "v1", "v2.3.4", "ahead", "95", true, []string{"-u", "-o", "json"}, ""},
		{"unknown-gate", "v1", "v2", "ahead", "Unknown", true, []string{"-u", "-o", "json"}, ""},
		{"force", "v1", "v2", "ahead", "20", false, []string{"-uf", "-o", "json"}, ""},
		{"verified-over-force", "v1", "v2", "ahead", "20", false, []string{"-uft", "-o", "json"}, ""},
		{"same-major", "v2", "v2.3.4", "ahead", "95", true, []string{"-u", "-o", "json"}, ""},
		{"current-pin", "v2.3.4", "v2.3.4", "ahead", "95", true, []string{"-us", "-o", "json"}, ""},
		{"sha-ahead", old, "v2.3.4", "ahead", "95", true, []string{"-u", "-o", "json"}, ""},
		{"sha-behind", old, "v2.3.4", "behind", "95", true, []string{"-uf", "-o", "json"}, ""},
		{"sha-diverged", old, "v2.3.4", "diverged", "95", true, []string{"-uf", "-o", "json"}, ""},
		{"no-release", old, "", "ahead", "95", true, []string{"-uf", "-o", "json"}, ""},
		{"ignore", "v1", "v2", "ahead", "95", true, []string{"-u", "-o", "json"}, ".github/\n!./.github/workflows/main.yml\nignored/\n"},
		{"branch-skip", "main", "v2", "ahead", "95", true, []string{"-usf", "-o", "json"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/releases/latest"):
					if tc.release == "" {
						http.NotFound(w, r)
					} else {
						_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tc.release)
					}
				case r.URL.Path == "/api/repos/owner/action":
					if tc.name == "unavailable" {
						http.NotFound(w, r)
						return
					}
					_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
				case strings.Contains(r.URL.Path, "/commits/"):
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, latestSHA)
				case strings.Contains(r.URL.Path, "/compare/"):
					_, _ = fmt.Fprintf(w, `{"status":%q,"ahead_by":302,"behind_by":0,"commits":[]}`, tc.status)
				case strings.HasSuffix(r.URL.Path, "/tags"):
					_, _ = fmt.Fprintf(w, `[{"name":"v1.2.3","commit":{"sha":%q}},{"name":"v1","commit":{"sha":%q}}]`, old, old)
				case strings.HasPrefix(r.URL.Path, "/raw/") && strings.HasSuffix(r.URL.Path, "action.yml"):
					_, _ = fmt.Fprint(w, "name: Fixture Action\nruns:\n  using: composite\n  steps: []\n")
				case strings.HasPrefix(r.URL.Path, "/web/marketplace/"):
					if tc.verified {
						_, _ = fmt.Fprint(w, "GitHub has manually verified the creator of the action as an official partner organization.")
					} else {
						_, _ = fmt.Fprint(w, "unverified")
					}
				case r.URL.Path == "/badge":
					_, _ = fmt.Fprintf(w, "<svg><title>compatibility: %s%%</title></svg>", tc.score)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"gh": parityGH, "curl": parityCurl} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			var outputs, errorsOut [][]byte
			var trees []map[string]string
			for _, implementation := range []string{"bash", "go"} {
				workspace := filepath.Join(root, implementation, "repo")
				if err := os.MkdirAll(workspace, 0700); err != nil {
					t.Fatal(err)
				}
				paths := []string{".github/workflows/main.yml", "composite/action.yaml", "ignored/action.yml"}
				for _, path := range paths {
					p := filepath.Join(workspace, path)
					if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
						t.Fatal(err)
					}
					source := fmt.Sprintf("steps:\n  - uses: owner/action@%s # keep\n  - run: echo 'owner/action@%s'\n", tc.ref, tc.ref)
					if err := os.WriteFile(p, []byte(source), 0640); err != nil {
						t.Fatal(err)
					}
				}
				if tc.ignore != "" {
					if err := os.WriteFile(filepath.Join(workspace, ".snitchignore"), []byte(tc.ignore), 0600); err != nil {
						t.Fatal(err)
					}
				}
				env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, implementation), "XDG_CONFIG_HOME=" + filepath.Join(root, implementation, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, implementation, "cache"), "CI=true", "TERM=dumb", "SNITCH_REAL_CURL=" + realCurl, "SNITCH_FIXTURE_URL=" + server.URL}
				if strings.HasPrefix(tc.name, "colored") {
					env = append(env, "TERM=xterm")
				}
				var command *exec.Cmd
				if implementation == "bash" {
					command = exec.CommandContext(t.Context(), "bash", append([]string{oracle}, tc.args...)...)
				} else {
					env = append(env, "SNITCH_PARITY_HELPER=1")
					command = exec.CommandContext(t.Context(), goBinary, append([]string{"-test.run=^TestGoParityHelper$", "--"}, tc.args...)...)
				}
				command.Dir = workspace
				command.Env = env
				var stdout, stderr bytes.Buffer
				command.Stdout = &stdout
				command.Stderr = &stderr
				if err := command.Run(); err != nil {
					t.Fatalf("%s: %v\n%s", implementation, err, stderr.String())
				}
				outputs = append(outputs, bytes.ReplaceAll(stdout.Bytes(), []byte(workspace), []byte("<workspace>")))
				errorsOut = append(errorsOut, stderr.Bytes())
				tree := map[string]string{}
				for _, path := range paths {
					b, err := os.ReadFile(filepath.Join(workspace, path))
					if err != nil {
						t.Fatal(err)
					}
					info, err := os.Stat(filepath.Join(workspace, path))
					if err != nil {
						t.Fatal(err)
					}
					tree[path] = fmt.Sprintf("%o:%s", info.Mode().Perm(), b)
				}
				trees = append(trees, tree)
			}
			if !bytes.Equal(outputs[0], outputs[1]) {
				t.Errorf("stdout differs\nBash: %s\nGo: %s", outputs[0], outputs[1])
			}
			if !bytes.Equal(errorsOut[0], errorsOut[1]) {
				t.Errorf("stderr differs\nBash: %s\nGo: %s", errorsOut[0], errorsOut[1])
			}
			for path, contents := range trees[0] {
				if trees[1][path] != contents {
					t.Errorf("%s contents/mode differ\nBash: %s\nGo: %s", path, contents, trees[1][path])
				}
			}
		})
	}
}
