package output

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeTerminfoFixture(t *testing.T, dir, name, foreground string) {
	t.Helper()
	if _, err := exec.LookPath("tic"); err != nil {
		t.Skip("terminfo compiler unavailable")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "terminal.src")
	entry := fmt.Sprintf("%s|Actions Snitch test terminal,\n\tcolors#8,\n\tsetaf=%s,\n\tsgr0=\\E[0m,\n", name, foreground)
	if err := os.WriteFile(source, []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(t.Context(), "tic", "-o", dir, source).CombinedOutput(); err != nil {
		t.Fatalf("compile terminfo fixture: %v\n%s", err, output)
	}
}

func TestTerminalStylePrefixFallback(t *testing.T) {
	prefix := t.TempDir()
	name := "snitch-prefix-colors"
	writeTerminfoFixture(t, filepath.Join(prefix, "share", "terminfo"), name, `\E[3%p1%dm`)
	t.Setenv("PREFIX", prefix)
	t.Setenv("TERMINFO", t.TempDir())
	t.Setenv("TERMINFO_DIRS", "")
	if got := TerminalStyle(name).Wrap("success", "value"); got != "\x1b[32mvalue\x1b[0m" {
		t.Fatalf("prefix terminfo colors: %q", got)
	}
	for _, missing := range []string{"", "snitch-no-such-terminal"} {
		if got := TerminalStyle(missing).Wrap("success", "value"); got != "value" {
			t.Fatalf("missing terminal %q should remain unstyled: %q", missing, got)
		}
	}
}

func TestTerminalStyleExplicitDatabasePrecedesPrefix(t *testing.T) {
	prefix, explicit := t.TempDir(), t.TempDir()
	name := "snitch-explicit-colors"
	writeTerminfoFixture(t, filepath.Join(prefix, "share", "terminfo"), name, "prefix-")
	writeTerminfoFixture(t, explicit, name, "explicit-")
	t.Setenv("PREFIX", prefix)
	t.Setenv("TERMINFO", explicit)
	if got := TerminalStyle(name).Wrap("success", "value"); got != "explicit-value\x1b[0m" {
		t.Fatalf("explicit terminfo database lost precedence: %q", got)
	}
}

func TestTerminfoMatchesTput(t *testing.T) {
	if _, err := exec.LookPath("tput"); err != nil {
		t.Skip("tput oracle unavailable")
	}
	style := TerminalStyle("xterm")
	for class, color := range map[string]string{"success": "2", "warning": "3", "info": "7", "section": "6", "status": "8"} {
		prefix, err := exec.CommandContext(t.Context(), "tput", "-T", "xterm", "setaf", color).Output()
		if err != nil {
			t.Skip("xterm terminfo unavailable")
		}
		reset, err := exec.CommandContext(t.Context(), "tput", "-T", "xterm", "sgr0").Output()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := style.Wrap(class, "value"), string(prefix)+"value"+string(reset); got != want {
			t.Errorf("%s: %q != %q", class, got, want)
		}
	}
}

type spinnerWriter struct {
	bytes.Buffer
	once    sync.Once
	started chan struct{}
}

func (w *spinnerWriter) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	w.once.Do(func() { close(w.started) })
	return n, err
}

func TestSpinnerRestoresCursorOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &spinnerWriter{started: make(chan struct{})}
	style := Style{HideCursor: "<hide>", ShowCursor: "<show>"}
	stop := StartSpinner(ctx, w, style, true)
	<-w.started
	cancel()
	stop()
	stop()
	text := w.String()
	if !strings.HasPrefix(text, "<hide>") || !strings.HasSuffix(text, "\x1b[2K\r<show>\r") {
		t.Fatalf("cursor lifecycle: %q", text)
	}
}

func TestColoredHelpParity(t *testing.T) {
	style := TerminalStyle("xterm")
	if style.reset == "" {
		t.Skip("xterm terminfo unavailable")
	}
	got := style.Usage("Usage: example\nOptions:\n  -h Help\n\n")
	if !strings.HasPrefix(got, style.bold+style.colors[5]) || !strings.Contains(got, style.colors[2]+"  -h Help") {
		t.Fatal("help lost its colors")
	}
}

func (w *spinnerWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
