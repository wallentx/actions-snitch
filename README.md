# actions-snitch

Like dependabot, but in bash for local execution. This tool helps you identify and update outdated GitHub Actions in workflow and composite-action files.

<img width="3173" height="1161" alt="1000037896" src="https://github.com/user-attachments/assets/83e27000-7b8a-4f43-93e0-d7e310287225" />


## Features

- 🔍 Scans workflow and composite-action files for outdated GitHub Actions
- 📍 Shows exact line numbers where actions are used
- 🚀 Shows compatibility scores between versions
- 💾 Caches API responses for faster subsequent runs (24h TTL)
- 🔄 Can automatically create PRs to update actions
- 🤖 Can ask an optional authenticated provider CLI to investigate low or unknown compatibility scores before updating
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

AI-assisted updates additionally require an authenticated, supported provider CLI. The interactive setup detects these exact executable names:

| Provider | Executable | Non-interactive interface |
| --- | --- | --- |
| [OpenAI Codex](https://developers.openai.com/blog/eval-skills) | `codex` | `codex exec` with an output schema |
| [Claude Code](https://code.claude.com/docs/en/cli-usage) | `claude` | `claude -p` with a JSON schema |
| [Cursor Agent](https://cursor.com/docs/en/cli/headless) | `agent` (`cursor-agent` compatibility alias) | Print mode with JSON output |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/tutorials/automation.md) | `gemini` | Headless mode with JSON output |
| [OpenCode](https://opencode.ai/v2/docs/cli/commands/) | `opencode` | `opencode run` with JSON events |
| [GitHub Copilot CLI](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-programmatic-reference) | `copilot` | Prompt mode with JSON events |
| [Google Antigravity](https://www.antigravity.google/docs/cli/headless/) | `agy` | Print mode with a JSON schema |

## Installation

1. Clone this repository
2. Add the `bin` directory to your PATH or create a symlink to `bin/actions-snitch` in a directory that's in your PATH

## Usage

```bash
actions-snitch [-c] [-u] [-s] [-f] [-p] [-b branch] [-o format] [-t] [-v] [-h]
```

### Options

- `-c` Interactively create the config file and exit (use alone)
- `-u` Update outdated actions in-place
- `-s` Pin proposed updates to full commit SHAs; combine with `-u` or `-p` to apply
- `-f` Force updates regardless of compatibility score (requires -u or -p)
- `-p` Commit, push, and create a pull request after updating actions (implies -u)
- `-b` Branch to update or create before applying changes (requires -u or -p)
- `-o` Output findings as `json`, `md`, or `yaml`
- `-t` Only update actions from GitHub Marketplace verified creators (requires -u or -p)
- `-v` Verbose output - show skipped actions and debug info
- `-h` Display help message

### SHA pins and readable versions

```bash
actions-snitch -s       # Preview updates as full SHA pins
actions-snitch -u -s    # Apply updates as full SHA pins
actions-snitch -u       # Preserve each reference's existing tag/SHA style
```

| Current reference | `-u` | `-u -s` |
| --- | --- | --- |
| Release tag | Updated tag (major-only tags stay major-only) | Latest release's full commit SHA |
| Full commit SHA | Updated full commit SHA | Updated full commit SHA |

`-s` also converts already-current release tags to SHA pins. Branch references such as `main` and `master` remain skipped. Existing compatibility, AI, and verified-creator gates still apply.

SHA findings show the current SHA's **exact matching tag**, the latest release, and its resolved commit SHA. All tag pages are searched; when multiple tags match, the lowest full stable semantic version is preferred over moving major/minor aliases. This is an exact commit match, not a claim about the first release containing an ancestor commit.

```text
Current: <40-character current SHA>
Current SHA matches tag: v3.11.1
Latest: 4.4.1
Latest SHA: <40-character latest SHA>
```

If no tag matches, `Commits since: N` replaces the tag line. The count comes from [GitHub's compare API](https://docs.github.com/en/rest/commits/commits#compare-two-commits), including counts beyond one page of commits. With no release, the default branch's SHA is the target. Only a target strictly ahead of the current SHA is an update: identical, behind, and diverged histories are never automatically rewritten. Failed SHA resolution or comparison emits a warning and skips the update. A tag lookup failure is reported separately from a successful lookup with no matches.

### Ignoring fixtures

Create `.snitchignore` in the directory where you run actions-snitch. This repository includes `.test/` to exclude local fixture checkouts from both findings and updates.

```text
# Paths are relative to the scan directory.
.test/
fixtures with spaces/
.github/workflows/example-*.yml
!.github/workflows/example-maintained.yml
```

Patterns are Bash globs, not full `.gitignore` syntax: `*`, `?`, and character classes are supported, `*` can span `/`, a trailing `/` matches all descendants, and a leading `/` or `./` is optional. Blank lines and lines beginning with `#` are ignored. `!` re-includes a matching path; the last matching rule wins. `.gitignore` does not control scanning.

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

SHA metadata is included in JSON, YAML, and Markdown: `current_tag`, `latest_sha`, `commits_since`, and the exact proposed `update_ref`. Unavailable SHA metadata is `null` in JSON/YAML.

Actions from GitHub Marketplace verified creators are marked with `☑️` in human and Markdown output. JSON and YAML output keep the action name unchanged and include a `verified_creator` boolean.

Use `-t` with `-u` or `-p` to update only actions from verified creators. This gate is stricter than `-f`; forced updates still skip unverified creators when `-t` is set.

Markdown output links the repository heading to the `origin` remote when one is configured.

### AI-Assisted Updates

AI analysis is opt-in and only runs during `-u` or `-p` when an action's compatibility score is below the configured threshold. The model receives redacted workflow context and both action definitions. Release notes, changelogs, compare commits, and a bounded upstream issue search provide additional evidence when available. Scores below 80 still require AI approval or `-f`; lowering the AI threshold does not permit automatic updates between that threshold and 80. Unknown or invalid compatibility scores also require AI approval or `-f`, so a missing score never authorizes an update by itself. The model must return a schema-validated `allow`, `review`, or `block` decision.

An `allow` decision may include narrowly scoped remediation for the affected action step's `with:` inputs. Before making any change, actions-snitch verifies the workflow file, exact action line and full action path, current action version, input name, and current scalar value. Sensitive inputs, arbitrary YAML patches, permissions, triggers, environment variables, shell commands, and changes outside the affected action step are rejected. All proposed input changes are validated and applied to temporary copies first, so an invalid proposal cannot partially modify the repository. Missing local usage evidence or either action definition, backend errors, and invalid responses fail closed; `-f` remains the explicit version-update override and never applies an unvalidated remediation.

Run the interactive setup:

```bash
actions-snitch -c
```

It asks whether to enable AI, which detected provider and model to use, the provider effort level when discoverable, the compatibility threshold, and the upstream issue search policy. The provider prompt lists only supported executables found on `PATH`. Cursor, OpenCode, and Antigravity expose non-interactive model lists, so setup presents numbered model choices for them. Claude and Copilot offer their documented effort levels. Antigravity model IDs that end in `-low`, `-medium`, or `-high` pin their effort, so setup offers only that level and the provider default. Other providers use manual model entry and their default effort because their CLIs do not expose a reliable non-interactive catalog. AI stays disabled by default.

Setup creates `~/.config/actions-snitch/config.yaml` (or `$XDG_CONFIG_HOME/actions-snitch/config.yaml`) with owner-only permissions. `ACTIONS_SNITCH_CONFIG` can select a different file. Existing files are never overwritten; edit them directly to change settings. Press Ctrl-C to cancel before writing. Setup only requires `jq` and `yq`, and does not scan or modify a repository.

The generated file has this shape:

```yaml
ai:
  enabled: false
  provider: codex
  model: your-provider-model-id
  # Optional when the selected provider and model support it.
  # effort: high
  threshold: 80
  issue_search: auto
```

The selected CLI uses its existing login session, so the actions-snitch configuration does not store provider credentials. Authenticate with that CLI before enabling AI. For example:

```bash
codex login
ACTIONS_SNITCH_AI=true actions-snitch -u
```

Environment variables override the corresponding defaults:

- `ACTIONS_SNITCH_AI=true|false`
- `ACTIONS_SNITCH_AI_PROVIDER=codex|claude|cursor|gemini|opencode|copilot|antigravity`
- `ACTIONS_SNITCH_AI_MODEL=model-id`
- `ACTIONS_SNITCH_AI_EFFORT=provider-supported-level`
- `ACTIONS_SNITCH_AI_THRESHOLD=0..100`
- `ACTIONS_SNITCH_CONFIG=/path/to/config.yaml`

Each assessment runs non-interactively in a temporary directory. Codex, Claude, and Antigravity receive the actions-snitch JSON schema directly. Cursor, Gemini, OpenCode, and Copilot return machine-readable envelopes, from which actions-snitch extracts and validates the final JSON response against the same schema. Every backend error, missing response, parse failure, or schema violation fails closed. The request sends redacted workflow evidence to the selected provider through its authenticated CLI session.

Codex, Claude, Gemini, OpenCode, Copilot, and Antigravity receive the complete assessment prompt through stdin. The Cursor adapter requires a command argument, so prompts above 32 KiB require manual review without invoking the CLI. This limit counts bytes and preserves the complete workflow evidence.

### Pull Request Body

With `-p`, actions-snitch pushes to `origin` and opens the PR in the repository selected by `origin`'s push URL, targeting that repository's default branch. When `origin` pushes to your fork, the PR stays in your fork instead of GitHub CLI's inferred upstream repository. The destination is checked before any branch or workflow changes.

PRs created with `-p` use a Dependabot-inspired body: a summary of the GitHub Actions updates, one section per unique action/version update, links to the action repositories, collapsible release notes/changelog/commit details, Dependabot compatibility badges, and a small `actions-snitch` footer. Repeated references to the same action update are collapsed into one section with a workflow-entry count. AI-approved updates or updates forced after an AI assessment also include the model's validated compatibility investigation; successfully applied input remediations are listed in that same collapsible section.

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
