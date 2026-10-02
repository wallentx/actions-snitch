package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/langchaingo/llms"
	gitops "github.com/wallentx/actions-snitch/internal/git"
	"github.com/wallentx/actions-snitch/internal/github"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/output"
)

type assessmentModel struct {
	response string
	calls    int
	prompt   string
}

func (m *assessmentModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.calls++
	for _, message := range messages {
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok {
				m.prompt += text.Text
			}
		}
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: m.response, StopReason: "stop"}}}, nil
}

func fixtureRuntime(t *testing.T, score string, verified, definitions bool) (Runtime, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github/workflows/main.yml"), []byte("steps:\n  - uses: owner/action@v1\n    env:\n      PRIVATE: hidden-env\n    with:\n      mode: old\n      githubToken: hidden-token\n"), 0640); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_, _ = fmt.Fprint(w, `{"tag_name":"v2"}`)
		case r.URL.Path == "/repos/owner/action":
			_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
		case r.URL.Path == "/badge":
			_, _ = fmt.Fprintf(w, "<title>compatibility: %s%%</title>", score)
		case strings.HasPrefix(r.URL.Path, "/raw/") && definitions:
			_, _ = fmt.Fprint(w, "name: Fixture\ninputs:\n  mode: {}\n")
		case strings.HasPrefix(r.URL.Path, "/marketplace/") && verified:
			_, _ = fmt.Fprint(w, "GitHub has manually verified the creator of the action as an official partner organization.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := github.New("github.com", "", nil)
	client.APIBase = server.URL + "/"
	client.RawBase = server.URL + "/raw/"
	client.WebBase = server.URL + "/"
	client.BadgeBase = server.URL + "/badge"
	client.HTTP = server.Client()
	client.PublicHTTP = server.Client()
	env := map[string]string{"HOME": root, "XDG_CONFIG_HOME": filepath.Join(root, "config"), "XDG_CACHE_HOME": filepath.Join(root, "cache"), "ACTIONS_SNITCH_AI": "true", "ACTIONS_SNITCH_AI_MODEL": "fixture"}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return Runtime{Dir: root, Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Input: strings.NewReader(""), Output: out, Error: errOut, Runner: gitops.Commands{}, Client: client}, out, errOut
}

func TestVerboseCurrentMajor(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		confirmations int
	}{
		{"verbose", []string{"-v"}, 2},
		{"quiet", nil, 0},
		{"structured", []string{"-v", "-o", "json"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, out, errOut := fixtureRuntime(t, "95", true, true)
			path := filepath.Join(r.Dir, ".github/workflows/main.yml")
			source := []byte("steps:\n  - uses: owner/action@v2\n  - uses: owner/action@v2\n")
			if err := os.WriteFile(path, source, 0640); err != nil {
				t.Fatal(err)
			}
			if code := Run(t.Context(), tc.args, r); code != 0 || errOut.Len() != 0 {
				t.Fatalf("code %d: %s", code, errOut.String())
			}
			if got := strings.Count(out.String(), "  ✅ owner/action is up-to-date (2)\n"); got != tc.confirmations {
				t.Fatalf("got %d confirmations, want %d:\n%s", got, tc.confirmations, out.String())
			}
			if strings.Contains(out.String(), "Findings in") || (tc.name == "structured" && out.Len() != 0) {
				t.Fatalf("current major produced a finding: %s", out.String())
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, source) {
				t.Fatalf("read-only scan modified workflow: %v", err)
			}
		})
	}
}

func TestAIUpdateSafetyIntegration(t *testing.T) {
	for _, tc := range []struct {
		name, decision                           string
		unsafe, definitions, force, verifiedOnly bool
		wantRef                                  string
		calls                                    int
	}{
		{"allow", "allow", false, true, false, false, "v2", 1},
		{"review", "review", false, true, false, false, "v1", 1},
		{"bad-remediation", "allow", true, true, false, false, "v1", 1},
		{"missing-evidence", "allow", false, false, false, false, "v1", 0},
		{"force-bypass", "allow", true, false, true, false, "v2", 0},
		{"creator-gate", "allow", false, true, true, true, "v1", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, errOut := fixtureRuntime(t, "20", false, tc.definitions)
			assessment := model.Assessment{Decision: tc.decision, Confidence: "high", Summary: "Fixture decision.", Findings: []model.Caution{}, Remediations: []model.Remediation{}}
			if tc.decision == "allow" {
				input, current := "mode", "old"
				if tc.unsafe {
					input, current = "githubToken", "hidden-token"
				}
				assessment.Remediations = append(assessment.Remediations, model.Remediation{File: ".github/workflows/main.yml", Line: 2, Input: input, Operation: "set", CurrentValue: current, NewValue: "safe", Reason: "Fixture change."})
			}
			encoded, err := json.Marshal(assessment)
			if err != nil {
				t.Fatal(err)
			}
			fake := &assessmentModel{response: string(encoded)}
			r.Model = fake
			args := []string{"-u", "-o", "json"}
			if tc.force {
				args = append(args, "-f")
			}
			if tc.verifiedOnly {
				args = append(args, "-t")
			}
			if code := Run(context.Background(), args, r); code != 0 {
				t.Fatalf("code %d: %s", code, errOut.String())
			}
			data, err := os.ReadFile(filepath.Join(r.Dir, ".github/workflows/main.yml"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "owner/action@"+tc.wantRef) || fake.calls != tc.calls {
				t.Fatalf("calls=%d source=%s", fake.calls, data)
			}
			if strings.Contains(fake.prompt, "hidden-env") || strings.Contains(fake.prompt, "hidden-token") {
				t.Fatal("secret sent to model")
			}
			if tc.force && strings.Contains(string(data), "mode: safe") {
				t.Fatal("force applied remediation")
			}
			if tc.name == "allow" && !strings.Contains(string(data), "mode: safe") {
				t.Fatal("remediation not applied")
			}
		})
	}
}

func TestSetupCancellationAndExclusiveCreation(t *testing.T) {
	for _, input := range []string{"n\n\n\n", "n\n"} {
		r, _, errOut := fixtureRuntime(t, "95", true, true)
		r.Input = strings.NewReader(input)
		code := Run(context.Background(), []string{"-c"}, r)
		path := filepath.Join(r.Dir, "config/actions-snitch/config.yaml")
		if input == "n\n" {
			if code == 0 {
				t.Fatal("EOF accepted")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("partial config written")
			}
			continue
		}
		if code != 0 {
			t.Fatal(errOut.String())
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("mode: %v %v", info, err)
		}
		if code := Run(context.Background(), []string{"-c"}, r); code == 0 {
			t.Fatal("overwrote config")
		}
	}
}

func TestForceDoesNotValidateProviderEffortOrModel(t *testing.T) {
	r, _, errOut := fixtureRuntime(t, "Unknown", false, false)
	lookup := r.Lookup
	r.Lookup = func(key string) (string, bool) {
		if key == "ACTIONS_SNITCH_AI_EFFORT" {
			return "legacy-ultra", true
		}
		if key == "ACTIONS_SNITCH_AI_MODEL" {
			return "", true
		}
		return lookup(key)
	}
	if code := Run(context.Background(), []string{"-uf", "-o", "json"}, r); code != 0 {
		t.Fatal(errOut.String())
	}
	data, err := os.ReadFile(filepath.Join(r.Dir, ".github/workflows/main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "owner/action@v2") {
		t.Fatal("force did not update")
	}
}

func TestQuietScanAnimationAndCI(t *testing.T) {
	style := output.TerminalStyle("xterm")
	if style.HideCursor == "" {
		t.Skip("xterm terminfo unavailable")
	}
	for _, ci := range []string{"false", "true"} {
		r, out, errOut := fixtureRuntime(t, "95", true, true)
		lookup := r.Lookup
		r.Lookup = func(key string) (string, bool) {
			if key == "TERM" {
				return "xterm", true
			}
			if key == "CI" {
				return ci, true
			}
			return lookup(key)
		}
		if code := Run(context.Background(), nil, r); code != 0 {
			t.Fatal(errOut.String())
		}
		text := out.String()
		hide := strings.Index(text, style.HideCursor)
		show := strings.Index(text, style.ShowCursor)
		if ci == "true" {
			if hide >= 0 || show >= 0 {
				t.Fatal("CI scan animated cursor")
			}
		} else if hide < 0 || show < hide || strings.Index(text, "Findings in") < show {
			t.Fatalf("invalid animation lifecycle: %q", text)
		}
		if !strings.Contains(text, "Finished.") {
			t.Fatal("scan did not finish")
		}
	}
}

func TestUnreadableLocalEvidenceFailsClosed(t *testing.T) {
	r, _, errOut := fixtureRuntime(t, "20", true, true)
	if err := os.WriteFile(filepath.Join(r.Dir, ".github/workflows/broken.yml"), []byte("steps: ["), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &assessmentModel{response: `{"decision":"allow","confidence":"high","summary":"safe","findings":[],"remediations":[]}`}
	r.Model = fake
	if code := Run(context.Background(), []string{"-u", "-o", "json"}, r); code != 0 {
		t.Fatal(errOut.String())
	}
	data, err := os.ReadFile(filepath.Join(r.Dir, ".github/workflows/main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 || !strings.Contains(string(data), "owner/action@v1") {
		t.Fatal("incomplete local evidence authorized an update")
	}
}

func TestSetupCancellationUnblocksInput(t *testing.T) {
	root := t.TempDir()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	started := make(chan struct{})
	r := Runtime{Dir: root, Input: &setupReadNotifier{Reader: reader, started: started}, Output: io.Discard, Error: &output, Lookup: func(key string) (string, bool) {
		if key == "ACTIONS_SNITCH_CONFIG" {
			return filepath.Join(root, "config.yaml"), true
		}
		return "", false
	}}
	done := make(chan int, 1)
	go func() { done <- Run(ctx, []string{"-c"}, r) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("setup did not start reading input")
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("exit=%d: %s", code, output.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled setup remained blocked on stdin")
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("cancelled setup wrote config")
	}
}

type setupReadNotifier struct {
	io.Reader
	started chan struct{}
	once    sync.Once
}

func (r *setupReadNotifier) Read(data []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.Reader.Read(data)
}
