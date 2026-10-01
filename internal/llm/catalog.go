package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/wallentx/actions-snitch/internal/config"
)

type ModelInfo struct{ ID, Name string }

// Catalog uses provider APIs to discover models without running inference.
type Catalog struct {
	Lookup config.Lookup
	HTTP   *http.Client
}

func (c Catalog) Models(ctx context.Context, provider string) ([]ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	get := func(key string) string { value, _ := c.Lookup(key); return value }
	base, endpoint, keyName, header := "", "", "", "Authorization"
	switch provider {
	case "openai":
		base = get("OPENAI_BASE_URL")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		endpoint = "/models"
		keyName = "OPENAI_API_KEY"
	case "anthropic":
		base = get("ANTHROPIC_BASE_URL")
		if base == "" {
			base = "https://api.anthropic.com"
		}
		endpoint = "/v1/models?limit=1000"
		keyName = "ANTHROPIC_API_KEY"
		header = "x-api-key"
	case "gemini":
		base = "https://generativelanguage.googleapis.com"
		endpoint = "/v1beta/models?pageSize=1000"
		keyName = "GEMINI_API_KEY"
		if get(keyName) == "" {
			keyName = "GOOGLE_API_KEY"
		}
		header = "x-goog-api-key"
	case "openrouter":
		base = get("OPENROUTER_BASE_URL")
		if base == "" {
			base = "https://openrouter.ai/api/v1"
		}
		endpoint = "/models/user?output_modalities=text"
		keyName = "OPENROUTER_API_KEY"
	case "ollama":
		base = get("OLLAMA_HOST")
		if base == "" {
			base = "http://127.0.0.1:11434"
		}
		endpoint = "/api/tags"
	default:
		return nil, fmt.Errorf("unsupported model catalog %q", provider)
	}
	if err := validateEndpoint(base); err != nil {
		return nil, err
	}
	key := get(keyName)
	if keyName != "" && key == "" {
		return nil, fmt.Errorf("%s is not set; add it to your environment to load models", keyName)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if c.HTTP != nil {
		copy := *c.HTTP
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	address, err := url.Parse(strings.TrimRight(base, "/") + endpoint)
	if err != nil {
		return nil, errors.New("invalid model catalog endpoint")
	}
	result := map[string]ModelInfo{}
	pages := map[string]bool{}
	for page := 0; page < 100; page++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
		if err != nil {
			return nil, errors.New("could not prepare model catalog request")
		}
		if keyName != "" {
			value := key
			if header == "Authorization" {
				value = "Bearer " + key
			}
			request.Header.Set(header, value)
		}
		if provider == "anthropic" {
			request.Header.Set("anthropic-version", "2023-06-01")
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("could not connect to the %s model catalog", provider)
		}
		data, err := readCatalog(response)
		closeErr := response.Body.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, errors.New("could not close model catalog response")
		}
		var listing struct {
			Data []struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				DisplayName  string `json:"display_name"`
				Architecture struct {
					Outputs []string `json:"output_modalities"`
				} `json:"architecture"`
			} `json:"data"`
			Models []struct {
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Methods     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			More bool   `json:"has_more"`
			Last string `json:"last_id"`
			Next string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(data, &listing); err != nil {
			return nil, errors.New("the provider returned an invalid model catalog")
		}
		add := func(id, name string) {
			id = strings.TrimSpace(id)
			if id == "" || len(id) > 512 || strings.ContainsFunc(id, unicode.IsControl) {
				return
			}
			name = SafeSummary(name)
			if name == "" {
				name = id
			}
			result[id] = ModelInfo{ID: id, Name: name}
		}
		for _, model := range listing.Data {
			if provider == "openai" && get("OPENAI_BASE_URL") == "" && !openAIChatModel(model.ID) {
				continue
			}
			if provider == "openrouter" && len(model.Architecture.Outputs) > 0 && !slices.Contains(model.Architecture.Outputs, "text") {
				continue
			}
			name := model.DisplayName
			if name == "" {
				name = model.Name
			}
			add(model.ID, name)
		}
		for _, model := range listing.Models {
			if provider == "gemini" && !slices.Contains(model.Methods, "generateContent") {
				continue
			}
			id := model.Name
			if provider == "gemini" {
				id = strings.TrimPrefix(id, "models/")
				if specializedModel(id) {
					continue
				}
			}
			add(id, model.DisplayName)
		}
		next, param := "", ""
		if provider == "anthropic" && listing.More {
			next, param = listing.Last, "after_id"
			if next == "" {
				return nil, errors.New("the model catalog omitted its next-page cursor")
			}
		}
		if provider == "gemini" {
			next, param = listing.Next, "pageToken"
		}
		if next == "" {
			models := make([]ModelInfo, 0, len(result))
			for _, model := range result {
				models = append(models, model)
			}
			sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
			if len(models) == 0 {
				return nil, errors.New("the provider returned no compatible models")
			}
			return models, nil
		}
		if pages[next] {
			return nil, errors.New("the model catalog repeated a page")
		}
		pages[next] = true
		query := address.Query()
		query.Set(param, next)
		address.RawQuery = query.Encode()
	}
	return nil, errors.New("the model catalog exceeded its page limit")
}

func readCatalog(response *http.Response) ([]byte, error) {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("the model catalog returned HTTP %d; check the provider credentials and endpoint", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return nil, errors.New("could not read the model catalog")
	}
	if len(data) > 8<<20 {
		return nil, errors.New("the model catalog exceeds the response-size limit")
	}
	return data, nil
}

func specializedModel(id string) bool {
	for _, fragment := range []string{"embed", "image", "audio", "realtime", "transcrib", "tts", "whisper", "dall-e", "moderation", "sora"} {
		if strings.Contains(strings.ToLower(id), fragment) {
			return true
		}
	}
	return false
}
func openAIChatModel(id string) bool {
	return !specializedModel(id) && (strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "chatgpt-") || (len(id) > 1 && id[0] == 'o' && id[1] >= '0' && id[1] <= '9'))
}

func SupportsEffort(provider, model string) bool {
	if provider != "anthropic" {
		return false
	}
	adapter, err := anthropic.New(anthropic.WithToken("capability-check-only"), anthropic.WithModel(model))
	return err == nil && adapter.SupportsReasoning()
}
