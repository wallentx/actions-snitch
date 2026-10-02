# actions-snitch

`actions-snitch` scans GitHub workflow and composite-action files, reports outdated action references, and can update them locally or open a pull request. The executable is written in Go.

## Installation

Build and install with Go 1.26.1 or newer:

```sh
git clone https://github.com/wallentx/actions-snitch.git
cd actions-snitch
make build
make install                 # Installs to ~/.local/bin/actions-snitch.
# make install PREFIX=/usr/local
```

You can also build directly with `go build -o bin/actions-snitch ./cmd/actions-snitch`. The repository-root `actions-snitch` path is a compatibility symlink to that binary, so existing checkout-based launch paths work after `make build`. The executable does not require Bash, jq, yq, or curl. Git operations require `git`; PR creation requires `gh`. GitHub API authentication uses `GH_TOKEN` or `GITHUB_TOKEN`, or the existing `gh auth` login. Public scans can run without credentials, subject to GitHub's anonymous rate limit.

## Usage

```sh
actions-snitch [-c] [-u] [-s] [-f] [-p] [-b branch] [-o format] [-t] [-v] [-h]
```

The command scans the current directory. These options retain their existing meanings:

| Option | Behavior |
| --- | --- |
| `-c` | The command creates a configuration interactively and exits. This option must appear alone. |
| `-u` | The command updates eligible references in place. |
| `-s` | The command proposes full SHA pins, including pins for already-current release tags. |
| `-f` | The command bypasses compatibility and AI assessment. This option requires `-u` or `-p`. |
| `-p` | The command updates, commits, pushes to origin, and creates a PR. |
| `-b branch` | The command switches to or creates this branch before updating. This option requires `-u` or `-p`. |
| `-o json\|md\|yaml` | The command prints findings in the selected structured format. |
| `-t` | The command updates only actions from Marketplace verified creators. This option requires `-u` or `-p`. |
| `-v` | The command prints discovery and skipped-reference details. |
| `-h` | The command prints help and exits. |

```sh
actions-snitch               # Reports findings without changing files.
actions-snitch -u            # Preserves each reference's existing tag/SHA style.
actions-snitch -s            # Previews full SHA pins.
actions-snitch -u -s         # Applies eligible full SHA pins.
actions-snitch -p -b actions-update
```

## Scanning and version policy

The scanner recursively includes `.yml` and `.yaml` files under `.github/workflows`, plus repository files named `action.yml` or `action.yaml`. Composite discovery prunes `.git` and `node_modules`. Findings retain the exact source line and full action path. Docker references, `main`/`master` references, and deeply nested internal action paths remain skipped. Unavailable private/internal repositories do not interrupt other findings.

Major-only references retain major-only updates unless `-s` is used. Other numeric references follow the existing numeric-prefix comparison policy. The latest release supplies the target; when no release exists, the repository's default branch supplies it.

| Current reference | `-u` | `-u -s` |
| --- | --- | --- |
| Release tag | The update retains tag style, including major-only tags. | The update uses the latest release's full SHA. |
| Full SHA | The update uses a full SHA. | The update uses a full SHA. |

A SHA update requires a resolved 40-character commit identity and a GitHub comparison proving that the target is strictly ahead. Identical, behind, and diverged targets remain unchanged, including with `-f`. The commit distance uses `ahead_by`, including counts beyond one page of commits. Failed resolution or comparison produces a warning and skips the update.

The exact-tag lookup searches every tag page and compares commit identities. When multiple tags match, it prefers the lowest full stable semantic version over moving major/minor aliases. A successful lookup with no match differs from a lookup failure. A tag identifies an exact commit; it does not identify the first release containing an ancestor.

## Ignore rules

The scanner reads `.snitchignore` from the working directory. `.gitignore` does not control scanning.

```text
# Paths are relative to the scan directory.
.test/
fixtures with spaces/
.github/workflows/example-*.yml
!.github/workflows/example-maintained.yml
```

Patterns use Bash conditional-glob semantics. `*` spans `/`; `?`, bracket classes, and extended groups such as `@(fixtures|examples)/*` are supported. A trailing `/` includes descendants. Leading `/` or `./` is optional. Blank lines and lines beginning with `#` are ignored. A leading `!` re-includes a path, and the last matching rule wins.

## Reports and compatibility

Terminal reports use terminfo colors, and non-CI scans display a loading animation that restores the cursor on completion or cancellation. Verbose scans identify current references and unavailable repositories.

Human and Markdown reports mark Marketplace verified creators with `☑️`. JSON and YAML preserve action names and include `verified_creator` as a boolean. Verification uses the action's Marketplace listing rather than an owner allowlist. The `-t` gate also applies to forced updates.

Structured output groups findings under the scanned repository name and prints nothing when there are no findings. Every finding includes `file`, `line`, `action`, `repository`, `current`, `latest`, `current_tag`, `latest_sha`, `commits_since`, `update_ref`, `compatibility_score`, `verified_creator`, and `release_notes`. Unavailable SHA metadata and release-note links are `null` in JSON/YAML. Markdown links the repository heading to its origin remote when available.

A known compatibility score of at least 80 normally permits an update. Unknown, invalid, or lower scores require AI approval or `-f`. Raising the enabled AI threshold raises the review threshold; lowering it never lowers the score floor of 80. A badge containing conflicting scores is treated as unknown.

## AI-assisted updates

AI is disabled by default. Enabled AI runs only during updates that require assessment. The integration calls provider APIs directly through langchaingo and does not launch provider CLIs, grant model tools, or execute repository evidence.

| Provider setting | Credential environment | Legacy alias |
| --- | --- | --- |
| `openai` | `OPENAI_API_KEY` | `codex` |
| `anthropic` | `ANTHROPIC_API_KEY` | `claude` |
| `gemini` | `GEMINI_API_KEY` or `GOOGLE_API_KEY` | `googleai` |
| `openrouter` | `OPENROUTER_API_KEY` | No alias applies. |
| `ollama` | The local server uses `OLLAMA_HOST`, which defaults to `http://127.0.0.1:11434`. | No alias applies. |

CLI login sessions and subscription authentication do not supply API credentials. Existing `cursor`, `opencode`, `copilot`, and `antigravity` settings require an explicit provider migration. Configuration stores no credentials, and setup does not ask for them. API providers can bill requests separately from CLI subscriptions.

`OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, and `OPENROUTER_BASE_URL` can select explicit compatible endpoints. Endpoint URLs cannot contain credentials, queries, or fragments. The command does not automatically switch providers on failure.

Run setup with `actions-snitch -c`. The terminal wizard uses arrow keys and Enter for selection, and `/` filters the provider model list. It fetches models using the selected provider's API credentials, offers retry or manual entry when a catalog is unavailable, and shows a final review before saving. Ctrl+C cancels from any screen or during catalog loading without writing a file. Piped input retains the text-prompt workflow.

You can also create this configuration:

```yaml
ai:
  enabled: false
  provider: openai
  model: your-api-model-id
  threshold: 80
  issue_search: auto
```

The default path is `$XDG_CONFIG_HOME/actions-snitch/config.yaml`, or `~/.config/actions-snitch/config.yaml`. `ACTIONS_SNITCH_CONFIG` selects another file. JSON configuration remains valid as a YAML subset, and the legacy `ai.backend` key remains accepted. Setup creates the file with mode `0600`, refuses to overwrite an existing file or symlink, and writes nothing when cancelled.

| Environment variable | Override |
| --- | --- |
| `ACTIONS_SNITCH_AI` | The value controls whether AI is enabled. |
| `ACTIONS_SNITCH_AI_PROVIDER` | The value selects the API provider. |
| `ACTIONS_SNITCH_AI_MODEL` | The value selects the provider's model ID. |
| `ACTIONS_SNITCH_AI_EFFORT` | The value selects a supported thinking level. |
| `ACTIONS_SNITCH_AI_THRESHOLD` | The value selects an integer threshold from 0 through 100. |
| `ACTIONS_SNITCH_CONFIG` | The value selects the configuration file. |

The pinned Anthropic adapter supports `ai.effort: low|medium|high` for models it recognizes as reasoning-capable. Unsupported provider/model effort settings produce a migration error when an assessment backend is required. OpenAI effort settings are rejected because the pinned adapter cannot transmit them reliably. Setup lists models from provider APIs rather than CLI catalogs, and retains an explicit model-ID option for custom or unavailable catalogs. `issue_search` accepts `auto`, `always`, and `never`; both `auto` and `always` include the bounded upstream search.

The evidence includes complete redacted local documents, current and proposed action definitions, release notes, changelogs, compare commits, and optional issue results. All `env` values, sensitive `with` inputs, and references to the secrets context are redacted. Composite evidence includes at most four referenced implementation files per version, with a 12,000-character bound per file and explicit missing/truncated/omitted indicators. The command reads this source without executing it.

Every response must satisfy the assessment schema and return `allow`, `review`, or `block`. Unknown fields, duplicate keys, incomplete responses, unauthorized tool requests, low-confidence approval, missing required evidence, and provider errors fail closed. Cached assessments undergo the same validation.

An approved remediation can only set or remove non-sensitive scalar `with` inputs on the exact affected action mapping. Validation checks the scanned file, original line, full action path, current version, input name, and exact current value. Arbitrary YAML patches, permissions, triggers, environment variables, shell commands, cross-action changes, and sensitive inputs are rejected.

The planner validates changes against immutable source snapshots, prepares all affected files, and checks the complete resulting YAML before publishing. Version-only edits preserve layout, comments, quoting, line endings, and final-newline state. Changes that would affect unrelated alias consumers fail closed. Publication checks write permission and rejects source files changed since scanning. On Linux and macOS, it preserves ownership, permissions, ACLs, and extended attributes; metadata that cannot be preserved causes the update to fail before publication. Linux uses atomic per-file replacement. macOS writes into the existing inode to preserve its native ACLs, matching the original copy behavior. Both paths attempt rollback after a publication failure, and an unsuccessful rollback retains a recovery backup whose path appears in the error. Cross-file publication is not a filesystem-wide atomic transaction.

Forced updates bypass the model and evidence collection entirely. They apply no AI remediation and still respect verified-creator, ancestry, and SHA-resolution gates.

## Branches and pull requests

Plain `-u` permits existing local changes and can run outside a Git repository. Branch switching requires a clean worktree. PR mode requires a clean worktree and resolves origin's push URL and that repository's default branch before changing branches or files. A fork push URL therefore keeps the PR in the fork. Interactive PR mode can prompt for a branch when `-b` is absent.

PR mode stages only successfully updated files, commits once, pushes to origin, and creates the PR with an explicit repository, head, and base. It refuses unrelated staged work. The body deduplicates repeated updates and includes release notes, changelog and commit details, compatibility badges, SHA metadata, and validated AI investigation/remediation details where applicable.

## Cache and GitHub hosts

The cache lives under `$XDG_CACHE_HOME/actions-snitch`, or `~/.cache/actions-snitch`, and entries expire after 24 hours. Go entries use a separate namespace so projected Bash cache values cannot be mistaken for raw API responses. API entries are isolated by host and credential identity. The cache stores metadata and sanitized assessments, not API credentials or raw workflow documents. Cache files use owner-only permissions.

`GH_HOST` selects the GitHub API host. `github.com` and `*.ghe.com` use `GH_TOKEN` before `GITHUB_TOKEN`; Enterprise Server hosts use `GH_ENTERPRISE_TOKEN` before `GITHUB_ENTERPRISE_TOKEN`. The fallback `gh auth token` lookup names the host explicitly. API pagination cannot move credentials to another origin. Public Marketplace, badge, and raw-source requests use a separate unauthenticated client.

## Development

Run `make` or `make help` to list the available targets. Progress output uses terminal colors automatically; `COLOR=always`, `COLOR=never`, and `NO_COLOR=1` control that presentation.

| Command | Behavior |
| --- | --- |
| `make build` / `make run ARGS="-o json"` | The targets build the local executable and optionally run it. `make run` defaults to help. |
| `make fix` / `make qa` | The targets apply formatting and module tidying, or check formatting, modules, and static analysis without editing sources. |
| `make check` / `make full-check` | The standard gate runs formatting/import checks, module verification, vet, staticcheck, golangci-lint, tests, race tests, gosec, and a build. The full gate adds live vulnerability checking and the original Bash suite. |
| `make coverage` / `make coverage-html` | The targets print function coverage and optionally generate `coverage.html`. |
| `make release VERSION=v1.0.0 RELEASE_GOOS=linux RELEASE_GOARCH=amd64` | The target creates a local release archive and a SHA-256 checksum under `dist`. |

Individual targets include `fmt-check`, `fmt-fix`, `imports-check`, `imports-fix`, `tidy-check`, `tidy-fix`, `vet`, `staticcheck`, `golangci-lint`, `gosec`, and `govulncheck`. `make security` runs both security scanners. `make tools` installs the pinned helper versions into `.tools/bin`.

`PKGS`, `TIMEOUT`, and `TEST_FLAGS` customize test runs. For example, `make test PKGS=./internal/git TEST_FLAGS="-count=1"` runs the Git tests without using cached results. `test-short`, `test-verbose`, and `race` provide common variants. The timeout defaults to 300 seconds.

The race target checks toolchain support and prints an explicit skip on unsupported targets such as `android/arm64`. Supported targets still run race tests; other setup or test failures remain errors. Termux users can run the rest of `make all` locally, with race coverage supplied by a supported host or CI.

`BIN`, `CMD_PATH`, `GO`, `GO_LDFLAGS`, and `TOOLS_BIN` customize build/tool locations and options. `make install` retains the `PREFIX` and `DESTDIR` behavior described above. `make build-info` prints the executable's embedded Go module and VCS information.

Release builds disable CGO and produce `.tar.gz` archives, or `.zip` archives for Windows. Windows packaging requires `zip` or `bsdtar`. Each archive contains the executable, README, and a `VERSION` file; it also includes `LICENSE` when the repository provides one. `VERSION` labels the archive and manifest, and defaults to `RELEASE_TAG` or Git's current description. The binary retains Go's embedded VCS information. `DIST_DIR` selects the output directory. `make clean` removes the selected binary and coverage files while preserving downloaded tools, release archives, tracked files, and symlinks.

Gosec uses the pinned Go 1.26.6 toolchain because its analyzer does not support the host Go 1.27 standard library. `GOSEC_GOTOOLCHAIN` can select another compatible toolchain. Vulnerability checks contact the public Go vulnerability database.

The differential suite runs the preserved Bash oracle and the production Go runner as separate processes against the same local HTTP fixtures. Bash, jq, yq, curl, and git are required for oracle tests; production use does not require the parsing utilities. The original integration suite remains available through `make oracle`. Tests never use a developer's live model credentials.

The [architecture document](docs/go-architecture.md) describes package ownership and the [parity matrix](docs/parity.md) maps behavior to regression tests. Jobscout supplies the architectural reference for the thin command, typed configuration, direct langchaingo adapters, and shared local/CI validation.
