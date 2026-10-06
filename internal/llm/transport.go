package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wallentx/actions-snitch/internal/diagnostic"
)

// responseTransport bounds provider responses and checks metadata that the
// pinned Ollama adapter otherwise discards before returning ContentChoice.
type responseTransport struct {
	base         http.RoundTripper
	ollama       bool
	omitSampling bool
}

func (t responseTransport) RoundTrip(request *http.Request) (_ *http.Response, failure error) {
	ctx := request.Context()
	defer func() { rememberFailure(ctx, failure) }()
	started := time.Now()
	if t.omitSampling && strings.HasSuffix(request.URL.Path, "/chat/completions") {
		var err error
		request, err = withoutSampling(request)
		if err != nil {
			return nil, err
		}
		diagnostic.Printf(ctx, "sampling parameters omitted for reasoning model")
	}
	diagnostic.Printf(ctx, "provider HTTP request method=%s host=%q path=%q bytes=%d", request.Method, request.URL.Host, request.URL.Path, request.ContentLength)
	response, err := t.base.RoundTrip(request)
	if err != nil {
		diagnostic.Printf(ctx, "provider HTTP failed elapsed=%s error_type=%T", time.Since(started).Round(time.Millisecond), err)
		return nil, err
	}
	diagnostic.Printf(ctx, "provider HTTP response status=%d elapsed=%s", response.StatusCode, time.Since(started).Round(time.Millisecond))
	b, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, errors.New("provider response exceeds size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := newProviderHTTPError(response.StatusCode, b)
		diagnostic.Printf(ctx, "%s", err)
		return nil, err
	}
	if t.ollama && strings.HasSuffix(request.URL.Path, "/api/chat") && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := validateOllamaEnvelope(b); err != nil {
			return nil, err
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(b))
	return response, nil
}

// The pinned OpenAI adapter inserts temperature=0 for models it does not
// recognize. GPT-6 reasoning requests must omit sampling parameters entirely.
func withoutSampling(request *http.Request) (*http.Request, error) {
	if request.Body == nil {
		return nil, errors.New("provider request has no body")
	}
	defer func() { _ = request.Body.Close() }()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body == nil {
		return nil, errors.New("provider request is not a JSON object")
	}
	for _, key := range []string{"temperature", "top_p", "top_logprobs", "logprobs"} {
		delete(body, key)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("provider request could not be encoded")
	}
	clone := request.Clone(request.Context())
	clone.Body = io.NopCloser(bytes.NewReader(b))
	clone.ContentLength = int64(len(b))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
	return clone, nil
}

func validateOllamaEnvelope(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	done := false
	count := 0
	for {
		var event struct {
			Done    bool   `json:"done"`
			Reason  string `json:"done_reason"`
			Error   string `json:"error"`
			Message struct {
				Tools    json.RawMessage `json:"tool_calls"`
				Function json.RawMessage `json:"function_call"`
			} `json:"message"`
		}
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("invalid Ollama response envelope")
		}
		if done {
			return errors.New("ollama returned data after completion")
		}
		count++
		if event.Error != "" || hasCall(event.Message.Tools) || hasCall(event.Message.Function) {
			return errors.New("ollama response contains an error or unauthorized tool request")
		}
		if event.Reason != "" && event.Reason != "stop" {
			return errors.New("ollama response did not complete normally")
		}
		done = event.Done
	}
	if count == 0 || !done {
		return errors.New("ollama response is incomplete")
	}
	return nil
}

func hasCall(raw json.RawMessage) bool {
	b := bytes.TrimSpace(raw)
	return len(b) > 0 && !bytes.Equal(b, []byte("null")) && !bytes.Equal(b, []byte("[]"))
}
