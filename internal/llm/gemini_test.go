package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms/googleai"
	"github.com/wallentx/actions-snitch/internal/config"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The real Google adapter consumes an array stream even for non-streaming chat.
// This test also exercises the gax decoder selected by the current Go toolchain.
func TestGeminiRealAdapterCompletion(t *testing.T) {
	for _, finish := range []string{"STOP", "MAX_TOKENS", "SAFETY"} {
		t.Run(finish, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": validAssessment}}}, "finishReason": finish, "index": 0}}, "usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1, "totalTokenCount": 2}})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request["systemInstruction"] == nil || request["tools"] != nil {
					t.Errorf("instruction/tool boundary: %+v", request)
				}
				data := string(body)
				if strings.Contains(r.URL.Path, "streamGenerateContent") {
					data = "[" + data + "]"
				} else {
					t.Errorf("expected actual chat stream, got %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
			})}
			provider, err := googleai.New(context.Background(), googleai.WithAPIKey("synthetic-key"), googleai.WithHTTPClient(client), googleai.WithDefaultModel("gemini-fixture"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			service := Service{Model: provider, Config: config.AI{Provider: "gemini", Model: "gemini-fixture"}}
			assessment := service.Assess(context.Background(), []byte(`{"evidence":"fixture"}`))
			want := "review"
			if finish == "STOP" {
				want = "allow"
			}
			if assessment.Decision != want || calls != 1 {
				t.Fatalf("got %+v calls=%d want %s", assessment, calls, want)
			}
		})
	}
}
