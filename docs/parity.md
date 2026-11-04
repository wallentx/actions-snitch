# Behavioral parity checks

The tests retain the Bash executable from commit `4fbff7e6d120050204a6989752c61dc62d0bed1e` under `testdata/oracle`. It is a test fixture and is not installed. The original integration harness runs through `make oracle`.

`TestBashGoExecutableParity` executes both the oracle and the production Go runner in separate processes. The Go test executable substitutes local HTTP origins at the existing dependency boundary; the shipped command contains no fixture switches. The harness compares exit status, stdout, stderr, changed file bytes, and file modes. It normalizes only the temporary working-directory prefix in human progress output.

| Contract | Regression coverage |
| --- | --- |
| Workflow/composite discovery and ignore rules | Discovery tests compare Bash paths and retain accessible workflows when an unrelated directory is unreadable. |
| Extended Bash globs | `TestExtendedGlobsAgainstBash` compares alternatives, repetition, negation, classes, and escaping. |
| Exact lines, aliases, and scalar styles | `TestPositionsAgainstYQ` compares coordinates with the oracle's parser. |
| JSON, Markdown, and YAML bytes | `TestReportsAgainstBash` compares exact rendering, and the executable harness checks integrated output. |
| Human/verbose output and update messages | The executable harness compares normal and colored scans/help/updates, current references, unavailable repositories, and gated updates. |
| Loading animation and cursor lifecycle | Terminal and CLI tests verify non-CI animation, CI suppression, and cancellation under the race detector. |
| Major tags, current-tag pins, and non-semver refs | `TestRefPolicies` and the executable harness exercise the update policy. |
| Multiple products in one repository | Release-family tests keep numeric action refs and their SHA pins separate from named bundle/tool releases, including pagination and fail-closed lookups. |
| All-page exact tag lookup | `TestSHAAncestryAndAllTags` checks the full stable tag preference across pages. |
| Ahead/behind/identical/diverged histories | The GitHub tests and executable harness verify the ancestry gate. |
| Missing releases and tag failures | The executable harness covers default-branch fallback; `TestTagPaginationFailsClosed` rejects partial lookup results. |
| Unknown compatibility and configured threshold | `TestGates`, `TestScore`, and `TestAmbiguousBadgeFailsClosed` enforce the policy. |
| Verified creators overriding force | The executable harness and `TestAIUpdateSafetyIntegration` verify that no model or update bypasses the creator gate. |
| Source layout and permissions | Layout tests check exact LF/CRLF/no-final-newline bytes and mode `0640`; metadata tests check read-only protection, xattrs, ACLs, and inherited-ACL exclusion. |
| Multiple documents and Unicode | `TestUnicodeFlowAndDocuments` checks source-span edits. |
| Shared alias scope | Alias tests preserve whole-step aliases while rejecting unrelated reference and input changes. |
| Stable remediation identity | `TestRemediationGroupsUseOriginalPositions` checks multiple groups in one file. |
| All-or-nothing validation and recovery | The update tests inject invalid proposals, external edits, rename failures, and rollback failures. |
| Schema and cached assessment validation | `TestStrictAssessmentSchema` and `TestAssessmentCacheAndFailures` check the fail-closed response boundary. |
| Secret redaction and required evidence | `TestRedaction`, `TestEvidenceRequiredDefinitionsAndImplementation`, and integrated AI tests check the supplied context. |
| Direct provider requests | HTTP fixtures exercise OpenAI, Anthropic thinking blocks, Gemini streamed arrays on Go 1.26/1.27, and Ollama completion/tool metadata. |
| AI-aware force | `TestForceWithoutAIDoesNotValidateProviderEffortOrModel` covers the AI-disabled override. Integrated tests require AI approval and validated remediation when AI is enabled. |
| Config precedence and private setup | The config and setup tests cover aliases, environment overrides, cancellation, exclusive creation, and permissions. |
| PR destination and staged scope | Git tests verify push-origin selection, failed preflight, scoped publishing, and preservation of unrelated index state. |
| HTTP/cache isolation | GitHub tests cover hostile pagination, public requests without credentials, auth-scoped caching, and corrupt entries. |

## Approved provider migration

Direct APIs replace CLI subprocess integration. `codex` maps to `openai`, and `claude` maps to `anthropic`; credentials come from API environment variables. Unsupported CLI-only providers and settings produce explicit migration errors. Setup uses explicit model IDs rather than CLI catalogs. These differences are documented in the README and are not hidden by parity-test normalization.

The Go implementation enforces the full supplied assessment schema locally, including unknown and duplicate field rejection. This enforces the existing fail-closed schema contract consistently across providers. The LLM has no workflow-editing or tool-execution capability.

The Go cache uses a new namespace because its raw JSON API representation differs from the Bash projection cache. Both use the same XDG directory selection and 24-hour expiry policy.

## Validation limits

The local suite uses stored fixtures, local HTTP servers, fake model results, and temporary Git repositories. It does not prove live provider availability, current Marketplace HTML, live GitHub permissions, or a real remote PR publication. The opt-in live AI helper consumes API usage and is separate from the local validation gate.

The AI-aware force policy intentionally differs from the Bash oracle: normal updates use the configured score threshold, and `-f` invokes enabled AI for low or unknown scores. It cannot bypass the AI decision. PR descriptions include committed branch updates as well as changes made by the current invocation.

## Release-family correction

The resolver deliberately corrects the Bash behavior that accepted a repository-wide named-product release as an update for a numeric action reference. For example, `github/codeql-action/init@v4.38.1` must compare against action releases such as `v4.38.2`, rather than `codeql-bundle-v2.27.1`. The dedicated fixtures cover this corrected contract separately from the unchanged Bash oracle.
