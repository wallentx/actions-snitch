package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/tmc/langchaingo/llms"
	"github.com/wallentx/actions-snitch/internal/diagnostic"
	"google.golang.org/api/googleapi"
)

type failureKey struct{}
type requestFailure struct {
	mu  sync.Mutex
	err error
}

// Some adapters flatten transport errors into strings. Preserve the original
// cause per call so diagnostics never need to parse or print those strings.
type diagnosticModel struct{ llms.Model }

func (m diagnosticModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	failure := &requestFailure{}
	response, err := m.Model.GenerateContent(context.WithValue(ctx, failureKey{}, failure), messages, options...)
	if response != nil {
		diagnostic.Printf(ctx, "model response choices=%d", len(response.Choices))
		for i, choice := range response.Choices {
			if choice == nil {
				continue
			}
			finish := strings.ToLower(choice.StopReason)
			switch finish {
			case "", "stop", "end_turn", "length", "max_tokens", "content_filter", "tool_calls", "function_call", "stop_sequence", "pause_turn", "refusal", "safety", "finishreasonstop", "finishreasonmaxtokens", "finishreasonsafety":
			default:
				finish = "unrecognized"
			}
			diagnostic.Printf(ctx, "model choice=%d stop_reason=%q response_bytes=%d", i, finish, len(choice.Content))
			for _, key := range []string{"PromptTokens", "CompletionTokens", "ReasoningTokens"} {
				if count, ok := choice.GenerationInfo[key].(int); ok {
					diagnostic.Printf(ctx, "model choice=%d %s=%d", i, key, count)
				}
			}
		}
	}
	if err != nil {
		failure.mu.Lock()
		defer failure.mu.Unlock()
		if failure.err != nil {
			err = failure.err
		}
	}
	return response, err
}

func (m diagnosticModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, m, prompt, options...)
}

func (m diagnosticModel) Close() error { return Close(m.Model) }

func (m diagnosticModel) SupportsReasoning() bool {
	r, ok := m.Model.(llms.ReasoningModel)
	return ok && r.SupportsReasoning()
}

func rememberFailure(ctx context.Context, err error) {
	if failure, ok := ctx.Value(failureKey{}).(*requestFailure); ok && err != nil {
		failure.mu.Lock()
		failure.err = err
		failure.mu.Unlock()
	}
}

// Provider messages can echo credentials or prompts. Only known diagnostic
// identifiers cross this boundary; arbitrary response text stays private.
type providerHTTPError struct {
	status    int
	code      string
	parameter string
}

func (e *providerHTTPError) Error() string {
	s := fmt.Sprintf("provider returned HTTP %d", e.status)
	if e.code != "" {
		s += " (" + e.code + ")"
	}
	if e.parameter != "" {
		s += "; parameter=" + e.parameter
	}
	return s
}

func newProviderHTTPError(status int, body []byte) *providerHTTPError {
	e := &providerHTTPError{status: status}
	var envelope struct {
		Error struct {
			Code   string `json:"code"`
			Type   string `json:"type"`
			Param  string `json:"param"`
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return e
	}
	for _, code := range []string{envelope.Error.Code, envelope.Error.Type, envelope.Error.Status} {
		switch code {
		case "invalid_api_key", "authentication_error", "permission_error", "model_not_found", "insufficient_quota", "rate_limit_exceeded", "rate_limit_error", "context_length_exceeded", "unsupported_parameter", "unsupported_value", "invalid_request_error", "not_found_error", "overloaded_error", "api_error", "billing_hard_limit_reached", "INVALID_ARGUMENT", "UNAUTHENTICATED", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "NOT_FOUND", "UNAVAILABLE":
			e.code = code
		}
		if e.code != "" {
			break
		}
	}
	switch envelope.Error.Param {
	case "model", "temperature", "top_p", "top_logprobs", "logprobs", "max_tokens", "max_completion_tokens", "max_output_tokens", "response_format", "messages", "reasoning_effort", "thinking", "tools":
		e.parameter = envelope.Error.Param
	}
	return e
}

// ProviderFailure returns a shareable failure summary without raw API bodies,
// URLs, request content, or error strings that might contain credentials.
func ProviderFailure(err error) string {
	if errors.Is(err, context.Canceled) {
		return "The model request was cancelled."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The model request timed out."
	}
	var apiErr *providerHTTPError
	var googleErr *googleapi.Error
	if !errors.As(err, &apiErr) && errors.As(err, &googleErr) {
		apiErr = newProviderHTTPError(googleErr.Code, []byte(googleErr.Body))
	}
	if apiErr != nil {
		hint := "Check the provider's request requirements."
		switch apiErr.status {
		case http.StatusUnauthorized:
			hint = "Check the API credential used by this shell."
		case http.StatusForbidden:
			hint = "Check the API project's permissions and model access."
		case http.StatusNotFound:
			hint = "Check the model ID and provider endpoint."
		case http.StatusTooManyRequests:
			hint = "Check the provider's quota and rate limits."
		default:
			if apiErr.status >= 500 {
				hint = "The provider is unavailable; retry later."
			}
		}
		return "The model request failed: " + apiErr.Error() + ". " + hint
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "The model request timed out."
		}
		return "The model request failed to connect; check DNS, network, TLS, and proxy settings."
	}
	return "The model request failed before an assessment was returned; use --debug for request metadata."
}
