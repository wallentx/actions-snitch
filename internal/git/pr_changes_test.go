package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRChangesIncludeCommittedAndPendingUpdates(t *testing.T) {
	repo, remote := initRepo(t)
	s := Service{Dir: repo, Runner: &fixtureRunner{pushURL: remote}}
	before := "steps:\n- uses: actions/checkout@v4\n- uses: actions/setup-go@v5\n"
	file := filepath.Join(repo, "action.yml")
	if err := os.WriteFile(file, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "Base workflows"}, {"push", "origin", "main"}, {"remote", "set-url", "--push", "origin", remote}, {"remote", "set-url", "origin", filepath.Join(repo, "unavailable-fetch-url")}} {
		if _, err := s.run(t.Context(), "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	target, err := s.Prepare(t.Context(), true, "actions-update")
	if err != nil {
		t.Fatal(err)
	}
	prior := strings.ReplaceAll(before, "checkout@v4", "checkout@v7")
	if err := os.WriteFile(file, []byte(prior), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "Earlier checkout update"}, {"config", "diff.relative", "true"}} {
		if _, err := s.run(t.Context(), "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	after := strings.ReplaceAll(prior, "setup-go@v5", "setup-go@v7")
	if err := os.WriteFile(file, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	s.Dir = filepath.Join(repo, "nested")
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	changes, err := s.PRChanges(t.Context(), target)
	if err != nil || len(changes) != 1 {
		t.Fatalf("snapshot: %+v %v", changes, err)
	}
	if changes[0].Path != "action.yml" || string(changes[0].Before) != strings.TrimRight(before, "\n") || string(changes[0].After) != after {
		t.Fatalf("PR comparison excludes earlier updates: %+v", changes)
	}
}

func TestPRChangesIncludeAddedAndRenamedWorkflows(t *testing.T) {
	repo, remote := initRepo(t)
	s := Service{Dir: repo, Runner: &fixtureRunner{pushURL: remote}}
	old := "name: Fixture action\ndescription: A stable description retained across the rename\nruns:\n  using: composite\n  steps:\n  - uses: owner/action@v1\n"
	if err := os.WriteFile(filepath.Join(repo, "action.yml"), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "action.yml"}, {"commit", "-m", "Base action metadata"}, {"push", "origin", "main"}} {
		if _, err := s.run(t.Context(), "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	target, err := s.Prepare(t.Context(), true, "updates")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"renamed", ".github/workflows"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.run(t.Context(), "git", "mv", "action.yml", "renamed/action.yml"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".github/workflows/new.yml"), []byte("steps:\n- uses: owner/other@v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "Add workflow and move action"}} {
		if _, err := s.run(t.Context(), "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "renamed/action.yml"), []byte(strings.ReplaceAll(old, "@v1", "@v2")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".github/workflows/new.yml"), []byte("steps:\n- uses: owner/other@v2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changes, err := s.PRChanges(t.Context(), target)
	if err != nil || len(changes) != 2 {
		t.Fatalf("changes=%+v error=%v", changes, err)
	}
	for _, change := range changes {
		if change.Path == "renamed/action.yml" && !strings.Contains(string(change.Before), "owner/action@v1") {
			t.Fatal("rename lost its base")
		}
		if change.Path == ".github/workflows/new.yml" && len(change.Before) != 0 {
			t.Fatal("added file was given an invented base")
		}
		if !strings.Contains(string(change.After), "@v2") {
			t.Fatal("pending update was omitted")
		}
	}
}
