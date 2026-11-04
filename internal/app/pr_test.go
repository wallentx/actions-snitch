package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitops "github.com/wallentx/actions-snitch/internal/git"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/output"
	"github.com/wallentx/actions-snitch/internal/update"
)

type prCaptureRunner struct {
	gitops.Commands
	title string
	body  string
}

func (r *prCaptureRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name != "gh" {
		return r.Commands.Run(ctx, dir, name, args...)
	}
	if len(args) > 1 && args[0] == "repo" && args[1] == "view" {
		return `{"url":"https://github.com/fixture/repo","defaultBranchRef":{"name":"main"}}`, nil
	}
	if len(args) > 1 && args[0] == "pr" && args[1] == "create" {
		for i, arg := range args {
			if arg == "--body" {
				r.body = args[i+1]
			}
			if arg == "--title" {
				r.title = args[i+1]
			}
		}
		return "https://github.com/fixture/repo/pull/1", nil
	}
	return "", fmt.Errorf("unexpected GitHub command")
}

func TestForcedAIPRIncludesInvestigationAndEarlierUpdates(t *testing.T) {
	r, _, errOut := fixtureRuntime(t, "20", true, true)
	runner := &prCaptureRunner{}
	r.Runner = runner
	r.Model = &assessmentModel{response: `{"decision":"allow","confidence":"high","summary":"The migration is safe with the input fix.","findings":[{"caution_detail":"The mode input changed.","safety":"Setting mode to safe preserves the workflow behavior.","evidence":"action_definition"}],"remediations":[{"file":".github/workflows/main.yml","line":2,"input":"mode","operation":"set","current_value":"old","new_value":"safe","reason":"The new version needs this value."}]}`}
	priorPath := filepath.Join(r.Dir, ".github/workflows/prior.yml")
	if err := os.WriteFile(priorPath, []byte("steps:\n- uses: owner/previous@v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "origin.git")
	for _, args := range [][]string{{"init", "--bare", remote}, {"init", "-b", "main"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}, {"add", ".github"}, {"commit", "-m", "Base"}, {"remote", "add", "origin", remote}, {"push", "-u", "origin", "main"}, {"checkout", "-b", "actions-update"}} {
		if _, err := runner.Run(t.Context(), r.Dir, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(priorPath, []byte("steps:\n- uses: owner/previous@v2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".github"}, {"commit", "-m", "Earlier action update"}} {
		if _, err := runner.Run(t.Context(), r.Dir, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	if code := Run(t.Context(), []string{"-ufp", "-b", "actions-update"}, r); code != 0 {
		t.Fatal(errOut.String())
	}
	for _, want := range []string{"with 2 updates", "## [owner/previous]", "## [owner/action]", "Investigation: Compatibility & Safety Details", "**Caution Detail:** The mode input changed.", "**Safety:** Setting mode to safe", "**Remediation:** Set"} {
		if !strings.Contains(runner.body, want) {
			t.Fatalf("missing %q in PR body: %s", want, runner.body)
		}
	}
	if runner.title != "Bump GitHub Actions dependencies" {
		t.Fatalf("title excludes prior updates: %s", runner.title)
	}
	data, err := os.ReadFile(filepath.Join(r.Dir, ".github/workflows/main.yml"))
	if err != nil || !strings.Contains(string(data), "owner/action@v2") || !strings.Contains(string(data), "mode: safe") {
		t.Fatalf("approved repair missing: %s %v", data, err)
	}
}

func TestPRUpdatesIncludeEarlierBranchCommits(t *testing.T) {
	base := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n      - uses: actions/checkout@v4\n"
	head := strings.ReplaceAll(strings.ReplaceAll(base, "checkout@v4", "checkout@v7"), "setup-go@v5", "setup-go@v7")
	changes := []gitops.FileChange{
		{Path: ".github/workflows/ci.yml", Before: []byte(base), After: []byte(head)},
		{Path: ".github/workflows/publish.yml", Before: []byte("steps:\n- uses: actions/checkout@v4\n"), After: []byte("steps:\n- uses: actions/checkout@v7\n")},
	}
	recent := []update.Applied{{File: changes[0].Path, Group: update.Group{ActionPath: "actions/setup-go", Current: "5", Latest: "7", Target: "v7"}, Count: 1}}
	applied, err := prUpdates(changes, recent)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, a := range applied {
		counts[a.Group.ActionPath] += a.Count
	}
	if counts["actions/checkout"] != 3 || counts["actions/setup-go"] != 1 || len(counts) != 2 {
		t.Fatalf("PR summary lost earlier updates: %+v", applied)
	}
	if output.PRTitle(applied) != "Bump GitHub Actions dependencies" {
		t.Fatal("PR title describes only the latest invocation")
	}
}

func TestPRUpdatesUseNetVersionAndSemanticMappingPaths(t *testing.T) {
	changes := []gitops.FileChange{{Path: "action.yml",
		Before: []byte("name: action\nsteps:\n- uses: owner/action@v1\n"),
		After:  []byte("description: extra property\nsteps:\n- uses: owner/action@v3\nname: action\n"),
	}}
	recent := []update.Applied{{Group: update.Group{ActionPath: "owner/action", Current: "2", Latest: "3", Target: "v3"}, Count: 1}}
	applied, err := prUpdates(changes, recent)
	if err != nil || len(applied) != 1 || applied[0].Group.Current != "1" || applied[0].Group.Latest != "3" {
		t.Fatalf("net update: %+v %v", applied, err)
	}
}

func TestPRUpdatesRetainResolvedSHAMetadata(t *testing.T) {
	sha := strings.Repeat("a", 40)
	changes := []gitops.FileChange{{Path: "action.yml", Before: []byte("uses: owner/action@v1\n"), After: []byte("uses: owner/action@" + sha + "\n")}}
	recent := []update.Applied{{Group: update.Group{ActionPath: "owner/action", Current: "1", Latest: "2", Target: sha}, Count: 1}}
	applied, err := prUpdates(changes, recent)
	if err != nil || len(applied) != 1 || applied[0].Group.Latest != "2" || applied[0].Group.Target != sha {
		t.Fatalf("SHA metadata lost: %+v %v", applied, err)
	}
}

func TestPRUpdatesSurviveInsertedSteps(t *testing.T) {
	before := "steps:\n- uses: actions/checkout@v4\n- uses: actions/setup-go@v5\n"
	after := "steps:\n- run: echo added\n- uses: actions/checkout@v7\n- uses: actions/setup-go@v7\n"
	updates, err := prUpdates([]gitops.FileChange{{Path: "action.yml", Before: []byte(before), After: []byte(after)}}, nil)
	if err != nil || len(updates) != 2 || updates[0].Group.Current != "4" || updates[1].Group.Current != "5" {
		t.Fatalf("inserted step hid updates: %+v %v", updates, err)
	}
}

func TestPRUpdatesKeepNamedStepIdentity(t *testing.T) {
	before := "steps:\n- id: first\n  uses: owner/action@v1\n- id: second\n  uses: owner/action@v2\n"
	after := "steps:\n- id: second\n  uses: owner/action@v2\n- id: first\n  uses: owner/action@v1\n"
	updates, err := prUpdates([]gitops.FileChange{{Path: "action.yml", Before: []byte(before), After: []byte(after)}}, nil)
	if err != nil || len(updates) != 0 {
		t.Fatalf("moving steps was reported as a version change: %+v %v", updates, err)
	}
}

func TestPRUpdatesDescribeAddedWorkflowReferences(t *testing.T) {
	updates, err := prUpdates([]gitops.FileChange{{Path: ".github/workflows/new.yml", After: []byte("steps:\n- uses: owner/action@v2\n")}}, []update.Applied{{Group: update.Group{ActionPath: "owner/action", Current: "1", Latest: "2", Target: "v2"}, Count: 1}})
	if err != nil || len(updates) != 1 || updates[0].Group.Current != "" || updates[0].Group.Latest != "2" {
		t.Fatalf("new workflow was lost: %+v %v", updates, err)
	}
}

func TestPRAssessmentsExcludeUnappliedRepairs(t *testing.T) {
	failed := output.AssessmentKey("owner/action", "2", "3")
	applied := update.Applied{Group: update.Group{ActionPath: "owner/other", Current: "1", Latest: "2"}, Count: 1}
	succeeded := output.AssessmentKey("owner/other", "1", "2")
	all := map[string]model.Assessment{
		failed:    {Decision: "allow", Remediations: []model.Remediation{{Input: "token", NewValue: "unsafe"}}},
		succeeded: {Decision: "allow", Findings: []model.Caution{{Detail: "The runtime changed.", Safety: "The runner supports it."}}},
	}
	selected := appliedAssessments(all, []update.Applied{applied})
	if len(selected) != 1 || selected[succeeded].Decision != "allow" {
		t.Fatalf("PR evidence includes a rejected repair: %+v", selected)
	}
}
