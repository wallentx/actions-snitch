package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// responseTransport bounds provider responses and checks metadata that the
// pinned Ollama adapter otherwise discards before returning ContentChoice.
type responseTransport struct {
	base   http.RoundTripper
	ollama bool
}

func (t responseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, errors.New("provider response exceeds size limit")
	}
	if t.ollama && strings.HasSuffix(request.URL.Path, "/api/chat") && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := validateOllamaEnvelope(b); err != nil {
			return nil, err
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(b))
	return response, nil
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
