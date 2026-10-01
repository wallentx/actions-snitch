package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/googleai"
	"github.com/tmc/langchaingo/llms/ollama"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/wallentx/actions-snitch/internal/config"
)

// NewProvider constructs a direct API adapter with explicit environment credentials.
// It never changes the process environment or invokes a provider executable.
func NewProvider(ctx context.Context, c config.AI, lookup config.Lookup) (llms.Model, string, error) {
	if strings.TrimSpace(c.Model) == "" {
		return nil, "", fmt.Errorf("AI analysis requires ai.model or ACTIONS_SNITCH_AI_MODEL")
	}
	if c.Effort != "" {
		if c.Provider != "anthropic" {
			return nil, "", fmt.Errorf("the pinned %s adapter cannot honor ai.effort; remove it to use the API default", c.Provider)
		}
		switch c.Effort {
		case "low", "medium", "high":
		default:
			return nil, "", fmt.Errorf("unsupported Anthropic effort %q; use low, medium, high, or the API default", c.Effort)
		}
	}
	get := func(name string) string { v, _ := lookup(name); return v }
	client := &http.Client{Timeout: 2 * time.Minute, Transport: responseTransport{base: http.DefaultTransport, ollama: c.Provider == "ollama"}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var model llms.Model
	var err error
	var endpoint string
	switch c.Provider {
	case "openai", "openrouter":
		keyName, baseName := "OPENAI_API_KEY", "OPENAI_BASE_URL"
		endpoint = "https://api.openai.com/v1"
		if c.Provider == "openrouter" {
			keyName, baseName = "OPENROUTER_API_KEY", "OPENROUTER_BASE_URL"
			endpoint = "https://openrouter.ai/api/v1"
		}
		if get(keyName) == "" {
			return nil, "", fmt.Errorf("%s environment variable is not set", keyName)
		}
		if v := get(baseName); v != "" {
			endpoint = v
		}
		if err := validateEndpoint(endpoint); err != nil {
			return nil, "", err
		}
		if c.Effort != "" {
			return nil, "", fmt.Errorf("the pinned langchaingo %s adapter cannot honor ai.effort; remove it to use the API default", c.Provider)
		}
		model, err = openai.New(openai.WithToken(get(keyName)), openai.WithModel(c.Model), openai.WithBaseURL(endpoint), openai.WithHTTPClient(client))
	case "anthropic":
		if get("ANTHROPIC_API_KEY") == "" {
			return nil, "", fmt.Errorf("ANTHROPIC_API_KEY environment variable is not set")
		}
		endpoint = "https://api.anthropic.com"
		if v := get("ANTHROPIC_BASE_URL"); v != "" {
			endpoint = v
		}
		if err := validateEndpoint(endpoint); err != nil {
			return nil, "", err
		}
		model, err = anthropic.New(anthropic.WithToken(get("ANTHROPIC_API_KEY")), anthropic.WithModel(c.Model), anthropic.WithBaseURL(endpoint), anthropic.WithHTTPClient(client))
	case "gemini":
		key := get("GEMINI_API_KEY")
		if key == "" {
			key = get("GOOGLE_API_KEY")
		}
		if key == "" {
			return nil, "", fmt.Errorf("GEMINI_API_KEY or GOOGLE_API_KEY environment variable is not set")
		}
		endpoint = "https://generativelanguage.googleapis.com"
		// The Google SDK applies its own API-key transport; the call context
		// supplies the timeout and cancellation rather than replacing that transport.
		model, err = googleai.New(ctx, googleai.WithAPIKey(key), googleai.WithDefaultModel(c.Model), googleai.WithDefaultMaxTokens(8192))
	case "ollama":
		endpoint = get("OLLAMA_HOST")
		if endpoint == "" {
			endpoint = "http://127.0.0.1:11434"
		}
		if err := validateEndpoint(endpoint); err != nil {
			return nil, "", err
		}
		model, err = ollama.New(ollama.WithModel(c.Model), ollama.WithServerURL(endpoint), ollama.WithHTTPClient(client))
	default:
		return nil, "", fmt.Errorf("unsupported API provider %q", c.Provider)
	}
	if err != nil {
		return nil, "", fmt.Errorf("could not initialize %s API adapter", c.Provider)
	}
	if c.Effort != "" {
		reasoning, ok := model.(llms.ReasoningModel)
		if !ok || !reasoning.SupportsReasoning() {
			_ = Close(model)
			return nil, "", fmt.Errorf("the pinned adapter cannot honor effort for model %q; remove ai.effort or use a supported thinking model", c.Model)
		}
	}
	return model, endpoint, nil
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("provider endpoint must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}

func Close(model any) error {
	if closer, ok := model.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
