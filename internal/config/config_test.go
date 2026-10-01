package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrecedenceAndAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("ai:\n  enabled: yes\n  backend: llm\n  model: old\n  threshold: 008\n  issue_search: never\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ACTIONS_SNITCH_AI": "", "ACTIONS_SNITCH_AI_MODEL": "new", "ACTIONS_SNITCH_AI_THRESHOLD": "00000000000000070"}
	c, err := Load(path, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if c.AI.Enabled || c.AI.Model != "new" || c.AI.Provider != "openai" || c.AI.Threshold != 70 || c.AI.IssueSearch != "never" {
		t.Fatalf("unexpected config: %+v", c)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, contents := range []string{"ai: [", "ai: {threshold: -1}", "ai: {threshold: 101}", "ai: {enabled: maybe}", "ai: {issue_search: sometimes}", "ai: {provider: cursor}", "ai: {model: [x]}", "ai: {}\n---\nai: {}"} {
		t.Run(contents, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(p, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p, func(string) (string, bool) { return "", false }); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestSaveExclusivePrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new", "config.yaml")
	if err := SaveNew(p, Defaults()); err != nil {
		t.Fatal(err)
	}
	i, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if i.Mode().Perm() != 0600 {
		t.Fatal(i.Mode())
	}
	if err := SaveNew(p, Defaults()); err == nil {
		t.Fatal("overwrote config")
	}
}
