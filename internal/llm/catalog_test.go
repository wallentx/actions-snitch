package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestProviderCatalogs(t *testing.T) {
	cases := []struct {
		provider, path, authHeader, authValue, response string
		env                                             map[string]string
		want                                            []string
	}{
		{"openai", "/v1/models", "Authorization", "Bearer fixture", `{"data":[{"id":"gpt-chat"},{"id":"text-embedding-3"},{"id":"gpt-chat"}]}`, map[string]string{"OPENAI_API_KEY": "fixture"}, []string{"gpt-chat"}},
		{"openai", "/custom/models", "Authorization", "Bearer fixture", `{"data":[{"id":"custom-chat"}]}`, map[string]string{"OPENAI_API_KEY": "fixture", "OPENAI_BASE_URL": "https://fixture.invalid/custom"}, []string{"custom-chat"}},
		{"anthropic", "/v1/models", "x-api-key", "fixture", `{"data":[{"id":"claude-chat","display_name":"Claude Chat"}]}`, map[string]string{"ANTHROPIC_API_KEY": "fixture"}, []string{"claude-chat"}},
		{"gemini", "/v1beta/models", "x-goog-api-key", "fixture", `{"models":[{"name":"models/gemini-chat","supportedGenerationMethods":["generateContent"]},{"name":"models/embedding","supportedGenerationMethods":["embedContent"]}]}`, map[string]string{"GOOGLE_API_KEY": "fixture"}, []string{"gemini-chat"}},
		{"openrouter", "/api/v1/models/user", "Authorization", "Bearer fixture", `{"data":[{"id":"owner/chat","architecture":{"output_modalities":["text"]}},{"id":"owner/image","architecture":{"output_modalities":["image"]}}]}`, map[string]string{"OPENROUTER_API_KEY": "fixture"}, []string{"owner/chat"}},
		{"ollama", "/local/api/tags", "", "", `{"models":[{"name":"local-chat:latest"}]}`, map[string]string{"OLLAMA_HOST": "http://fixture.invalid/local"}, []string{"local-chat:latest"}},
	}
	for _, tc := range cases {
		t.Run(tc.provider+tc.path, func(t *testing.T) {
			catalog := Catalog{Lookup: func(key string) (string, bool) { value, ok := tc.env[key]; return value, ok }, HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tc.path || r.Header.Get(tc.authHeader) != tc.authValue {
					t.Errorf("unexpected catalog request path/header: %s", r.URL.Path)
				}
				if strings.Contains(r.URL.RawQuery, "fixture") {
					t.Error("credential appeared in URL")
				}
				if tc.provider == "anthropic" && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing Anthropic version")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.response))}, nil
			})}}
			models, err := catalog.Models(t.Context(), tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, model := range models {
				got = append(got, model.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%v != %v", got, tc.want)
			}
		})
	}
}

func TestCatalogPaginationAndCancellation(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			catalog := Catalog{Lookup: func(string) (string, bool) { return "fixture", true }, HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := ""
				if provider == "anthropic" {
					if calls == 1 {
						body = `{"data":[{"id":"a"}],"has_more":true,"last_id":"a"}`
					} else {
						if r.URL.Query().Get("after_id") != "a" {
							t.Error("missing Anthropic page cursor")
						}
						body = `{"data":[{"id":"b"}]}`
					}
				} else {
					if calls == 1 {
						body = `{"models":[{"name":"models/a","supportedGenerationMethods":["generateContent"]}],"nextPageToken":"next"}`
					} else {
						if r.URL.Query().Get("pageToken") != "next" {
							t.Error("missing Gemini page cursor")
						}
						body = `{"models":[{"name":"models/b","supportedGenerationMethods":["generateContent"]}]}`
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			// Endpoint overrides must remain unset while fixture credentials are available.
			catalog.Lookup = func(key string) (string, bool) {
				if strings.HasSuffix(key, "API_KEY") {
					return "fixture", true
				}
				return "", false
			}
			models, err := catalog.Models(t.Context(), provider)
			if err != nil || len(models) != 2 || calls != 2 {
				t.Fatalf("%v %v calls=%d", models, err, calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	catalog := Catalog{Lookup: func(key string) (string, bool) { return "", false }, HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}}
	if _, err := catalog.Models(ctx, "ollama"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCatalogErrorsDoNotLeakCredentials(t *testing.T) {
	secret := "fixture-secret"
	catalog := Catalog{Lookup: func(key string) (string, bool) {
		if key == "OPENAI_API_KEY" {
			return secret, true
		}
		return "", false
	}, HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(secret))}, nil
	})}}
	if _, err := catalog.Models(t.Context(), "openai"); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe catalog error: %v", err)
	}
}
