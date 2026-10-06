package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/creack/pty"
	"github.com/wallentx/actions-snitch/internal/lintrepair"
	"golang.org/x/term"
)

func TestLintReviewWorkflow(t *testing.T) {
	if _, err := exec.LookPath("actionlint"); err != nil {
		t.Skip("actionlint is unavailable")
	}
	source := "on: push\njobs:\n  build:\n    runs-on: [self-hosted\n    steps:\n      - run: echo private-secret\n"
	for _, tc := range []struct {
		name, script, answer string
		apply, fail          bool
	}{
		{"accept", "cp \"$2\" \"$1\"", "yes\n", true, false},
		{"reject", "cp \"$2\" \"$1\"", "n\n", false, false},
		{"ours", "exit 0", "", false, false},
		{"cancel", "cp \"$2\" \"$1\"; exit 130", "yes\n", false, true},
		{"invalid selection", "printf 'bad' > \"$1\"", "yes\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ".github/workflows/main.yml")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			tool := filepath.Join(dir, "review")
			if err := os.WriteFile(tool, []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			e, err := lintrepair.Prepare([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			proposal := strings.Replace(e.Text, "[self-hosted", "[self-hosted]", 1)
			response, _ := json.Marshal(map[string]string{"repair": proposal})
			model := &assessmentModel{response: string(response)}
			answerPath := filepath.Join(dir, "answer")
			if err := os.WriteFile(answerPath, []byte(tc.answer), 0600); err != nil {
				t.Fatal(err)
			}
			input, err := os.Open(answerPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close() }()
			var output bytes.Buffer
			r := Runtime{Dir: dir, Interactive: true, Model: model, Input: input, Output: &output, Error: &output, Lookup: func(key string) (string, bool) {
				switch key {
				case "DIFFTOOL":
					return tool, true
				case "XDG_CONFIG_HOME":
					return dir, true
				}
				return "", false
			}}
			err = executeLint(t.Context(), r)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v output=%s", err, output.String())
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := source
			if tc.apply {
				expected = strings.Replace(source, "[self-hosted", "[self-hosted]", 1)
			}
			if string(got) != expected {
				t.Fatalf("unexpected file contents %q", got)
			}
			if strings.Contains(model.prompt, "private-secret") || strings.Contains(output.String(), "private-secret") {
				t.Fatal("secret exposed outside local diff")
			}
		})
	}
}

func TestLintInvalidProposal(t *testing.T) {
	e, err := lintrepair.Prepare([]byte("runs-on: [self-hosted\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Restore(strings.Replace(e.Text, "self-hosted", "ubuntu-latest", 1)); err == nil {
		t.Fatal("runner change accepted")
	}
	if err := lintrepair.Check(t.Context(), t.TempDir(), "test.yml", []byte("runs-on: [self-hosted\n")); err == nil {
		t.Fatal("invalid proposal validated")
	}
}

func TestLintReviewRestoresTerminal(t *testing.T) {
	if _, err := exec.LookPath("stty"); err != nil {
		t.Skip("stty is unavailable")
	}
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("PTY is unavailable: %v", err)
	}
	defer func() { _ = master.Close(); _ = slave.Close() }()
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "review")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nstty raw -echo\nexit 130\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = reviewLint(t.Context(), Runtime{Input: slave, Error: slave}, script, []byte("original"), []byte("proposed"))
	if err == nil {
		t.Fatal("cancel was accepted")
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored")
	}
}

func TestLintCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := reviewLint(ctx, Runtime{Input: strings.NewReader(""), Error: io.Discard}, "false", []byte("original"), []byte("proposed"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
