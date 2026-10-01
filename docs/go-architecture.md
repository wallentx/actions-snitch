# Architecture

The command composes focused packages around immutable scan snapshots and validated update plans. Its entrypoint supplies signal cancellation and terminal detection; it delegates behavior to `internal/app`.

```text
cmd/actions-snitch
    |
    v
internal/app
    +-- config
    +-- workflow
    +-- github + cache
    +-- compat
    +-- llm + safety
    +-- update
    +-- git
    +-- output
```

| Package | Responsibility |
| --- | --- |
| `app` | The package parses flags, sequences work, and handles interactive setup. |
| `config` | The package loads YAML/JSON, applies environment precedence, and exclusively creates private config files. |
| `workflow` | The package discovers files, evaluates ignore rules, parses YAML, and records original source identities. |
| `github` | The package performs bounded API/public HTTP reads and resolves releases, SHAs, ancestry, tags, and evidence. |
| `compat` | The package normalizes badge scores and applies the compatibility/creator gates. |
| `llm` | The package constructs langchaingo adapters, redacts evidence, and validates assessments without write authority. |
| `safety` | The package shares sensitive-input and secret-reference checks. |
| `update` | The package validates proposals, prepares source edits, proves mutation scope, and publishes with rollback. |
| `git` | The package runs allowlisted argument-array subprocesses and handles branch/PR preflight and publication. |
| `output` | The package renders deterministic findings and deduplicated PR bodies. |
| `cache` | The package stores private, expiring, namespaced entries. |
| `model` | The package supplies shared report and assessment values without services or mutable runtime state. |

## Update flow

```text
Original files
    |
    v
Immutable YAML snapshots + source coordinates
    |
    v
GitHub resolution -> compatibility/creator gates
    |
    +--> optional redacted evidence -> langchaingo -> strict assessment
    |
    v
Authorized version/input plan
    |
    v
Prepared bytes -> parse -> whole-document semantic comparison
    |
    v
Original identity/content recheck -> staged replacements -> publication
    |
    +--> failure -> rollback or retained recovery backup
    |
    v
Reports + optional scoped commit/push/PR
```

Every usage records its file, document index, mapping path, original scalar position, full action path, and current ref. Remediation validation uses those original identities, so inserting an input cannot retarget a later proposal. Conflicting proposals fail before publication.

Version-only changes patch scalar spans rather than marshal the document. This preserves comments, quotes, anchors, Unicode prefixes, CRLF, and missing final newlines. The expected semantic tree expands alias values independently. A shared anchor cannot authorize collateral changes merely because its pointer also appears in the mutable tree.

Publication uses filesystem-root operations, private staging directories, original-content and identity checks, and atomic per-file renames on Linux. macOS publication writes the validated bytes into the existing inode because native ACLs are excluded from ordinary xattr enumeration. It checks source write access without truncation. Linux staging copies ownership, permissions, ACLs, and extended attributes, and rejects concurrent metadata changes. macOS retains inode metadata, creates original-content backups before writing, and includes partially failed writes in rollback. Later failures trigger reverse-order rollback. Rollback errors retain original backups and identify their paths. Concurrent writers can still race between a final check and a rename; the command does not claim a cross-file atomic transaction or replace repository locking.

## External boundaries

The GitHub client separates authenticated API traffic from public raw-source, Marketplace, and badge traffic. API pagination remains on the configured origin. API caches include a credential digest and host; LLM caches include endpoint, provider, model, effort, schema/prompt identity, and sanitized evidence digest. Raw workflow documents and credentials are not persisted in those entries.

The LLM receives two data-only messages and no tools. Local validation rejects schema violations and ambiguous, truncated, or tool-bearing replies. Anthropic content-block handling and Ollama raw-envelope validation compensate for the pinned adapter's response representation. Provider capability checks run only when an assessment backend is required, so forced updates do not initialize or validate a model.

The subprocess boundary permits only `git` and `gh`, uses argument arrays without a shell, and propagates context cancellation. PR preflight resolves origin's push repository before branch or file changes. Publication refuses unrelated staged work and names every file it stages.

## Reference patterns

[Jobscout](https://github.com/wallentx/jobscout/tree/f6981d1b63a556340003c4c97fb21cdd68218d70) supplies the thin `cmd` entrypoint, typed configuration, provider-factory, injected-model testing, and shared Makefile/CI patterns. Actions Snitch keeps its own YAML config shape and mutation-safety contracts. It does not need Jobscout's TUI, database, global runtime state, credential commands, or provider fallback selection.

The module pins langchaingo `v0.1.14` and YAML `v3.0.1`, matching the inspected reference. The Google API dependency pins gax-go `v2.26.1`, whose released stream-parser fix supports Go 1.27. Local schema validation remains authoritative even when a provider offers JSON mode.
