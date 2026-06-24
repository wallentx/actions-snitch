# actions-snitch

Like dependabot, but in bash for local execution. This tool helps you identify and update outdated GitHub Actions in your workflow files.

<img width="3173" height="1161" alt="1000037896" src="https://github.com/user-attachments/assets/83e27000-7b8a-4f43-93e0-d7e310287225" />


## Features

- 🔍 Scans all workflow files for outdated GitHub Actions
- 📍 Shows exact line numbers where actions are used
- 🚀 Shows compatibility scores between versions
- 💾 Caches API responses for faster subsequent runs (24h TTL)
- 🔄 Can automatically create PRs to update actions
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

### Pull Request Body

PRs created with `-p` use a Dependabot-inspired body: a summary of the GitHub Actions updates, one section per unique action/version update, links to the action repositories, collapsible release notes/changelog/commit details, Dependabot compatibility badges, and a small `actions-snitch` footer. Repeated references to the same action update are collapsed into one section with a workflow-entry count.

## How It Works

1. Scans all `.yml` files in `.github/workflows/`
2. For each GitHub Action found:
   - Records the exact line number where it's used
   - Checks the current version against the latest release
   - Fetches compatibility score from dependabot
   - Shows detailed information for outdated actions
3. Optionally updates workflow files in-place
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
