package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/wallentx/actions-snitch/internal/config"
	"github.com/wallentx/actions-snitch/internal/llm"
)

func setupForm(ctx context.Context, r Runtime, description string, fields ...huh.Field) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	theme := huh.ThemeCharm()
	group := huh.NewGroup(fields...).Title("Actions Snitch").Description(description + "\nCtrl+C cancels without saving.").WithTheme(theme)
	form := huh.NewForm(group).
		// The main process owns OS signals; a second signal handler can race
		// context cancellation while the terminal program shuts down.
		WithInput(r.Input).WithOutput(r.Error).WithTheme(theme).WithWidth(76).WithAccessible(false).WithProgramOptions(tea.WithoutSignalHandler())
	err := form.RunWithContext(ctx)
	if ctx.Err() != nil || errors.Is(err, huh.ErrUserAborted) {
		return context.Canceled
	}
	return err
}

func configureInteractive(ctx context.Context, r Runtime, path string) error {
	cfg := config.Defaults()
	if err := setupForm(ctx, r, "Step 1 of 4 · Use arrow keys to choose and Enter to continue.",
		huh.NewSelect[bool]().Title("Should AI investigate uncertain updates?").
			Description("Enabled AI sends redacted workflow evidence to your chosen model provider.").
			Options(huh.NewOption("No, use compatibility scores only", false), huh.NewOption("Yes, enable AI-assisted review", true)).Value(&cfg.AI.Enabled)); err != nil {
		return err
	}
	if cfg.AI.Enabled {
		if err := chooseProviderAndModel(ctx, r, &cfg); err != nil {
			return err
		}
		if llm.SupportsEffort(cfg.AI.Provider, cfg.AI.Model) {
			if err := setupForm(ctx, r, "Step 2 of 4 · Choose how much thinking the selected model should use.",
				huh.NewSelect[string]().Title("Which thinking effort should it use?").Options(huh.NewOption("Provider default", ""), huh.NewOption("Low", "low"), huh.NewOption("Medium", "medium"), huh.NewOption("High", "high")).Value(&cfg.AI.Effort)); err != nil {
				return err
			}
		}
	}
	if cfg.AI.Enabled {
		threshold := strconv.Itoa(cfg.AI.Threshold)
		if err := setupForm(ctx, r, "Step 3 of 4 · Automatic updates always retain the compatibility floor of 80.",
			huh.NewInput().Title("Below which score should AI investigate?").Description("Enter a number from 0 through 100.").Value(&threshold).Validate(func(value string) error { _, err := config.Threshold(value); return err }),
			huh.NewSelect[string]().Title("Should assessments include upstream issues?").Options(huh.NewOption("Auto: include relevant upstream issues", "auto"), huh.NewOption("Always include upstream issues", "always"), huh.NewOption("Never search upstream issues", "never")).Value(&cfg.AI.IssueSearch)); err != nil {
			return err
		}
		value, err := config.Threshold(threshold)
		if err != nil {
			return err
		}
		cfg.AI.Threshold = value
	}

	summary := "AI is disabled."
	if cfg.AI.Enabled {
		summary = fmt.Sprintf("Provider: %s\nModel: %s\nThinking effort: %s", cfg.AI.Provider, cfg.AI.Model, displayEffort(cfg.AI.Effort))
	}
	summary += fmt.Sprintf("\nThreshold: %d\nIssue search: %s\nConfig file: %s", cfg.AI.Threshold, cfg.AI.IssueSearch, path)
	save := true
	reviewTitle := "Step 4 of 4 · Review your choices before writing the config file."
	if !cfg.AI.Enabled {
		reviewTitle = "Review your choices before writing the config file."
	}
	if err := setupForm(ctx, r, reviewTitle, huh.NewConfirm().Title("Save this configuration?").Description(summary).Affirmative("Save").Negative("Cancel").Value(&save)); err != nil {
		return err
	}
	if !save {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.SaveNew(path, cfg); err != nil {
		return err
	}
	_, err := fmt.Fprintf(r.Error, "Created %s\n", path)
	return err
}

func chooseProviderAndModel(ctx context.Context, r Runtime, cfg *config.Config) error {
	catalog := r.Catalog
	if catalog == nil {
		catalog = (llm.Catalog{Lookup: r.Lookup}).Models
	}
	for {
		if err := setupForm(ctx, r, "Step 2 of 4 · The provider's model catalog uses your environment credentials.",
			huh.NewSelect[string]().Title("Which provider should perform reviews?").Options(
				huh.NewOption("OpenAI", "openai"), huh.NewOption("Anthropic", "anthropic"), huh.NewOption("Google Gemini", "gemini"), huh.NewOption("OpenRouter", "openrouter"), huh.NewOption("Ollama (local)", "ollama")).Value(&cfg.AI.Provider)); err != nil {
			return err
		}
		for {
			_, _ = fmt.Fprintf(r.Error, "\n  Loading %s models...\n\n", cfg.AI.Provider)
			models, err := catalog(ctx, cfg.AI.Provider)
			if ctx.Err() != nil {
				return context.Canceled
			}
			if err != nil || len(models) == 0 {
				message := "The provider returned no models."
				if err != nil {
					message = llm.SafeSummary(err.Error())
				}
				choice := "retry"
				if err := setupForm(ctx, r, "Step 2 of 4 · The model catalog could not be loaded.", huh.NewSelect[string]().Title("How would you like to continue?").Description(message).Options(huh.NewOption("Retry the model catalog", "retry"), huh.NewOption("Enter a model ID manually", "manual"), huh.NewOption("Choose another provider", "provider"), huh.NewOption("Cancel setup", "cancel")).Value(&choice)); err != nil {
					return err
				}
				switch choice {
				case "retry":
					continue
				case "manual":
					return manualModel(ctx, r, &cfg.AI.Model)
				case "cancel":
					return context.Canceled
				}
				break
			}
			const manual = "\x00manual"
			choices := make([]huh.Option[string], 0, len(models)+1)
			for _, model := range models {
				label := model.ID
				if model.Name != "" && model.Name != model.ID {
					label = model.Name + " (" + model.ID + ")"
				}
				choices = append(choices, huh.NewOption(label, model.ID))
			}
			choices = append(choices, huh.NewOption("Enter a model ID manually", manual))
			selection := models[0].ID
			if err := setupForm(ctx, r, "Step 2 of 4 · Press / to search the catalog, use arrows to select, and Enter to continue.",
				huh.NewSelect[string]().Title("Which model should perform reviews?").Description(fmt.Sprintf("%d models are available from %s.", len(models), cfg.AI.Provider)).Options(choices...).Height(12).Value(&selection)); err != nil {
				return err
			}
			if selection == manual {
				return manualModel(ctx, r, &cfg.AI.Model)
			}
			cfg.AI.Model = selection
			return nil
		}
	}
}

func manualModel(ctx context.Context, r Runtime, model *string) error {
	err := setupForm(ctx, r, "Step 2 of 4 · Enter the exact model ID accepted by your provider.", huh.NewInput().Title("Which model ID should it use?").Value(model).Validate(func(value string) error {
		if strings.TrimSpace(value) == "" || len(value) > 512 || strings.ContainsFunc(value, unicode.IsControl) {
			return errors.New("enter a nonempty model ID without control characters")
		}
		return nil
	}))
	if err == nil {
		*model = strings.TrimSpace(*model)
	}
	return err
}

func displayEffort(effort string) string {
	if effort == "" {
		return "provider default"
	}
	return effort
}
