# actions-snitch

Like dependabot, but in bash for local execution. This tool helps you identify and update outdated GitHub Actions in workflow and composite-action files.

<img width="3173" height="1161" alt="1000037896" src="https://github.com/user-attachments/assets/83e27000-7b8a-4f43-93e0-d7e310287225" />


## Features

- 🔍 Scans workflow and composite-action files for outdated GitHub Actions
- 📍 Shows exact line numbers where actions are used
- 🚀 Shows compatibility scores between versions
- 💾 Caches API responses for faster subsequent runs (24h TTL)
- 🔄 Can automatically create PRs to update actions
- 🤖 Can ask an optional, provider-neutral LLM to investigate low compatibility scores before updating
- ☑️ Indicates GitHub Marketplace verified creators
- 🎨 Cute color-coded output with status badges
- 🏃 Fast local execution
- 🔒 Handles private/internal actions gracefully

## Requirements

- `gh` (GitHub CLI)
- `jq`
- `yq`
- `curl`
- `git`

AI-assisted updates additionally require Simon Willison's [`llm` CLI](https://llm.datasette.io/en/stable/setup.html) and a schema-capable model. Provider plugins are listed in the [`llm` plugin directory](https://llm.datasette.io/en/stable/plugins/directory.html).

## Installation

1. Clone this repository
2. Add the `bin` directory to your PATH or create a symlink to `bin/actions-snitch` in a directory that's in your PATH

## Usage

```bash
actions-snitch [-u] [-f] [-p] [-b branch] [-o format] [-t] [-v] [-h]
```

### Options

- `-u` Update outdated actions in-place
- `-f` Force updates regardless of compatibility score (requires -u or -p)
- `-p` Commit, push, and create a pull request after updating actions (implies -u)
- `-b` Branch to update or create before applying changes (requires -u or -p)
- `-o` Output findings as `json`, `md`, or `yaml`
- `-t` Only update actions from GitHub Marketplace verified creators (requires -u or -p)
- `-v` Verbose output - show skipped actions and debug info
- `-h` Display help message

### Example Output

```
Findings in .github/workflows/build.yml:
    ❗ actions/checkout is outdated:
      Line: 52
      Current: 2
      Latest: 4
      🤖compatibility: 79%
      Release Notes: https://github.com/actions/checkout/releases/tag/v4
```

### Structured Output

Use `-o json`, `-o md`, or `-o yaml` to print only findings in a machine-readable or report-friendly format. Structured output is grouped under the name of the scanned repository and prints nothing when there are no findings.

Actions from GitHub Marketplace verified creators are marked with `☑️` in human and Markdown output. JSON and YAML output keep the action name unchanged and include a `verified_creator` boolean.

Use `-t` with `-u` or `-p` to update only actions from verified creators. This gate is stricter than `-f`; forced updates still skip unverified creators when `-t` is set.

Markdown output links the repository heading to the `origin` remote when one is configured.

### AI-Assisted Updates

AI analysis is opt-in and only runs during `-u` or `-p` when an action's compatibility score is below the configured threshold. The model receives redacted workflow context, release and changelog content, action definitions, compare commits, and a bounded upstream issue search. It must return a schema-validated `allow`, `review`, or `block` decision.

An `allow` decision may include narrowly scoped remediation for the affected action step's `with:` inputs. Before making any change, actions-snitch verifies the workflow file, exact action line, current action version, input name, and current scalar value. Sensitive inputs, arbitrary YAML patches, permissions, triggers, environment variables, shell commands, and changes outside the affected action step are rejected. All proposed input changes are validated and applied to temporary copies first, so an invalid proposal cannot partially modify the repository. Errors and invalid responses fail closed; `-f` remains the explicit version-update override and never applies an unvalidated remediation.

Create `~/.config/actions-snitch/config.yaml`, or use `$XDG_CONFIG_HOME/actions-snitch/config.yaml`:

```yaml
ai:
  enabled: false
  backend: llm
  model: your-installed-model-id
  threshold: 80
  issue_search: auto
  # Optional: require this environment variable to be set.
  api_key_env: OPENAI_API_KEY
```

Store credentials with the provider's environment variable or the `llm` key store, not in this file. For example:

```bash
llm keys set openai
ACTIONS_SNITCH_AI=true actions-snitch -u
```

Environment variables override the corresponding defaults:

- `ACTIONS_SNITCH_AI=true|false`
- `ACTIONS_SNITCH_AI_MODEL=model-id`
- `ACTIONS_SNITCH_AI_THRESHOLD=0..100`
- `ACTIONS_SNITCH_CONFIG=/path/to/config.yaml`

The `llm` request uses `--no-log`, so workflow evidence is not written to its local prompt database. Redacted workflow evidence is still sent to the selected model provider; use a local model plugin when repository policy prohibits that.

### Pull Request Body

PRs created with `-p` use a Dependabot-inspired body: a summary of the GitHub Actions updates, one section per unique action/version update, links to the action repositories, collapsible release notes/changelog/commit details, Dependabot compatibility badges, and a small `actions-snitch` footer. Repeated references to the same action update are collapsed into one section with a workflow-entry count. AI-approved or forced low-score updates also include the model's validated compatibility investigation; successfully applied input remediations are listed in that same collapsible section.

## How It Works

1. Scans `.yml` and `.yaml` files in `.github/workflows/`, plus `action.yml` and `action.yaml` composite actions
2. For each GitHub Action found:
   - Records the exact line number where it's used
   - Checks the current version against the latest release
   - Fetches compatibility score from dependabot
   - Shows detailed information for outdated actions
3. Optionally updates workflow and composite-action files in-place
4. Optionally commits the updates, pushes them, and creates a PR
5. Skips internal/private actions and docker references

## Cache

The tool caches API responses in `~/.cache/actions-snitch/` (or `$XDG_CACHE_HOME/actions-snitch/`) to:
- Reduce API calls to GitHub
- Speed up subsequent runs
- Avoid rate limiting

Cache entries expire after 24 hours.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
