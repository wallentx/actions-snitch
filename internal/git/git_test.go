package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureRunner struct {
	commands      [][]string
	metadataError bool
	pushURL       string
}

func (f *fixtureRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	f.commands = append(f.commands, append([]string{name}, args...))
	if name == "gh" {
		if len(args) > 1 && args[0] == "repo" && args[1] == "view" {
			if f.metadataError {
				return "", fmt.Errorf("fixture metadata failure")
			}
			if args[2] != f.pushURL {
				return "", fmt.Errorf("wrong PR origin: %s", args[2])
			}
			return `{"url":"https://github.com/fork/actions","defaultBranchRef":{"name":"main"}}`, nil
		}
		if len(args) > 1 && args[0] == "pr" && args[1] == "create" {
			return "https://github.com/fork/actions/pull/1", nil
		}
		if len(args) > 1 && args[0] == "auth" {
			return "fallback", nil
		}
		return "", fmt.Errorf("unexpected gh args")
	}
	return (Commands{}).Run(ctx, dir, name, args...)
}

func initRepo(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	remote := filepath.Join(root, "origin.git")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := func(dir string, args ...string) {
		if _, err := (Commands{}).Run(ctx, dir, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	run(root, "init", "--bare", remote)
	run(repo, "init", "-b", "main")
	run(repo, "config", "user.name", "Fixture")
	run(repo, "config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "action.yml"), []byte("uses: owner/action@v1\n"), 0640); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", "action.yml")
	run(repo, "commit", "-m", "Initial fixture")
	run(repo, "remote", "add", "origin", remote)
	run(repo, "push", "-u", "origin", "main")
	return repo, remote
}

func TestPRPreflightAndScopedPublishing(t *testing.T) {
	repo, remote := initRepo(t)
	runner := &fixtureRunner{pushURL: remote}
	s := Service{Dir: repo, Runner: runner}
	ctx := context.Background()
	target, err := s.Prepare(ctx, true, "actions-update")
	if err != nil {
		t.Fatal(err)
	}
	if target.URL != "https://github.com/fork/actions" || target.Base != "main" || target.Branch != "actions-update" {
		t.Fatal(target)
	}
	if err := os.WriteFile(filepath.Join(repo, "action.yml"), []byte("uses: owner/action@v2\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, target, []string{"action.yml"}, "Bump action", "Fixture body."); err != nil {
		t.Fatal(err)
	}
	last := runner.commands[len(runner.commands)-1]
	joined := strings.Join(last, " ")
	if !strings.Contains(joined, "--repo https://github.com/fork/actions") || !strings.Contains(joined, "--base main") {
		t.Fatal(joined)
	}
	if err := s.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	branch, err := (Commands{}).Run(ctx, repo, "git", "ls-remote", "--heads", "origin", "actions-update")
	if err != nil || branch == "" {
		t.Fatalf("push missing: %s %v", branch, err)
	}
}

func TestFailedPreflightDoesNotSwitch(t *testing.T) {
	repo, remote := initRepo(t)
	s := Service{Dir: repo, Runner: &fixtureRunner{metadataError: true, pushURL: remote}}
	ctx := context.Background()
	if _, err := s.Prepare(ctx, true, "actions-update"); err == nil {
		t.Fatal("metadata failure ignored")
	}
	branch, err := s.Branch(ctx)
	if err != nil || branch != "main" {
		t.Fatalf("branch changed: %s %v", branch, err)
	}
}

func TestPublishPreservesUnrelatedIndex(t *testing.T) {
	repo, remote := initRepo(t)
	s := Service{Dir: repo, Runner: &fixtureRunner{pushURL: remote}}
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(repo, "unrelated"), []byte("user work"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.run(ctx, "git", "add", "unrelated"); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, Target{Branch: "main"}, []string{"action.yml"}, "bad", "bad"); err == nil {
		t.Fatal("unrelated stage committed")
	}
	staged, err := s.run(ctx, "git", "diff", "--cached", "--name-only")
	if err != nil || staged != "unrelated" {
		t.Fatalf("index changed: %s %v", staged, err)
	}
}

func TestTokenHostPrecedence(t *testing.T) {
	lookup := func(k string) (string, bool) {
		m := map[string]string{"GH_TOKEN": "public", "GITHUB_TOKEN": "second", "GH_ENTERPRISE_TOKEN": "enterprise"}
		v, ok := m[k]
		return v, ok
	}
	runner := &fixtureRunner{}
	for host, want := range map[string]string{"github.com": "public", "tenant.ghe.com": "public", "github.internal": "enterprise"} {
		if got := Token(context.Background(), runner, "", host, lookup); got != want {
			t.Errorf("%s: %s", host, got)
		}
	}
	got := Token(context.Background(), runner, "", "github.internal", func(string) (string, bool) { return "", false })
	if got != "fallback" {
		t.Fatal(got)
	}
	last := strings.Join(runner.commands[len(runner.commands)-1], " ")
	if last != "gh auth token --hostname github.internal" {
		t.Fatal(last)
	}
}

func TestSubdirectoryPublishingSeesEntireIndex(t *testing.T) {
	repo, remote := initRepo(t)
	sub := filepath.Join(repo, "nested")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	s := Service{Dir: sub, Runner: &fixtureRunner{pushURL: remote}}
	ctx := context.Background()
	if _, err := s.run(ctx, "git", "config", "diff.relative", "true"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "action.yml"), []byte("uses: owner/action@v2\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "outside"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.run(ctx, "git", "add", "../outside"); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, Target{Branch: "main"}, []string{"action.yml"}, "title", "body"); err == nil || !strings.Contains(err.Error(), "unrelated staged changes") {
		t.Fatalf("outside staged file escaped the initial guard: %v", err)
	}
	staged, err := s.run(ctx, "git", "diff", "--cached", "--no-relative", "--name-only")
	if err != nil || staged != "outside" {
		t.Fatalf("index changed: %s %v", staged, err)
	}
}

func TestSubdirectoryPublishingWithRelativeDiff(t *testing.T) {
	repo, remote := initRepo(t)
	sub := filepath.Join(repo, "nested")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "action.yml")
	if err := os.WriteFile(path, []byte("uses: owner/action@v1\n"), 0640); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{pushURL: remote}
	s := Service{Dir: sub, Runner: runner}
	ctx := context.Background()
	for _, args := range [][]string{{"add", "action.yml"}, {"commit", "-m", "Add nested fixture"}, {"config", "diff.relative", "true"}} {
		if _, err := s.run(ctx, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	target, err := s.Prepare(ctx, true, "actions-update")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("uses: owner/action@v2\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, target, []string{"action.yml"}, "Bump nested action", "Fixture body."); err != nil {
		t.Fatal(err)
	}
	last := runner.commands[len(runner.commands)-1]
	if len(last) < 3 || strings.Join(last[:3], " ") != "gh pr create" {
		t.Fatalf("PR creation was not reached: %v", last)
	}
	if err := s.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	local, err := s.run(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	pushed, err := s.run(ctx, "git", "ls-remote", "--heads", "origin", target.Branch)
	if err != nil || !strings.HasPrefix(pushed, local+"\t") {
		t.Fatalf("updated commit was not pushed: %q %v", pushed, err)
	}
}
