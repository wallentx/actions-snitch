//go:build linux || darwin

package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/wallentx/actions-snitch/internal/config"
	"github.com/wallentx/actions-snitch/internal/llm"
	"golang.org/x/term"
)

func TestSetupTerminalHelper(t *testing.T) {
	if os.Getenv("SNITCH_SETUP_HELPER") != "1" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	catalog := func(ctx context.Context, provider string) ([]llm.ModelInfo, error) {
		if os.Getenv("SNITCH_SETUP_HOLD") == "1" {
			_, _ = fmt.Fprintln(os.Stderr, "Fixture catalog started")
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []llm.ModelInfo{{ID: "gpt-alpha", Name: "Alpha"}, {ID: "gpt-beta", Name: "Beta"}}, nil
	}
	code := Run(ctx, []string{"-c"}, Runtime{Interactive: true, Catalog: catalog})
	cancel()
	os.Exit(code)
}

type setupTerminal struct {
	command  *exec.Cmd
	terminal *os.File
	chunks   chan string
	done     chan error
	text     string
	path     string
	before   *term.State
}

func newSetupTerminal(t *testing.T, hold bool) *setupTerminal {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	command := exec.CommandContext(t.Context(), binary, "-test.run=^TestSetupTerminalHelper$")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "TERM=xterm-256color", "COLORFGBG=15;0", "SNITCH_SETUP_HELPER=1", "ACTIONS_SNITCH_CONFIG=" + configPath}
	if hold {
		command.Env = append(command.Env, "SNITCH_SETUP_HOLD=1")
	}
	if coverage := flag.Lookup("test.gocoverdir"); coverage != nil && coverage.Value.String() != "" {
		command.Env = append(command.Env, "GOCOVERDIR="+coverage.Value.String())
	}
	terminal, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	defer func() { _ = slave.Close() }()
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	s := &setupTerminal{command: command, terminal: terminal, chunks: make(chan string, 128), done: make(chan error, 1), path: configPath, before: before}
	go func() { s.done <- command.Wait(); close(s.done) }()
	go func() {
		defer close(s.chunks)
		buffer := make([]byte, 8192)
		pending := ""
		for {
			n, err := terminal.Read(buffer)
			if n > 0 {
				// A PTY transports bytes but does not answer terminal queries.
				// Emulate the background/cursor replies supplied by real terminals.
				pending += string(buffer[:n])
				for _, query := range []struct{ request, response string }{{"\x1b]11;?", "\x1b]11;rgb:0000/0000/0000\x1b\\"}, {"\x1b[6n", "\x1b[1;1R"}} {
					if strings.Contains(pending, query.request) {
						_, _ = terminal.WriteString(query.response)
						pending = strings.ReplaceAll(pending, query.request, "")
					}
				}
				if len(pending) > 64 {
					pending = pending[len(pending)-64:]
				}
				select {
				case s.chunks <- string(buffer[:n]):
				case <-t.Context().Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = command.Process.Kill(); <-s.done; _ = terminal.Close() })
	return s
}

func (s *setupTerminal) wait(t *testing.T, text string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !strings.Contains(s.text, text) {
		select {
		case chunk, ok := <-s.chunks:
			if !ok {
				t.Fatalf("terminal exited before %q: %s", text, s.text)
			}
			s.text += chunk
		case <-timer.C:
			t.Fatalf("terminal did not render %q: %s", text, s.text)
		}
	}
}

func (s *setupTerminal) send(t *testing.T, text string) {
	t.Helper()
	if _, err := s.terminal.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func (s *setupTerminal) finish(t *testing.T, code int) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("setup did not exit promptly")
	}
	if got := s.command.ProcessState.ExitCode(); got != code {
		t.Fatalf("exit %d, want %d: %s", got, code, s.text)
	}
	after, err := term.GetState(int(s.terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, s.before) {
		t.Fatal("terminal mode was not restored")
	}
}

func (s *setupTerminal) chooseAI(t *testing.T) {
	t.Helper()
	s.wait(t, "Should AI investigate")
	s.send(t, "\x1b[B\r")
	s.wait(t, "Which provider should")
	s.send(t, "\r")
}

func TestSetupTerminalCancellation(t *testing.T) {
	for _, stage := range []string{"key", "signal", "catalog", "model-menu"} {
		t.Run(stage, func(t *testing.T) {
			s := newSetupTerminal(t, stage == "catalog")
			s.wait(t, "Should AI investigate")
			if stage == "catalog" || stage == "model-menu" {
				s.chooseAI(t)
				if stage == "catalog" {
					s.wait(t, "Fixture catalog started")
				} else {
					s.wait(t, "Which model should")
				}
			}
			if stage == "signal" {
				if err := s.command.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			} else {
				s.send(t, "\x03")
			}
			s.finish(t, 130)
			if _, err := os.Stat(s.path); !os.IsNotExist(err) {
				t.Fatal("cancelled setup wrote config")
			}
		})
	}
}

func TestSetupTerminalModelSelection(t *testing.T) {
	s := newSetupTerminal(t, false)
	s.chooseAI(t)
	s.wait(t, "Which model should")
	s.wait(t, "gpt-beta")
	s.send(t, "\x1b[B\r")
	s.wait(t, "Below which score")
	s.send(t, "\r\r")
	s.wait(t, "Save this configuration?")
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatal("setup saved before confirmation")
	}
	s.send(t, "\r")
	s.finish(t, 0)
	cfg, err := config.Load(s.path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AI.Enabled || cfg.AI.Provider != "openai" || cfg.AI.Model != "gpt-beta" {
		t.Fatalf("wrong selected configuration: %+v", cfg)
	}
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v %v", info, err)
	}
}
