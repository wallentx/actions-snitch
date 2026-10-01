package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/workflow"
)

func writeSnapshot(t *testing.T, root, path string, data []byte) *workflow.File {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0640); err != nil {
		t.Fatal(err)
	}
	f, err := workflow.Read(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestVersionUpdatesPreserveLayout(t *testing.T) {
	original, err := os.ReadFile("../../.github/scripts/fixtures/update-layout.yml")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("../../.github/scripts/fixtures/update-layout-expected.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"LF", "CRLF", "no-final-newline"} {
		t.Run(style, func(t *testing.T) {
			from, to := bytes.Clone(original), bytes.Clone(expected)
			if style == "CRLF" {
				from = bytes.ReplaceAll(from, []byte("\n"), []byte("\r\n"))
				to = bytes.ReplaceAll(to, []byte("\n"), []byte("\r\n"))
			}
			if style == "no-final-newline" {
				from = bytes.TrimSuffix(from, []byte("\n"))
				to = bytes.TrimSuffix(to, []byte("\n"))
			}
			root := t.TempDir()
			f := writeSnapshot(t, root, "action.yml", from)
			plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "actions/checkout", Current: "2", Latest: "4", Target: "v4"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Changes) != 1 || plan.Changes[0].Count != 17 {
				t.Fatalf("unexpected edit count: %+v", plan.Changes)
			}
			if !bytes.Equal(plan.Changes[0].Data, to) {
				t.Fatalf("layout mismatch:\n%s", plan.Changes[0].Data)
			}
			if err := plan.Commit(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(root, "action.yml"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0640 {
				t.Fatal(info.Mode())
			}
		})
	}
}

func TestAliasCollateralFailsClosed(t *testing.T) {
	for _, source := range []string{
		"steps:\n  - uses: &ref owner/action@v1\n  - run: *ref\n",
		"steps:\n  - uses: owner/action@v1\n    with: &inputs\n      mode: old\n  - uses: other/action@v1\n    with: *inputs\n",
	} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			f := writeSnapshot(t, root, "action.yml", []byte(source))
			g := Group{ActionPath: "owner/action", Current: "1", Target: "v2"}
			if strings.Contains(source, "&inputs") {
				g.Remediations = []model.Remediation{{File: "action.yml", Line: 2, Input: "mode", Operation: "set", CurrentValue: "old", NewValue: "new"}}
			}
			if _, err := Build([]*workflow.File{f}, []Group{g}); err == nil {
				t.Fatal("collateral alias change accepted")
			}
			b, err := os.ReadFile(filepath.Join(root, "action.yml"))
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != source {
				t.Fatal("file changed")
			}
		})
	}
}

func TestRemediationGroupsUseOriginalPositions(t *testing.T) {
	root := t.TempDir()
	f := writeSnapshot(t, root, "action.yml", []byte("steps:\n  - uses: owner/first@v1\n  - uses: owner/second@v1\n    with:\n      old: value\n"))
	groups := []Group{
		{ActionPath: "owner/first", Current: "1", Target: "v2", Remediations: []model.Remediation{{File: "action.yml", Line: 2, Input: "mode", Operation: "set", CurrentValue: "<absent>", NewValue: "safe"}}},
		{ActionPath: "owner/second", Current: "1", Target: "v2", Remediations: []model.Remediation{{File: "action.yml", Line: 3, Input: "old", Operation: "remove", CurrentValue: "value", NewValue: "<removed>"}}},
	}
	plan, err := Build([]*workflow.File{f}, groups)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := workflow.Parse("action.yml", plan.Changes[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Usages) != 2 || parsed.Usages[0].Ref != "v2" || parsed.Usages[1].Ref != "v2" {
		t.Fatal("references not updated")
	}
	if workflow.Field(workflow.Field(parsed.Usages[0].Step, "with"), "mode").Value != "safe" {
		t.Fatal("wrong remediation target")
	}
	if workflow.Field(workflow.Field(parsed.Usages[1].Step, "with"), "old") != nil {
		t.Fatal("input was not removed")
	}
}

func TestInvalidRemediationsLeaveAllFilesUntouched(t *testing.T) {
	base := model.Remediation{File: "action.yml", Line: 2, Input: "mode", Operation: "set", CurrentValue: "old", NewValue: "safe"}
	cases := []func(*model.Remediation){func(r *model.Remediation) { r.Input = "githubToken" }, func(r *model.Remediation) { r.NewValue = "${{ toJSON(secrets) }}" }, func(r *model.Remediation) { r.Line = 1 }, func(r *model.Remediation) { r.CurrentValue = "stale" }, func(r *model.Remediation) { r.File = "../action.yml" }, func(r *model.Remediation) { r.NewValue = "x\ny" }, func(r *model.Remediation) { r.Operation = "patch" }}
	for _, mutate := range cases {
		root := t.TempDir()
		source := []byte("steps:\n  - uses: owner/action@v1\n    with:\n      mode: old\n")
		f := writeSnapshot(t, root, "action.yml", source)
		bad := base
		mutate(&bad)
		if _, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2", Remediations: []model.Remediation{base, bad}}}); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
		b, err := os.ReadFile(filepath.Join(root, "action.yml"))
		if err != nil || !bytes.Equal(b, source) {
			t.Fatalf("modified source: %v", err)
		}
	}
}

func TestCommitRejectsChangedSourceAndRollsBack(t *testing.T) {
	for _, kind := range []string{"changed", "rename", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			source := []byte("uses: owner/action@v1\n")
			a := writeSnapshot(t, root, "a/action.yml", source)
			b := writeSnapshot(t, root, "b/action.yml", source)
			plan, err := Build([]*workflow.File{a, b}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "changed" {
				if err := os.WriteFile(filepath.Join(root, a.Path), []byte("external change\n"), 0640); err != nil {
					t.Fatal(err)
				}
				if err := plan.Commit(context.Background(), root); err == nil {
					t.Fatal("overwrote changed source")
				}
				return
			}
			calls := 0
			err = plan.commit(context.Background(), root, func(r *os.Root, from, to string) error {
				calls++
				if calls == 2 || (kind == "rollback" && calls == 3) {
					return errors.New("injected rename failure")
				}
				return r.Rename(from, to)
			})
			if err == nil {
				t.Fatal("missing failure")
			}
			if kind == "rollback" {
				if !strings.Contains(err.Error(), "original retained at") {
					t.Fatal(err)
				}
				backups, e := filepath.Glob(filepath.Join(root, "a/.actions-snitch-*"))
				if e != nil || len(backups) != 1 {
					t.Fatalf("missing recovery backup: %v %v", backups, e)
				}
				return
			}
			for _, f := range []*workflow.File{a, b} {
				data, e := os.ReadFile(filepath.Join(root, f.Path))
				if e != nil || !bytes.Equal(data, source) {
					t.Fatalf("rollback failed: %s %v", data, e)
				}
			}
		})
	}
}

func TestUnicodeFlowAndDocuments(t *testing.T) {
	source := []byte("steps: [{name: café, uses: 'owner/action@v1'}, {uses: owner/action@v1}]\n---\nuses: owner/action@v1\n")
	f, err := workflow.Parse("action.yml", source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.ReplaceAll(source, []byte("@v1"), []byte("@v2"))
	if !bytes.Equal(plan.Changes[0].Data, want) {
		t.Fatalf("%s", plan.Changes[0].Data)
	}
}

func TestWholeStepAliasPreservesAuthorizedChanges(t *testing.T) {
	source := []byte("steps:\n  - &shared\n    uses: owner/action@v1\n    with:\n      mode: old\n  - *shared\n")
	for _, remediate := range []bool{false, true} {
		f, err := workflow.Parse("action.yml", source)
		if err != nil {
			t.Fatal(err)
		}
		g := Group{ActionPath: "owner/action", Current: "1", Target: "v2"}
		if remediate {
			g.Remediations = []model.Remediation{{File: "action.yml", Line: 3, Input: "mode", Operation: "set", CurrentValue: "old", NewValue: "safe"}}
		}
		plan, err := Build([]*workflow.File{f}, []Group{g})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Applied) != 1 || plan.Applied[0].Count != 1 {
			t.Fatal("alias was counted as a separate source edit")
		}
		if !remediate && !bytes.Equal(plan.Changes[0].Data, bytes.ReplaceAll(source, []byte("@v1"), []byte("@v2"))) {
			t.Fatal("whole-step alias layout changed")
		}
		parsed, err := workflow.Parse("action.yml", plan.Changes[0].Data)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := parsed.Documents[0].Decode(&document); err != nil {
			t.Fatal(err)
		}
		steps := document["steps"].([]any)
		for _, step := range steps {
			mapping := step.(map[string]any)
			if mapping["uses"] != "owner/action@v2" {
				t.Fatal("alias ref did not update")
			}
			if remediate && mapping["with"].(map[string]any)["mode"] != "safe" {
				t.Fatal("alias input did not update")
			}
		}
	}
}

func TestInPlaceRollbackIncludesPartiallyFailedFile(t *testing.T) {
	root := t.TempDir()
	original := []byte("uses: owner/action@v1\n")
	a := writeSnapshot(t, root, "a/action.yml", original)
	b := writeSnapshot(t, root, "b/action.yml", original)
	plan, err := Build([]*workflow.File{a, b}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = plan.commitInPlace(context.Background(), root, func(file *os.File, data []byte) error {
		calls++
		if calls == 2 {
			if _, err := file.WriteAt([]byte("partial"), 0); err != nil {
				return err
			}
			return errors.New("injected partial write")
		}
		return writeContents(file, data)
	})
	if err == nil || calls != 4 {
		t.Fatalf("rollback did not cover both writes: %v calls=%d", err, calls)
	}
	for _, file := range []*workflow.File{a, b} {
		data, err := os.ReadFile(filepath.Join(root, file.Path))
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("partial write survived: %s %v", data, err)
		}
		info, err := os.Stat(filepath.Join(root, file.Path))
		if err != nil || !os.SameFile(info, file.Info) {
			t.Fatal("rollback replaced the inode")
		}
	}
}

func TestCancellationAfterFinalWriteRollsBack(t *testing.T) {
	root := t.TempDir()
	original := []byte("uses: owner/action@v1\n")
	f := writeSnapshot(t, root, "action.yml", original)
	plan, err := Build([]*workflow.File{f}, []Group{{ActionPath: "owner/action", Current: "1", Target: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = plan.commitInPlace(ctx, root, func(file *os.File, data []byte) error { err := writeContents(file, data); cancel(); return err })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "action.yml"))
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("cancelled write was not rolled back")
	}
}
