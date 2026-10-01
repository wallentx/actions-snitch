// Package config loads the existing YAML configuration and environment overrides.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Lookup func(string) (string, bool)

type AI struct {
	Enabled     bool   `yaml:"enabled"`
	Provider    string `yaml:"provider"`
	Model       string `yaml:"model"`
	Effort      string `yaml:"effort,omitempty"`
	Threshold   int    `yaml:"threshold"`
	IssueSearch string `yaml:"issue_search"`
}

type Config struct {
	AI AI `yaml:"ai"`
}

func Defaults() Config { return Config{AI: AI{Provider: "openai", Threshold: 80, IssueSearch: "auto"}} }

func env(lookup Lookup, key string) string { v, _ := lookup(key); return v }

func Path(lookup Lookup) string {
	if p := env(lookup, "ACTIONS_SNITCH_CONFIG"); p != "" {
		return p
	}
	base := env(lookup, "XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(env(lookup, "HOME"), ".config")
	}
	return filepath.Join(base, "actions-snitch", "config.yaml")
}

func CachePath(lookup Lookup) string {
	base := env(lookup, "XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(env(lookup, "HOME"), ".cache")
	}
	return filepath.Join(base, "actions-snitch")
}

func Boolean(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off", "":
		return false, nil
	default:
		return false, fmt.Errorf("AI enabled must be true or false")
	}
}

func Threshold(s string) (int, error) {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return 0, nil
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("AI threshold must be an integer from 0 to 100")
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > 100 {
		return 0, errors.New("AI threshold must be an integer from 0 to 100")
	}
	return n, nil
}

func Load(path string, lookup Lookup) (Config, error) {
	c := Defaults()
	values := map[string]string{}
	// #nosec G304 -- The user explicitly selects the configuration path, including paths outside the repository.
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		var document struct {
			AI map[string]yaml.Node `yaml:"ai"`
		}
		dec := yaml.NewDecoder(bytes.NewReader(b))
		if err := dec.Decode(&document); err != nil && err != io.EOF {
			return c, fmt.Errorf("parse %s: %w", path, err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return c, fmt.Errorf("config must contain one YAML document")
		}
		for k, node := range document.AI {
			if node.Kind != yaml.ScalarNode {
				return c, fmt.Errorf("ai.%s must be a scalar", k)
			}
			if node.Tag != "!!null" {
				values[k] = node.Value
			}
		}
	}
	if values["provider"] == "" {
		values["provider"] = values["backend"]
		if values["provider"] == "llm" {
			values["provider"] = "codex"
		}
	}
	for key, variable := range map[string]string{"enabled": "ACTIONS_SNITCH_AI", "provider": "ACTIONS_SNITCH_AI_PROVIDER", "model": "ACTIONS_SNITCH_AI_MODEL", "effort": "ACTIONS_SNITCH_AI_EFFORT", "threshold": "ACTIONS_SNITCH_AI_THRESHOLD"} {
		if v, ok := lookup(variable); ok && (v != "" || key == "enabled") {
			values[key] = v
		}
	}
	c.AI.Enabled, err = Boolean(values["enabled"])
	if err != nil {
		return c, err
	}
	if v := values["provider"]; v != "" {
		c.AI.Provider = v
	}
	switch c.AI.Provider {
	case "codex":
		c.AI.Provider = "openai"
	case "claude":
		c.AI.Provider = "anthropic"
	case "googleai":
		c.AI.Provider = "gemini"
	case "openai", "anthropic", "gemini", "openrouter", "ollama":
	default:
		return c, fmt.Errorf("AI provider %q requires migration: use openai, anthropic, gemini, openrouter, or ollama with environment credentials; CLI login sessions are no longer used", c.AI.Provider)
	}
	c.AI.Model, c.AI.Effort = values["model"], values["effort"]
	if v := values["threshold"]; v != "" {
		c.AI.Threshold, err = Threshold(v)
		if err != nil {
			return c, err
		}
	}
	if v := values["issue_search"]; v != "" {
		c.AI.IssueSearch = v
	}
	switch c.AI.IssueSearch {
	case "auto", "always", "never":
	default:
		return c, errors.New("AI issue_search must be auto, always, or never")
	}

	return c, nil
}

// SaveNew never overwrites an existing file, including a dangling symlink.
func SaveNew(path string, c Config) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// #nosec G304 -- Setup writes the explicitly selected config path with exclusive creation and owner-only permissions.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create config (existing files are not overwritten): %w", err)
	}
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
