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
| All-page exact tag lookup | `TestSHAAncestryAndAllTags` checks the full stable tag preference across pages. |
| Ahead/behind/identical/diverged histories | The GitHub tests and executable harness verify the ancestry gate. |
| Missing releases and tag failures | The executable harness covers default-branch fallback; `TestTagPaginationFailsClosed` rejects partial lookup results. |
| Unknown compatibility and threshold floor | `TestGates`, `TestScore`, and `TestAmbiguousBadgeFailsClosed` enforce the policy. |
| Verified creators overriding force | The executable harness and `TestAIUpdateSafetyIntegration` verify that no model or update bypasses the creator gate. |
| Source layout and permissions | Layout tests check exact LF/CRLF/no-final-newline bytes and mode `0640`; metadata tests check read-only protection, xattrs, ACLs, and inherited-ACL exclusion. |
| Multiple documents and Unicode | `TestUnicodeFlowAndDocuments` checks source-span edits. |
| Shared alias scope | Alias tests preserve whole-step aliases while rejecting unrelated reference and input changes. |
| Stable remediation identity | `TestRemediationGroupsUseOriginalPositions` checks multiple groups in one file. |
| All-or-nothing validation and recovery | The update tests inject invalid proposals, external edits, rename failures, and rollback failures. |
| Schema and cached assessment validation | `TestStrictAssessmentSchema` and `TestAssessmentCacheAndFailures` check the fail-closed response boundary. |
| Secret redaction and required evidence | `TestRedaction`, `TestEvidenceRequiredDefinitionsAndImplementation`, and integrated AI tests check the supplied context. |
| Direct provider requests | HTTP fixtures exercise OpenAI, Anthropic thinking blocks, Gemini streamed arrays on Go 1.26/1.27, and Ollama completion/tool metadata. |
| Force bypass | `TestForceDoesNotValidateProviderEffortOrModel` and integrated tests verify that force collects no AI evidence and applies no remediation. |
| Config precedence and private setup | The config and setup tests cover aliases, environment overrides, cancellation, exclusive creation, and permissions. |
| PR destination and staged scope | Git tests verify push-origin selection, failed preflight, scoped publishing, and preservation of unrelated index state. |
| HTTP/cache isolation | GitHub tests cover hostile pagination, public requests without credentials, auth-scoped caching, and corrupt entries. |

## Approved provider migration

Direct APIs replace CLI subprocess integration. `codex` maps to `openai`, and `claude` maps to `anthropic`; credentials come from API environment variables. Unsupported CLI-only providers and settings produce explicit migration errors. Setup uses explicit model IDs rather than CLI catalogs. These differences are documented in the README and are not hidden by parity-test normalization.

The Go implementation enforces the full supplied assessment schema locally, including unknown and duplicate field rejection. This enforces the existing fail-closed schema contract consistently across providers. The LLM has no workflow-editing or tool-execution capability.

The Go cache uses a new namespace because its raw JSON API representation differs from the Bash projection cache. Both use the same XDG directory selection and 24-hour expiry policy.

## Validation limits

The local suite uses stored fixtures, local HTTP servers, fake model results, and temporary Git repositories. It does not prove live provider availability, current Marketplace HTML, live GitHub permissions, or a real remote PR publication. The opt-in live AI helper consumes API usage and is separate from the local validation gate.
