package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wallentx/actions-snitch/internal/config"
	"github.com/wallentx/actions-snitch/internal/diagnostic"
)

func TestReasoningRequestOmitsSampling(t *testing.T) {
	for _, name := range []string{"gpt-6.1-sol", "gpt-6-astra", "fixture-model"} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				_, sampling := request["temperature"]
				if want := name == "fixture-model"; sampling != want {
					t.Errorf("temperature present=%t want=%t", sampling, want)
				}
				if request["max_completion_tokens"] == nil || request["response_format"] == nil || request["messages"] == nil {
					t.Error("normalization lost required request fields")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": validAssessment}, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			settings := map[string]string{"OPENAI_API_KEY": "private-api-key", "OPENAI_BASE_URL": server.URL + "/v1"}
			cfg := config.AI{Provider: "openai", Model: name}
			ctx := diagnostic.WithWriter(t.Context(), &logs)
			provider, endpoint, err := NewProvider(ctx, cfg, func(k string) (string, bool) { v, ok := settings[k]; return v, ok })
			if err != nil {
				t.Fatal(err)
			}
			s := Service{Model: provider, Endpoint: endpoint, Config: cfg}
			if result := s.Assess(ctx, []byte(`{"evidence":"private-workflow"}`)); result.Decision != "allow" {
				t.Fatal(result)
			}
			for _, want := range []string{"provider=\"openai\"", "status=200", "elapsed="} {
				if !strings.Contains(logs.String(), want) {
					t.Fatalf("missing %q: %s", want, logs.String())
				}
			}
			for _, secret := range []string{"private-api-key", "private-workflow", validAssessment} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("debug output leaked a payload or credential")
				}
			}
		})
	}
}

func TestProviderHTTPDiagnostics(t *testing.T) {
	for _, providerName := range []string{"openai", "anthropic", "openrouter", "ollama"} {
		t.Run(providerName, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"error":{"code":"unsupported_value","type":"invalid_request_error","param":"temperature","message":"private-key private-prompt"}}`)
			}))
			defer server.Close()
			var logs bytes.Buffer
			ctx := diagnostic.WithWriter(t.Context(), &logs)
			cfg := config.AI{Provider: providerName, Model: "fixture-model"}
			lookup := func(k string) (string, bool) {
				if strings.HasSuffix(k, "_API_KEY") {
					return "private-key", true
				}
				return server.URL, true
			}
			provider, endpoint, err := NewProvider(ctx, cfg, lookup)
			if err != nil {
				t.Fatal(err)
			}
			result := (Service{Model: provider, Config: cfg, Endpoint: endpoint}).Assess(ctx, []byte(`{"evidence":"private-prompt"}`))
			if result.Decision != "review" || !strings.Contains(result.Summary, "HTTP 400") || !strings.Contains(result.Summary, "parameter=temperature") {
				t.Fatal(result)
			}
			if strings.Contains(logs.String()+result.Summary, "private-") {
				t.Fatal("provider response leaked private content")
			}
		})
	}
}

func TestProviderFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		hint   string
	}{
		{http.StatusUnauthorized, "credential"}, {http.StatusForbidden, "permissions"},
		{http.StatusNotFound, "model ID"}, {http.StatusTooManyRequests, "quota"},
		{http.StatusServiceUnavailable, "unavailable"},
	} {
		err := newProviderHTTPError(tc.status, []byte(`{"error":{"code":"secret-code","type":"secret-type","param":"secret-param","message":"secret-body"}}`))
		message := ProviderFailure(err)
		if !strings.Contains(message, tc.hint) || strings.Contains(message, "secret") {
			t.Fatal(message)
		}
	}
	if !strings.Contains(ProviderFailure(context.DeadlineExceeded), "timed out") || !strings.Contains(ProviderFailure(context.Canceled), "cancelled") {
		t.Fatal("context errors lost their causes")
	}
}

func TestMalformedProviderRequestFailsWithoutSending(t *testing.T) {
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test/v1/chat/completions", strings.NewReader("invalid JSON"))
	if err != nil {
		t.Fatal(err)
	}
	transport := responseTransport{omitSampling: true, base: fixtureTransport(func(*http.Request) (*http.Response, error) {
		t.Error("invalid request reached transport")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	response, err := transport.RoundTrip(request)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err == nil {
		t.Fatal("invalid request was accepted")
	}
}
