package output

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

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
