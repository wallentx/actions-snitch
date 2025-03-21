# actions-snitch

Like dependabot, but in bash for local execution. This tool helps you identify and update outdated GitHub Actions in your workflow files.

## Features

- 🔍 Scans all workflow files for outdated GitHub Actions
- 📍 Shows exact line numbers where actions are used
- 🚀 Shows compatibility scores between versions
- 💾 Caches API responses for faster subsequent runs (24h TTL)
- 🔄 Can automatically create PRs to update actions
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
actions-snitch [-u] [-p] [-v] [-h]
```

### Options

- `-u` Update outdated actions by creating a new branch and committing changes
- `-p` Push changes and create a pull request (requires -u)
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

## How It Works

1. Scans all `.yml` files in `.github/workflows/`
2. For each GitHub Action found:
   - Records the exact line number where it's used
   - Checks the current version against the latest release
   - Fetches compatibility score from dependabot
   - Shows detailed information for outdated actions
3. Optionally creates PRs to update outdated actions
4. Skips internal/private actions and docker references

## Cache

The tool caches API responses in `~/.cache/actions-snitch/` (or `$XDG_CACHE_HOME/actions-snitch/`) to:
- Reduce API calls to GitHub
- Speed up subsequent runs
- Avoid rate limiting

Cache entries expire after 24 hours.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request. 