package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/wallentx/actions-snitch/internal/config"
)

func prompt(ctx context.Context, reader *bufio.Reader, r Runtime, label string) (string, error) {
	if _, err := fmt.Fprint(r.Error, label); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type answer struct {
		line string
		err  error
	}
	ready := make(chan answer, 1)
	// Terminal reads need not unblock on Close. Cancellation lets the CLI exit
	// without waiting for its one outstanding noninteractive stdin read.
	go func() { line, err := reader.ReadString('\n'); ready <- answer{line, err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value := <-ready:
		if value.err != nil {
			return "", errors.New("setup cancelled: no config was written")
		}
		return strings.TrimSuffix(strings.TrimSuffix(value.line, "\n"), "\r"), nil
	}
}

func configure(ctx context.Context, r Runtime) error {
	path := config.Path(r.Lookup)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("config already exists; edit %s to change settings", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if r.Interactive {
		return configureInteractive(ctx, r, path)
	}
	reader := bufio.NewReader(r.Input)
	cfg := config.Defaults()
	_, _ = fmt.Fprintf(r.Error, "Create %s\nAI analysis sends redacted workflow context to your selected API provider.\n", path)
	for {
		answer, err := prompt(ctx, reader, r, "Enable AI-assisted updates? [y/N]: ")
		if err != nil {
			return err
		}
		if answer == "y" || answer == "Y" {
			answer = "yes"
		}
		if answer == "n" || answer == "N" {
			answer = "no"
		}
		enabled, err := config.Boolean(answer)
		if err == nil {
			cfg.AI.Enabled = enabled
			break
		}
		_, _ = fmt.Fprintln(r.Error, "Enter yes or no.")
	}
	if cfg.AI.Enabled {
		providers := []string{"openai", "anthropic", "gemini", "openrouter", "ollama"}
		for i, p := range providers {
			_, _ = fmt.Fprintf(r.Error, "  %d) %s\n", i+1, p)
		}
		for {
			answer, err := prompt(ctx, reader, r, "Select provider [1]: ")
			if err != nil {
				return err
			}
			if answer == "" {
				answer = "1"
			}
			if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(providers) {
				cfg.AI.Provider = providers[n-1]
				break
			}
			found := false
			for _, p := range providers {
				if answer == p {
					cfg.AI.Provider = p
					found = true
				}
			}
			if found {
				break
			}
			_, _ = fmt.Fprintln(r.Error, "Enter a provider name or number.")
		}
		for {
			answer, err := prompt(ctx, reader, r, "Model ID: ")
			if err != nil {
				return err
			}
			if strings.TrimSpace(answer) != "" {
				cfg.AI.Model = answer
				break
			}
			_, _ = fmt.Fprintln(r.Error, "Enter a model ID.")
		}
		if cfg.AI.Provider == "anthropic" {
			for {
				answer, err := prompt(ctx, reader, r, "Thinking effort (low/medium/high; empty uses API default): ")
				if err != nil {
					return err
				}
				if answer == "" || answer == "low" || answer == "medium" || answer == "high" {
					cfg.AI.Effort = answer
					break
				}
				_, _ = fmt.Fprintln(r.Error, "Enter low, medium, high, or leave empty.")
			}
		}
	}
	for {
		answer, err := prompt(ctx, reader, r, "Analyze compatibility scores below (0-100) [80]: ")
		if err != nil {
			return err
		}
		if answer == "" {
			answer = "80"
		}
		threshold, err := config.Threshold(answer)
		if err == nil {
			cfg.AI.Threshold = threshold
			break
		}
		_, _ = fmt.Fprintln(r.Error, "Enter an integer from 0 to 100.")
	}
	for {
		answer, err := prompt(ctx, reader, r, "Search upstream issues (auto/always/never) [auto]: ")
		if err != nil {
			return err
		}
		if answer == "" {
			answer = "auto"
		}
		if answer == "auto" || answer == "always" || answer == "never" {
			cfg.AI.IssueSearch = answer
			break
		}
		_, _ = fmt.Fprintln(r.Error, "Enter auto, always, or never.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.SaveNew(path, cfg); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.Error, "Created %s\n", path)
	return nil
}

func promptBranch(ctx context.Context, r Runtime) (string, error) {
	reader := bufio.NewReader(r.Input)
	answer, err := prompt(ctx, reader, r, "Create a new branch before applying updates and opening the PR? [Y/n] ")
	if err != nil {
		return "", err
	}
	if strings.EqualFold(answer, "n") || strings.EqualFold(answer, "no") {
		return "", nil
	}
	branch, err := prompt(ctx, reader, r, "Branch name [actions-snitch/update-actions]: ")
	if err != nil {
		return "", err
	}
	if branch == "" {
		branch = "actions-snitch/update-actions"
	}
	return branch, nil
}
