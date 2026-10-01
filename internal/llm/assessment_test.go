package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
	"github.com/wallentx/actions-snitch/internal/cache"
	"github.com/wallentx/actions-snitch/internal/config"
	"github.com/wallentx/actions-snitch/internal/github"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/workflow"
)

const validAssessment = `{"decision":"allow","confidence":"high","summary":"Inputs are compatible.","findings":[],"remediations":[]}`

func TestStrictAssessmentSchema(t *testing.T) {
	if _, err := Validate([]byte(validAssessment)); err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		`null`, `{}`, strings.Replace(validAssessment, `"high"`, `"low"`, 1),
		strings.Replace(validAssessment, `"findings":[]`, `"findings":null`, 1),
		strings.Replace(validAssessment, `"summary":`, `"unknown":true,"summary":`, 1),
		strings.Replace(validAssessment, `"decision":"allow"`, `"decision":"block","decision":"allow"`, 1),
		validAssessment + ` {}`, strings.Replace(validAssessment, `"Inputs are compatible."`, `"`+strings.Repeat("x", 601)+`"`, 1),
		strings.Replace(validAssessment, `"remediations":[]`, `"remediations":[{"file":"x","line":1.5,"input":"mode","operation":"set","current_value":"x","new_value":"y","reason":"z"}]`, 1),
	}
	for _, data := range invalid {
		if _, err := Validate([]byte(data)); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

type fakeModel struct {
	calls    int
	response string
	err      error
	t        *testing.T
}

func (f *fakeModel) GenerateContent(_ context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	f.calls++
	var opts llms.CallOptions
	for _, option := range options {
		option(&opts)
	}
	if len(opts.Tools) > 0 || len(opts.Functions) > 0 {
		f.t.Error("model has tool authority")
	}
	if len(messages) != 2 {
		f.t.Error("missing instruction/data boundary")
	}
	if f.err != nil {
		return nil, f.err
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: f.response, StopReason: "stop"}}}, nil
}

func TestAssessmentCacheAndFailures(t *testing.T) {
	fake := &fakeModel{response: validAssessment, t: t}
	store := &cache.Store{Dir: t.TempDir()}
	s := Service{Model: fake, Config: config.AI{Provider: "openai", Model: "fixture"}, Cache: store, Endpoint: "fixture"}
	if got := s.Assess(context.Background(), []byte(`{"evidence":"safe"}`)); got.Decision != "allow" {
		t.Fatal(got)
	}
	if got := s.Assess(context.Background(), []byte(`{"evidence":"safe"}`)); got.Decision != "allow" || fake.calls != 1 {
		t.Fatal("cache missed")
	}
	s.Endpoint = "different"
	fake.err = errors.New("secret-provider-diagnostic")
	got := s.Assess(context.Background(), []byte(`{"evidence":"safe"}`))
	if got.Decision != "review" || strings.Contains(got.Summary, "secret-provider-diagnostic") {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := fake.calls
	if s.Assess(ctx, []byte(`{}`)).Decision != "review" || fake.calls != before {
		t.Fatal("cancelled model invoked")
	}
}

func TestOpenAIAdapterUsesDirectAPI(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("missing explicit credential")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["tools"] != nil || request["functions"] != nil {
			t.Error("request grants tools")
		}
		if request["model"] != "fixture-model" {
			t.Error(request["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": validAssessment}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	values := map[string]string{"OPENAI_API_KEY": "fixture-key", "OPENAI_BASE_URL": server.URL + "/v1"}
	c := config.AI{Provider: "openai", Model: "fixture-model"}
	provider, endpoint, err := NewProvider(context.Background(), c, func(k string) (string, bool) { v, ok := values[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := Close(provider); err != nil {
			t.Error(err)
		}
	}()
	s := Service{Model: provider, Config: c, Endpoint: endpoint}
	if result := s.Assess(context.Background(), []byte(`{"test":true}`)); result.Decision != "allow" || calls != 1 {
		t.Fatalf("%+v calls=%d", result, calls)
	}
	c.Effort = "high"
	if _, _, err := NewProvider(context.Background(), c, func(k string) (string, bool) { v, ok := values[k]; return v, ok }); err == nil {
		t.Fatal("ignored effort accepted")
	}
}

func TestRedaction(t *testing.T) {
	input := map[string]any{"env": map[string]any{"SAFE": "private-env"}, "with": map[string]any{"githubToken": "private-token", "SSHPrivateKey": "private-key", "safe": "retain", "expression": "${{ toJSON(secrets) }}"}, "nested": []any{map[string]any{"with": map[string]any{"apiKey": "private-api"}, "env": map[string]any{"x": "private-other"}}}}
	b, err := json.Marshal(Sanitize(input))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "private-") || strings.Contains(string(b), "toJSON") || !strings.Contains(string(b), "retain") {
		t.Fatalf("unsafe redaction: %s", b)
	}
}

func TestAnthropicThinkingAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["temperature"] != float64(1) || request["thinking"] == nil {
			t.Errorf("thinking request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "type": "message", "role": "assistant", "model": "fixture", "stop_reason": "end_turn", "usage": map[string]int{"input_tokens": 5, "output_tokens": 10}, "content": []any{map[string]any{"type": "thinking", "thinking": "internal reasoning", "signature": "fixture"}, map[string]any{"type": "text", "text": validAssessment}}})
	}))
	defer server.Close()
	values := map[string]string{"ANTHROPIC_API_KEY": "fixture-key", "ANTHROPIC_BASE_URL": server.URL}
	c := config.AI{Provider: "anthropic", Model: "claude-sonnet-4-5", Effort: "high"}
	provider, endpoint, err := NewProvider(context.Background(), c, func(k string) (string, bool) { v, ok := values[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	s := Service{Model: provider, Config: c, Endpoint: endpoint}
	if result := s.Assess(context.Background(), []byte(`{}`)); result.Decision != "allow" {
		t.Fatal(result)
	}
}

func TestOllamaDroppedMetadataFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		reason string
		tools  bool
		want   string
	}{{"stop", false, "allow"}, {"length", false, "review"}, {"stop", true, "review"}, {"length", true, "review"}} {
		t.Run(fmt.Sprintf("%s-%t", tc.reason, tc.tools), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				message := map[string]any{"role": "assistant", "content": validAssessment}
				if tc.tools {
					message["tool_calls"] = []any{map[string]any{"function": map[string]string{"name": "exec", "arguments": "{}"}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "done": true, "done_reason": tc.reason, "message": message})
			}))
			defer server.Close()
			c := config.AI{Provider: "ollama", Model: "fixture"}
			provider, endpoint, err := NewProvider(context.Background(), c, func(k string) (string, bool) { return server.URL + "/ollama", k == "OLLAMA_HOST" })
			if err != nil {
				t.Fatal(err)
			}
			s := Service{Model: provider, Config: c, Endpoint: endpoint}
			if result := s.Assess(context.Background(), []byte(`{}`)); result.Decision != tc.want {
				t.Fatalf("got %+v want %s", result, tc.want)
			}
		})
	}
}

func TestEvidenceRequiredDefinitionsAndImplementation(t *testing.T) {
	workflowBytes, err := os.ReadFile("../../.github/scripts/fixtures/promci-approve-workflows.yml")
	if err != nil {
		t.Fatal(err)
	}
	file, err := workflow.Parse(".github/workflows/approve.yml", workflowBytes)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := os.ReadFile("../../.github/scripts/fixtures/promci-approve-action.yml")
	if err != nil {
		t.Fatal(err)
	}
	missing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "action.yml") && !missing {
			_, _ = fmt.Fprint(w, string(definition))
			return
		}
		if strings.HasSuffix(r.URL.Path, "approve-workflows.sh") {
			_, _ = fmt.Fprint(w, "# This evidence is never executed.\nexit 99")
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := github.New("github.com", "", nil)
	client.APIBase = server.URL + "/"
	client.RawBase = server.URL + "/raw"
	client.HTTP = server.Client()
	client.PublicHTTP = server.Client()
	u := file.Usages[0]
	finding := model.Finding{Action: u.Action, Repository: u.Repository, Current: u.Current, Latest: "1.0.0", CompatibilityScore: "Unknown"}
	b, err := Evidence(context.Background(), client, []*workflow.File{file}, finding, "never")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "approve-workflows.sh") || !strings.Contains(string(b), "<redacted>") && !strings.Contains(string(b), `\u003credacted\u003e`) {
		t.Fatalf("missing bounded evidence: %s", b)
	}
	missing = true
	if _, err := Evidence(context.Background(), client, []*workflow.File{file}, finding, "never"); err == nil {
		t.Fatal("missing required definitions allowed")
	}
}
