# commiter-cli Software Requirements Specification

[日本語](SOFTWARE_REQUIREMENTS_SPECIFICATION.md) | English

| Item | Description |
| --- | --- |
| Document status | Draft v0.1 |
| Created | 2026-08-29 |
| Scope | v1 for personal use |
| Target implementation | A single CLI binary primarily implemented in Go, including Tree-sitter CGo bindings |

## 1. Purpose and Background

Launching an interactive coding agent every time to analyze diffs, split commits, create commits, and push them incurs reasoning latency and token usage.

This system mechanically performs Git operations and diff preprocessing, extracts syntactic facts through Tree-sitter-based syntax-aware structural analysis, and delegates only the interpretation of change meaning and intent and the generation of the commit plan to a local LLM. This avoids requiring a low-parameter quantized model to perform syntax recognition itself, reducing input size and the risk of external data transmission while preserving generation quality and speed.

## 2. Goals

- Reproducibly determine the target scope from Git staged, unstaged, and untracked changes.
- Structure mechanically observable facts from Git and syntax trees, then have the local LLM judge only the meaning and intent of changes and file grouping to generate a Conventional Commits plan.
- Display the plan, verification commands, and sensitive-file classification results, and by default mutate Git state only after explicit confirmation. Always exclude clearly sensitive files automatically, and request confirmation before reading only ambiguous sensitive candidates.
- Allow commit confirmation and push confirmation to be skipped independently through global configuration or CLI flags, but never allow skipping the read confirmation for ambiguous sensitive candidates or the push confirmation for commits containing files that were included as sensitive candidates in the current run. Clearly sensitive files cannot be overridden by configuration or CLI and must never be included in commiter's commit targets.
- Split commits by purpose and perform exactly one safe push after all commits complete.
- Never send diffs used for generation outside the local machine.

## 3. Non-goals

v1 does not target Windows or Linux, a GUI, cloud LLMs, a llama.cpp backend, a Homebrew Tap, hunk-level splitting, semantic static analysis such as type resolution, symbol resolution, control-flow graphs, or data-flow analysis, recursion into submodules, or distribution for multiple users.

During the repository initialization phase, only the README and this specification are deliverables. Implementation code, a Go module, LICENSE, CI, release automation, and remote configuration are not created during that phase.

## 4. Users and Terminology

**User**: A developer who runs the CLI in a Git repository they manage.

**pathspec**: A relative path or path pattern interpreted by Git.

**target change**: A file-level change selected after applying pathspec, Git status, untracked-file safety evaluation, sensitive-file classification, and user confirmation. For tracked files, existing staged/unstaged boundaries are not used for target selection; the complete change from HEAD to the final working-tree state is targeted. Excluded files are not included in target changes and are not assigned file IDs.

**structural evidence**: Facts mechanically observed from Git diffs and syntax trees. This may include path, status, hunks, syntax node kinds, declaration names, enclosing declarations, import/export statements, call expressions, HTML tags/attributes, CSS selectors/properties, and similar facts, but must not include semantic classifications or grouping recommendations such as change intent, functional relationships, `test_for`, or `should_group`.

**commit plan**: JSON that assigns target changes to one or more commits and defines the message for each commit.

**clearly sensitive file**: A path that is always automatically excluded, prioritizing leak prevention over false positives, such as `.env`, `.env.*`, `*.pem`, `*.key`, known SSH private-key names, and known paths that explicitly indicate credentials or secrets. v1 provides no configuration or CLI override and does not include these files in commiter analysis, file-ID assignment, or commit targets.

**sensitive candidate**: A path whose name or location suggests that it may contain sensitive data while still plausibly being an ordinary configuration file. User confirmation is required before reading its contents, and the file is included in local analysis and commit targets only when approved. Even after approval, raw diffs and raw values must not be displayed in terminal or JSON output.

**opaque file**: A target file whose contents are not supplied to the LLM because it is binary, large, or otherwise unsuitable. Because content-based semantic judgment is unavailable, only for such a file may path, status, size, type, and other metadata be used as supporting evidence for grouping.

**change_hash**: A SHA-256 computed from a normalized record of a target change to verify that the change analyzed by the LLM is identical to the change immediately before commit. It includes status, old/new path, old/new mode, HEAD-side object identity, and the identity of the final working-tree state.

**canonical repo path**: The real path obtained by resolving symlinks in the absolute path of the repository root. It is used as the scope key for verification trust and to determine whether a cwd is inside the repository.

**verification definition**: The normalized representation of the verification process that commiter will actually launch. It includes source type, an ordered command list, and each command's name, normalized repository-root-relative cwd, and full argv. For `package.json` autodetection, it additionally includes the manifest path, script name, and exact script body, as well as the name and body of any pre/post script that the package manager implicitly executes and cannot disable.

**verification trust**: Stored state indicating that the user has approved the SHA-256 of a verification definition for a particular canonical repo path. Trust approves the verification command definition; it does not guarantee the safety of source code, dependencies, lockfiles, or other code indirectly executed by verification.

## 5. Assumptions and Constraints

The target OS for v1 is macOS 14 or later, the target architecture is Apple Silicon, and the reference machine is an M3 Mac with 16 GB of memory.

The only external runtime dependencies are system Git and Ollama. Syntax-aware structural analysis uses the official `github.com/tree-sitter/go-tree-sitter` package and target-language grammars, embedding Tree-sitter's C implementation into the single CLI binary via CGo. CGo must not be extended beyond the Tree-sitter boundary, and the implementation must not require an external parser executable, runtime shared grammar, Oniguruma, or a cloud-LLM fallback.

The default model is `qwen3.5:4b-q4_K_M`; its size is treated as approximately 3.4 GB based on official distribution information. [Qwen3.5 model information](https://ollama.com/library/qwen3.5%3A4b-q4_K_M/blobs/81fb60c7daa8)

## 6. Normal Flow

1. The CLI checks the Git repository state, HEAD, branch, index lock, merge, rebase, cherry-pick, revert, conflicts, and detached HEAD state.
2. The CLI obtains the path list and staged/unstaged state from NUL-delimited Git status output. Stage state is metadata for diagnostics and protection, not a boundary for target selection.
3. The CLI applies pathspec, excludes ignored files, and targets tracked files at file granularity from HEAD through the final working-tree state. A file containing both staged and unstaged changes, including a partially staged file, is targeted as a whole file. A rename is represented as one change with an old path and new path and receives one file ID. A symlink is handled as Git-tracked link information without following the link target, and a submodule is handled only as the parent repository's pointer update.
4. The CLI performs sensitive classification using paths only and always automatically excludes clearly sensitive files. No override is provided to target clearly sensitive files. Sensitive candidates are confirmed before their contents are read, and only approved candidates are passed to local analysis.
5. For target changes remaining after sensitive classification, the CLI generates Git metadata and Tree-sitter structural evidence. v1 parses Go, JavaScript, JSX, TypeScript, TSX, Python, Rust, HTML, and CSS. A text file in an unsupported language or one for which syntax parsing fails falls back to raw diff plus Git metadata. Opaque files are passed to plan generation as metadata only, without their contents. The mechanical layer must not generate semantic relationships or grouping recommendations such as source/test, docs/source, or same-feature relationships.
6. The CLI assigns a file ID and change_hash to every target change, constructs LLM input at 8K, 16K, then 32K context levels, and performs hierarchical summarization if the allowed limit is exceeded.
7. Ollama returns a constrained JSON commit plan. Before any Git mutation, the CLI validates the schema, file assignment, and safety conditions.
8. The CLI displays the full plan and exclusion list and prompts exactly once with `Create these N commits? [y/r/N]`.
9. Only after user approval, the CLI runs verification once against the entire working tree based on an approved verification definition.
10. After verification and immediately before commit creation begins, the CLI revalidates HEAD, target-change change_hash values, the index, and the target untracked set. Changes limited to ignored output are allowed. If the tracked working tree, index, or target untracked set changed, the CLI must not start commits or push. It leaves verification-generated working-tree changes intact, restores the index to its state at the start of the run, displays the changed paths, and prompts `Re-analyze changed state? [y/N]`. If the user selects `y`, processing returns to step 1 using the current Git state; if declined, the CLI exits with code 4.
11. Only when revalidation matches does the CLI create file-level commits in plan order. Immediately before staging each commit, it recalculates the current change_hash of every unprocessed file ID assigned to that commit and verifies it against the analysis snapshot. Git hooks are generally allowed as part of normal Git operations, and a hook may modify the contents of paths corresponding to file IDs assigned to the current commit. After each commit is created, the CLI verifies that the file set in the diff against the parent exactly matches the file IDs assigned to that commit. If an unassigned file is included or an assigned file is missing, later commits and push are stopped.
12. After all commits succeed, the CLI displays the actually resolved `<remote>/<branch>` and normally prompts `Push to <remote>/<branch>? [y/N]`. Under ordinary Git push semantics, outgoing commits that existed before the current run may also be included in the push.
13. After push succeeds, the CLI displays per-phase metrics and the created commit hashes.

## 7. Public CLI

The normal invocation form is:

```text
commiter [flags] [--] [pathspec...]
```

With no arguments, the CLI targets all tracked-file changes from HEAD through the working tree regardless of stage state, plus untracked files that pass the safety checks.

When pathspec is specified, Git pathspec semantics limit the target scope.

Subcommands are `setup [--update-model]`, `doctor`, `config init --global|--repo`, `config show [--effective]`, `config path --global|--repo`, `trust list`, `trust revoke <repo>`, and `version`.

Temporary override flags are `--dry-run`, `--no-push`, `--no-confirm-commit`, `--no-confirm-push`, `--language en|ja`, `--model`, `--record-metrics`, and `--json`.

No override flag is provided for targeting clearly sensitive files.

`--json` is limited to `--dry-run` and read-only subcommands and is rejected when combined with an invocation that performs commits or push.

`--dry-run` displays the normally targeted diff, exclusions, plan, planned verification, and push destination, but does not modify the index, commits, or remote. For sensitive candidates, even when approved, it must not display raw diffs or raw values in terminal or JSON output and may display only path, status, and other non-sensitive metadata.

## 8. Configuration and State

Configuration precedence is `CLI > repo configuration > global configuration > built-in defaults`.

Global configuration is stored at `$XDG_CONFIG_HOME/commiter/config.toml`, or `~/.config/commiter/config.toml` when the environment variable is unset.

Repository configuration is `.commiter.toml` at the repository root.

State and trust are stored under `$XDG_STATE_HOME/commiter/`, or `~/.local/state/commiter/` when the environment variable is unset.

Persistent metrics are disabled by default and are written to `metrics.jsonl` under the state directory only when explicitly configured or when `--record-metrics` is supplied.

Commit confirmation and auto push may be changed only by global configuration or explicit CLI flags. Additional patterns for sensitive classification and the Ollama endpoint may be changed only in global configuration. Protection of out-of-scope staged contents and staged selection is an immutable v1 invariant and cannot be disabled by configuration or CLI.

The entire verification configuration is repository-scoped. Verification commands, autodetection, and timeout may be specified only in repository configuration. Verification must not be configured globally.

Glob, language, model, and classification-helper settings may be overridden in repository configuration.

If repository configuration attempts to modify a safety setting, the CLI must not ignore it and continue; it must stop with a configuration error.

### 8.1 Configuration Schema

Configuration files use TOML and the following v1 schema:

| key | type | default | allowed sources |
| --- | --- | --- | --- |
| `schema_version` | integer | `1` | global, repo |
| `commit.language` | string | `"en"` | global, repo, CLI |
| `commit.confirm` | boolean | `true` | global, CLI |
| `push.enabled` | boolean | `true` | global, CLI |
| `push.confirm` | boolean | `true` | global, CLI |
| `llm.model` | string | `"qwen3.5:4b-q4_K_M"` | global, repo, CLI |
| `llm.endpoint` | string | `"http://127.0.0.1:11434"` | global |
| `llm.context` | string | `"auto"` | global, repo |
| `llm.max_context_tokens` | integer | `32768` | global, repo |
| `analysis.untracked` | string | `"auto-safe"` | global |
| `analysis.include` | string array | `[]` | global, repo |
| `analysis.exclude` | string array | `[]` | global, repo |
| `verification.autodetect` | boolean | `true` | repo |
| `verification.timeout_seconds` | integer | `600` | repo |
| `verification.commands` | array of tables | unset | repo |
| `metrics.persist` | boolean | `false` | global, CLI |
| `safety.additional_sensitive_patterns` | string array | `[]` | global |

Each `verification.commands` element requires `name`, `argv` (string array), and `cwd` (string). `verification.commands` may be specified only in repository configuration and must contain at least one command. An explicit empty array is a configuration error.

If `verification.commands` is explicitly present in repository configuration, that command list is used. Only when it is absent is `verification.autodetect` evaluated; when true, commands are autodetected from the repository-root `package.json`, and when false the result is `Verification: none`.

Allowed values for `llm.context` are `"auto"`, `"8k"`, `"16k"`, and `"32k"`.

Allowed values for `llm.max_context_tokens` are 8192, 16384, and 32768. With `"auto"`, the CLI selects progressively from 8K, 16K, and 32K up to the configured maximum.

`analysis.include` and `analysis.exclude` use doublestar globs and are restricted to repository-root-relative paths.

Array-valued configuration overrides replace the prior value; they do not append elements.

`include` and `exclude` are restricted to repository-root-relative paths. Before use, a verification command's `cwd` is resolved through symlinks and compared with the canonical repo path; a cwd outside the repository root is rejected as a configuration error.

`safety.additional_sensitive_patterns` does not replace built-in clearly sensitive patterns. It only adds patterns to the automatically excluded set.

`provider = "ollama"`, `think = false`, `stream = false`, and `keep_alive = 0` are fixed v1 values and cannot be relaxed through configuration.

Unknown keys, unsupported `schema_version` values, type mismatches, or keys declared in a configuration source not allowed by the schema cause exit code 2. Therefore, any `verification.*` key in global configuration is rejected as a configuration error.

## 9. Functional Requirements

### FR-001 Repository State Validation

Before starting, the CLI must check the target repository root, HEAD, branch, index lock, and in-progress Git operation state, and must stop without modifying Git when the state is ambiguous.

### FR-002 Determining the Change Scope

The CLI must retrieve staged, unstaged, and untracked changes separately to understand their state, and must apply Git pathspec when one is provided. Staged/unstaged boundaries of tracked files must not be used for target selection; a targeted tracked file must be treated as one file change containing the complete change from HEAD through the final working-tree state.

A change recognized by Git as a rename must not be split into separate deletion and addition file IDs. It must be represented as one change with old and new paths and receive one file ID.

### FR-003 File Classification

After sensitive classification and exclusion are complete, the CLI must assign every target change a stable file ID, status, old path, new path, language, size, binary classification, and change_hash. For changes other than renames, the non-applicable one of old path and new path may be normalized to null. Automatically excluded or user-rejected files must not receive file IDs.

change_hash must be the SHA-256 of the UTF-8 bytes of canonical JSON containing schema version `1`, status, old path, new path, old mode, new mode, HEAD-side Git object identity, and final working-tree kind and identity. Non-applicable fields are null, and object key order is fixed.

For the final working-tree identity, regular and binary files use the SHA-256 of the final file bytes; symlinks use the SHA-256 of the symlink target byte sequence without following the target; submodules use the gitlink commit OID stored by the parent repository; deletion uses null. The HEAD-side object identity of an untracked file is null.

### FR-004 Safe Handling of Untracked Files

The default mode is `auto-safe`. Ordinary text files must be treated as additions. Large or binary files must be treated as opaque files whose content is not sent to the LLM; only metadata is supplied to plan generation.

In v1, an untracked ordinary text file whose final working-tree size is 64 KiB or greater is classified as large.
Binary files are opaque regardless of size.

Because content-based semantic judgment is unavailable for an opaque file, only for that file may path, status, size, type, and other metadata be used as supporting evidence for grouping.

### FR-005 Syntax-aware Structural Analysis

For target text files remaining after sensitive classification, the CLI must map diff hunks to the target file's syntax tree and generate mechanically observable structural evidence in addition to Git facts, such as the syntax node kind containing the changed region, declaration name, enclosing declaration, import/export, and call expressions.

The Tree-sitter-supported languages in v1 are Go, JavaScript, JSX, TypeScript, TSX, Python, Rust, HTML, and CSS. For HTML, tags and attributes may be structural evidence. For CSS, selectors and declaration properties may be structural evidence.

For a text file in an unsupported language, unsupported by the available grammar, or for which adequate structural evidence cannot be obtained because of syntax errors or other reasons, the CLI must fall back for that file only to raw diff plus Git metadata and continue processing. A file or the entire run must not be excluded solely because syntax parsing failed.

The mechanical layer must not generate semantic relationships or grouping recommendations such as change intent, feature, source/test, docs/source, same logical change, `test_for`, `related_to`, or `should_group`.

### FR-006 Input Size Control

The CLI must prioritize structural evidence and required diff hunks when constructing LLM input.

When `llm.context = "auto"`, context tiers are selected in order from 8K to 16K to 32K, bounded by `llm.max_context_tokens`. When `llm.context` is fixed to `"8k"`, `"16k"`, or `"32k"`, that tier is the maximum, and the CLI must not automatically promote to a larger context tier.

Before selecting a context tier, the CLI must treat the UTF-8 byte length of the final prompt as a conservative upper bound on input token count, then add a fixed 256 tokens for the chat template and output-reserve tokens calculated as `max(1024, 48 × target file count)`.
The CLI selects the smallest allowed context tier that can contain the resulting total.

If the allowed context maximum is exceeded, the CLI must perform hierarchical summarization in file, hunk, then chunk order and, for files supported by syntax parsing, preserve structural evidence in the final plan input.
If the total still exceeds the allowed context maximum after summarization, or the complete set of target file IDs, change_hash values, and structural evidence cannot be preserved, the CLI must stop without calling the LLM or modifying Git.

### FR-007 Hierarchical Summarization

Hierarchical summarization must preserve the complete set of target file IDs, old/new paths, statuses, and change_hash values so that missing file assignments remain detectable after summarization.

### FR-008 Commit Plan Generation

The CLI must send structured input to the local Ollama API and obtain a file-level commit plan.

### FR-009 LLM Generation Failure and Output Validation

There is exactly one initial generation. The retry budget for transport errors or timeouts is one retry total, shared across the initial generation and repair.

Every time candidate output is received from the LLM, the CLI must revalidate the JSON schema, complete assignment of target file IDs, sensitive-value constraints, and all other safety conditions before any Git mutation. Git must not be modified while the candidate output is invalid.

If invalid JSON, a JSON-schema violation, missing/duplicate/out-of-range target file IDs, or a sensitive-value match defined by SR-010 is detected, the CLI performs exactly one automatic repair.
If multiple violations are detected simultaneously, they are combined into one repair request and must not increase the repair count.

The repair request must include the original normalized input, the candidate output, and violation reasons that do not contain the sensitive values themselves, and must explicitly treat the candidate output as untrusted data rather than instructions.
The repaired candidate is fully revalidated as a new candidate. If any violation remains, the CLI performs no additional repair and exits with code 5 without modifying Git.

Therefore, an individual plan-generation cycle may make at most three Ollama calls total: the initial generation, an optional repair, and the single shared transport retry.

### FR-010 File-level Splitting

The LLM must infer the meaning and intent of each change from the diff and structural evidence and group files by purpose when it judges them to belong to the same logical change. The mechanical layer must not merge, split, or reorder otherwise valid LLM grouping based on heuristics such as source/test relationships, directories, filenames, imports, or dependencies.

Opaque files are an exception because their contents cannot be used for semantic judgment; path, status, size, type, and other metadata may be used as supporting evidence for grouping.

The CLI must not assign the same file ID to multiple commits. Even when a target file contains both staged and unstaged changes, it must not split the file at hunk granularity and must include the file's complete change in one commit. A rename is one file ID with old/new paths and must not be split into deletion and addition.

### FR-011 Plan Confirmation

The CLI must display the order, message, assigned files, and excluded files for all commits and accept approval, regeneration, or rejection through a single confirmation interaction.

If the user selects `r`, the CLI accepts a short supplemental instruction and reruns inference. Direct editing of the plan is not provided.

### FR-012 Running Verification

After plan approval and before commits, the CLI must run verification commands exactly once against the entire working tree.

Verification configuration is repository-scoped. `verification.commands`, `verification.autodetect`, and `verification.timeout_seconds` must not be specifiable in global configuration.

When repository configuration explicitly provides `verification.commands`, the CLI must use that command list. `verification.commands` must contain at least one command; an explicitly empty array is a configuration error causing exit code 2.

Only when `verification.commands` is absent does the CLI evaluate the effective `verification.autodetect`. If `verification.autodetect=true`, autodetection is performed; if false, no verification is run and the CLI reports `Verification: none`.

The only manifest eligible for v1 autodetection is `package.json` at the repository root. Only existing `lint`, `typecheck`, `test`, and `build` scripts are candidates, in that order. If the package manager cannot be uniquely determined from repository-root information, the CLI must not execute an autodetected command. Standard Go, Rust, and Python commands are not inferred automatically.

Verification commands in repository configuration must be specified as argv arrays, not shell strings. Verification commands should be configured so that they do not persistently modify Git-visible state.

The CLI must compare Git-visible state before and after verification. Generation or modification of ignored output alone is allowed. Changes to the tracked working tree, index, or target untracked set must be treated as verification mutation.

After verification completes and immediately before the commit sequence starts, the CLI must recalculate HEAD and the change_hash of every target change and verify that they match the analysis snapshot. If verification mutation or snapshot mismatch is detected, commits and push must not start. Working-tree changes are left intact, the index is restored to its state at the start of the run, changed paths are displayed, and the CLI must prompt `Re-analyze changed state? [y/N]`. If the user selects `y`, analysis restarts from the current state; if declined, the CLI exits with code 4.

### FR-013 Commit Execution

The CLI must stage the final working-tree state of explicit file sets in plan order and create commits while honoring Git hooks and signing configuration. Even if a targeted file was partially staged when the run started, that staged selection is not preserved; the full file change is included in the commit.

Protection of staged content and staged selection for out-of-scope files is an immutable v1 invariant and cannot be disabled by configuration or CLI.

Immediately before staging each commit, the CLI must recalculate the current change_hash of every unprocessed file ID assigned to that commit and confirm that it matches the analysis snapshot. On mismatch, that commit must not start, and all later commits and push must stop as a safety-condition violation. This prevents unanalyzed content from being staged when a prior commit's hook, an IDE, or another process modifies a later target.

Git hooks are generally allowed as part of normal Git operations. A hook may modify the path contents or index contents corresponding to file IDs assigned to the current commit as long as no commiter-specific invariant is violated. The modified content is not required to retain the original change_hash.

The file set in each created commit's diff against its parent must exactly match the file set corresponding to the file IDs assigned to that commit. If a hook or other process introduces an out-of-scope staged change or a file assigned to another commit, or if an assigned file is missing from the commit, this is a commiter-specific security-invariant violation.

If such an invariant violation is detected after a commit is created, the CLI must not automatically roll back the created commit. It must display the violating paths, prohibit later commits and push, and exit with code 7.

### FR-014 Push Execution

After all commits succeed, the CLI must push exactly once. It must prefer the upstream and may use `git push -u` only when no upstream exists, exactly one remote exists, and the branch can be safely determined.

Push follows normal Git push semantics and targets every outgoing commit sent from the current branch to the resolved remote ref. Outgoing commits that existed before the current commiter run are not excluded and may be automatically pushed when auto push is enabled. commiter does not reanalyze those pre-existing outgoing commits as part of the current diff analysis or sensitive classification. This behavior must be stated in the pre-push display.

### FR-015 setup

`commiter setup` must separately confirm installation of Ollama, daemon startup, and download of the default model, and must not automatically install Homebrew itself.

### FR-016 doctor

`commiter doctor` must perform read-only diagnostics for Git, Ollama, the loopback API, the default model, structured output, thinking-disabled operation, configuration, trust, and Git identity.

### FR-017 Language Configuration

Commit summaries are English by default and must be switchable to Japanese through configuration or `--language ja`.

Language handling must have an extension boundary that allows additional languages to be registered without changing the plan schema.

### FR-018 Metrics

The CLI must display timing for Git preprocessing, syntax analysis, model load, prompt evaluation, generation, summarization, verification, Git, and push, as well as model tag or digest, context tier, file/line/byte counts, counts of Tree-sitter parse success/fallback files, summarization count, and exit classification.

### FR-019 Daemon Lifecycle

During normal execution, if loopback Ollama is stopped, the CLI must start a temporary daemon and stop only a daemon that it started itself.

An existing daemon must be reused and must not be stopped when the CLI exits.

Normal Ollama calls must specify `keep_alive: 0`.

### FR-020 Model Lifecycle

Normal execution must not pull or update models.

`setup --update-model` must update a model only after displaying the update and obtaining explicit confirmation.

setup must reuse an existing official Ollama App or CLI. If Ollama is not installed and Homebrew is available, setup must separately prompt for Ollama installation, daemon startup, and model download.

setup must not install Homebrew itself.

### FR-021 Configuration and State Commands

`config init` must generate either the global or repository initial configuration file, `config show` must display effective values and their origins, and `config path` must display the absolute path of each configuration file.

`trust list` must display the canonical repo path, verification definition hash, source type, and argv for stored trust records, and `trust revoke` must remove the specified trust record.

Configuration precedence is CLI, repo, global, then built-in defaults. Safety settings may be changed only by global configuration or explicit CLI. However, verification configuration is repository-scoped only and must not be specifiable through global configuration or CLI. Protection of out-of-scope staged content and staged selection and the permanent exclusion of clearly sensitive files are immutable v1 invariants that cannot be relaxed from any configuration source.

If repository configuration contains a prohibited safety-setting key, the CLI must stop with a configuration error.

If the current verification definition hash does not match the stored trust hash, the CLI must require approval again before running verification.

### FR-022 Default Confirmation Behavior

Commit confirmation and push confirmation are enabled by default, and push itself is enabled by default.

Global configuration and explicit CLI options must be able to skip commit confirmation and push confirmation independently.

The confirmation before reading a sensitive candidate and the push confirmation for a commit containing a file included as a sensitive candidate in the current run must not be skippable through configuration or generic confirmation-bypass CLI options. Clearly sensitive files are always automatically excluded and no override is provided to target them.

### FR-023 Complete Assignment of the Commit Plan

There is no upper limit on the number of commits, and every target file ID must be assigned to exactly one commit.

If any target file ID is missing, duplicated, or out of range, the CLI must stop without modifying Git.

### FR-024 Machine-readable Output

`--json` may be used only with `--dry-run` and read-only subcommands. Combining it with normal execution that performs commits or push is a usage error causing exit code 2.

## 10. LLM Input and Output

The Ollama endpoint is restricted to loopback and uses `think: false`, `stream: false`, JSON Schema, and `keep_alive: 0`. Based on the API features required by commiter v1, operational compatibility of the default model `qwen3.5:4b-q4_K_M`, and the structured-output fix for models with thinking disabled, supported Ollama versions are `0.31.2` or later. Earlier versions, prereleases of `0.31.2`, and invalid version responses are treated as API-incompatible. See [Ollama Chat API](https://docs.ollama.com/api/chat), [Structured Outputs](https://docs.ollama.com/capabilities/structured-outputs), and [Ollama v0.31.2](https://github.com/ollama/ollama/releases/tag/v0.31.2).

Input includes mechanically computed repository state, target file IDs, old/new paths, status, language, change_hash, structural evidence, and required raw diff hunks or hierarchical summaries. Structural evidence is limited to syntactically observed facts and must not include semantic relation labels or grouping recommendations such as source/test, docs/source, same feature, or same logical change.

The LLM determines each file's change intent from the actual change content, compares those intents across files, and decides grouping. For ordinary files, path, common directory, similar filename, import relationships, or proximity of syntax nodes alone must not be treated as sufficient grounds for grouping. Only for opaque files may metadata be used as supporting evidence to compensate for unavailable content.

The output schema must have the following form:

```json
{
  "schema_version": 1,
  "commits": [
    {
      "type": "fix",
      "scope": "auth",
      "breaking": false,
      "summary": "improve token validation",
      "file_ids": ["f001", "f002"]
    }
  ]
}
```

`type` must be one of `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, or `revert`.

`scope` is a required single-line string and must not be empty.

`breaking` is boolean; when true, `!` is appended after the scope in the subject.

`summary` must be a single line. Commit bodies, multiline summaries, unassigned file IDs, and duplicate file IDs are not allowed.

The normal commit-message form is `type(scope): summary`; for a breaking change it is `type(scope)!: summary`.

## 11. Verification Trust

Verification configuration is repository-scoped; there is no global verification configuration.

If repository configuration explicitly defines `verification.commands`, that command list has highest priority. `verification.commands` must contain at least one command; an empty array is a configuration error.

Only when `verification.commands` is absent is `verification.autodetect` evaluated. When `verification.autodetect=true`, the CLI safely autodetects existing `lint`, `typecheck`, `test`, and `build` scripts from the repository-root `package.json` in that order. When false, verification is disabled. If the package manager cannot be uniquely resolved from repository-root information, no autodetected command is executed.

Standard Go, Rust, and Python commands are not inferred automatically; they are explicitly specified as argv arrays in repository configuration.

The canonical repo path is the real path obtained by resolving symlinks in the absolute path of the repository root. The cwd of each verification command is also resolved through symlinks before execution and is allowed only when its real path is contained within the canonical repo path. A cwd outside the repository or a cwd that cannot be resolved is rejected as a configuration error.

Before running verification, the CLI must determine the current verification definition and compute the SHA-256 of its normalized representation as the trust hash. The verification definition includes:

- schema version `1`
- source type (`repo_config` or `package_json_autodetect`)
- the command list preserving execution order
- each command's `name`
- `cwd` normalized relative to the repository root
- complete `argv`, preserving argument boundaries and order
- only for `package_json_autodetect`, the manifest path and exact script name and script body
- only when a package manager used by `package_json_autodetect` implicitly runs pre/post scripts that cannot be disabled, the exact name and body of each such script, preserving execution order

The normalized representation is canonical JSON encoded as UTF-8, with fixed object-key order, no unnecessary whitespace, and preserved array order for commands and argv. The trust hash is the SHA-256 of those bytes. The canonical repo path is not included in the hash and is stored separately as the scope key of the trust record.

For repository configuration, the entire `.commiter.toml` must not be hashed; only command definitions adopted into the verification definition are hashed. For `package.json` autodetection, the whole manifest must not be hashed; only the adopted script's manifest path, script name, script body, and the names and bodies of pre/post scripts that are actually run implicitly are hashed.

Source files, Git HEAD, lockfiles, dependency content, manifest scripts unrelated to verification and not implicitly executed, model, commit, analysis, and other configuration must not be included in the trust hash.
However, if package-manager resolution changes such that the final argv or the implicitly executed pre/post scripts change, the trust hash must change because the verification definition changed.

On the first run, when no trust record exists, or when the current verification definition hash differs from the stored hash, the CLI must display the source type, each planned command's name, argv, and cwd, the script name and script body for autodetection, the names and bodies of pre/post scripts actually executed implicitly, and the current trust hash, then request approval before running verification.

When the hash matches, the same verification definition may execute without reapproval. If there are no verification commands, the CLI displays `Verification: none` and continues without creating a trust record or requesting additional confirmation.

## 12. Security and Safety Requirements

### SR-001 Local Transmission Boundary

commiter itself must not transmit diffs, prompts, LLM responses, or any other repository content outside loopback for LLM inference, analysis, telemetry, or other auxiliary processing.

This restriction does not apply to communication performed by user-authorized Git push or by child processes such as user-approved verification commands, Git hooks, or signing operations.

### SR-002 Pre-read Sensitive-file Classification

Before reading contents, the CLI must classify sensitivity using paths only. Clearly sensitive files must always be automatically excluded without prompting, and the exclusion reason must be displayed. v1 provides no configuration or CLI override, and these files must not be included in analysis, file-ID assignment, or commit targets.

Before reading any sensitive candidate, the CLI must display its path and detection reason and request batch approval.

Path classification operates component-by-component on normalized repository-root-relative paths while preserving Unicode and ignoring ASCII case.
Each directory component and the basename without its extension are split into tokens on `.`, `-`, and `_`. A path is a sensitive candidate when one of those tokens exactly equals `auth`, `credential`, `credentials`, `secret`, `secrets`, `token`, `tokens`, `password`, or `passwd`.
Substring-only matches must not cause files such as `authentication.go` or `tokenizer.go` to become candidates.

`.npmrc`, `.pypirc`, `.netrc`, `.docker/config.json`, and paths whose basename is `kubeconfig` are sensitive candidates.
However, when a path matches a built-in clearly sensitive pattern or `safety.additional_sensitive_patterns`, automatic exclusion takes precedence over candidate confirmation.

### SR-003 Rejecting Sensitive Candidates

If the user rejects a sensitive candidate, the CLI must exclude it without reading its contents, show it in the exclusion list on the plan screen, and continue with the remaining targets. Automatically excluded or rejected files must be removed from the target-change and file-ID sets.

### SR-004 Protecting Included Sensitive Candidates

The contents of an approved sensitive candidate may be provided only to in-process structural analysis and the loopback local LLM. Raw values, prompts, and raw diffs must not be output or persisted in terminal output, JSON output, metrics, debug logs, or persistent files.

Even under `--dry-run`, the raw diff of an approved sensitive candidate must not be shown. Only path, status, and other non-sensitive metadata may be displayed.

### SR-005 Reconfirmation Before Pushing Sensitive Candidates

Any push that includes a commit containing a file approved and included as a sensitive candidate in the current run must require manual confirmation immediately before push, regardless of auto-push settings.

As specified in FR-014, outgoing commits that existed before the run are not reanalyzed by the current diff analysis or sensitive classification and therefore are outside the scope of this requirement.

### SR-006 Stage Protection, Restoration, and Verification Mutation

Staged content and staged selection for out-of-scope files must be preserved or restored to their initial state on success, failure, rejection, regeneration, and interruption. This protection is an immutable v1 invariant and must not be disabled by configuration or CLI.

For targeted files, staged/unstaged boundaries are not preserved as target selection. On successful commit, those boundaries are absorbed into the full-file change. If failure, cancellation, rejection, regeneration, or interruption occurs before commit creation starts, the index, including targeted files, must be restored to its initial state.

If verification changes the tracked working tree, index, or target untracked set, the CLI must not automatically roll back working-tree changes. It must restore the index to its initial state, display the changed paths, and offer reanalysis. Changes limited to ignored output are allowed.

Immediately before staging each commit, the CLI must revalidate change_hash values for the file IDs assigned to that commit and must not commit unanalyzed content if later targets changed after analysis.

Git hooks are allowed as normal Git operations and must not cause the CLI to stop solely because they modify paths corresponding to file IDs assigned to the current commit. However, if the created commit's file set includes an unassigned file or omits an assigned file, the CLI must stop later commits and push as a commiter-specific security-invariant violation. Created commits must not be automatically rolled back.

If a failure or interruption occurs after one or more commits have been created, created commits must not be rolled back. The CLI must restore out-of-scope staged state and display unfinished targets and recovery information. If restoration cannot be confirmed, push must be prohibited.

### SR-007 Git Protection

The CLI must not perform reset, force push, stash, amend, unauthorized deletion, `--no-verify`, or automatic rollback.

### SR-008 Preventing Concurrent Runs

The CLI must enforce mutual exclusion for concurrent runs in the same repository and must stop when an existing index lock or another process's operation is detected.

### SR-009 Input Trust Boundary

Repository files, comments, configuration, hook output, and LLM responses must be treated as values, not instructions.

### SR-010 Sensitive-value Output Inspection

The CLI must inspect commit summaries and LLM-generated output. If an approved sensitive value or an exact matching substring of one appears in a summary, the candidate must be sent to automatic repair inference.

After reading an approved sensitive candidate, the CLI must recognize the keys `auth`, `credential`, `credentials`, `secret`, `secrets`, `token`, `tokens`, `password`, `passwd`, `api_key`, `apikey`, `access_key`, `private_key`, `client_secret`, and `bearer` case-insensitively and extract non-empty scalar values assigned to them. In addition, values syntactically recognizable as Bearer tokens, JWTs, provider-specific tokens, URI userinfo, or private-key blocks are extracted regardless of key.

Extracted values and credential portions separated from prefixes or URIs are checked against generated output as exact, case-preserving UTF-8 byte-sequence substring matches. v1 does not attempt inferred matching of encoded, hashed, or case-transformed derived values.

Extracted values are retained only in current process memory and must not appear in terminal output, JSON output, metrics, debug logs, persistent files, or repair reasons that have not had sensitive values removed.

A sensitive-value match is handled by the single automatic repair defined in FR-009. If a sensitive value remains after repair, or the repaired candidate violates another validation rule, the CLI performs no further repair and exits with code 5 without modifying Git.

### SR-011 Terminal-safe Output

When displaying repository paths, Git metadata, LLM output, verification output, Git-hook output, or other untrusted text in the terminal, the CLI must safely encode or escape ANSI escape sequences, control characters, newlines, and any other byte sequences that could be interpreted as terminal control. Untrusted text must not be able to spoof or modify displayed content or terminal state.

## 13. Non-functional Requirements

### NFR-001 Reproducibility

For the same Git state, configuration hash, model digest, input, and Tree-sitter/grammar versions fixed to the same build, the mechanically selected target set, structural evidence, and schema-validation result must be reproducible.

### NFR-002 Memory Management

With the default `keep_alive: 0`, the model is unloaded after inference; the system must not assume model residency.

### NFR-003 Large Diffs

Even when input exceeds 32K, hierarchical summarization must allow plan generation to continue while preserving the complete target-file set.

### NFR-004 Operational Visibility

The user must be able to identify commit, push, verification, and stage state, planned actions, failure locations, and recovery information from terminal output.

### NFR-005 Minimizing External Dependencies

Language, binary, and vendor detection use Apache-2.0-licensed `github.com/go-enry/go-enry/v2`, prioritizing accuracy and maintainability over a hand-maintained extension table.

The binary-size impact of go-enry's generated data is accepted. Oniguruma is not used. [go-enry](https://github.com/go-enry/go-enry)

Syntax-aware structural analysis uses the official `github.com/tree-sitter/go-tree-sitter` package and official grammar Go bindings. Tree-sitter core and grammar C code are embedded into the single CLI binary at build time via CGo, so no external parser executable or shared grammar library is required at runtime. CGo is limited to this syntax-analysis boundary. [go-tree-sitter](https://github.com/tree-sitter/go-tree-sitter)

MIT-licensed `github.com/bmatcuk/doublestar/v4` is used for globs containing `**`; standard `path.Match`, which cannot handle `**`, is not used as a substitute.

doublestar traversal is restricted to the repository root. [doublestar](https://github.com/bmatcuk/doublestar)

MIT-licensed `github.com/pelletier/go-toml/v2` is used for TOML, prioritizing maintainability over a custom parser.

TOML input size is limited, and unknown keys are configuration errors. [go-toml](https://github.com/pelletier/go-toml)

JSON, HTTP, and subprocess handling use the Go standard library.

Mapping diff hunks to syntax-node ranges, normalizing structural evidence, and compressing LLM input are implemented in Go. Tree-sitter is used only for syntax-aware structural analysis and is not extended into semantic static analysis such as type resolution, cross-file symbol resolution, call graphs, control-flow graphs, or data-flow analysis.

### NFR-006 Local Records

Persistent metrics are disabled by default and written to `metrics.jsonl` under the state directory only when explicitly enabled.

Metrics must not record paths, messages, diffs, prompts, user feedback, or sensitive values.

### NFR-007 Performance Measurement

v1 defines no numeric performance gate. Per-phase measurements are collected on an M3 machine with 16 GB of memory and used to define thresholds for a later version.

## 14. Failure Behavior

If the LLM is unavailable, the model is not installed, API compatibility diagnostics fail, transport errors or timeouts remain after the allowed retry, the final candidate fails schema/assignment/safety validation, verification fails, or stage restoration fails before commit creation begins, commits and push must not start.

Unsupported Tree-sitter grammar, syntax error, or structural-evidence extraction failure for an individual file is not fatal; only that file falls back to raw diff plus Git metadata.

If revalidation after verification detects a change in the tracked working tree, index, target untracked set, HEAD, or target change_hash values, verification-generated working-tree changes are left intact and the index is restored to its initial state. Changed paths are displayed in a terminal-safe manner and the CLI prompts `Re-analyze changed state? [y/N]`. If the user selects `y`, target selection and analysis restart from the current state; if declined, the CLI exits with code 4. Verification commands need to be configured not to persistently modify Git-visible state; if the same verification mutation recurs after reanalysis, the CLI displays the responsible command so the user can correct the verification definition.

Generation or modification of ignored files alone is not considered verification mutation and processing may continue.

If a mismatch is detected by change_hash revalidation immediately before staging a commit, that commit must not start. Already-created commits are retained; later commits and push are stopped, and the changed paths and unfinished plan are reported.

If a created commit's file set differs from the assigned file IDs because of a Git hook or another normal Git operation, the created commit is not reset. The CLI reports the violating paths, unfinished plan, and that push was not performed, then exits with code 7.

If another failure occurs partway through commit creation, already-created commits are likewise not reset; their hashes, the unfinished plan, and that push was not performed are reported.

If the push destination cannot be resolved and the user approved commit creation, local commits may still be created before the process stops with a push failure.

Failure to resolve the push destination returns exit code 8. A network push failure also leaves created local commits intact and performs no rollback.

On Ctrl-C, the CLI stops the in-progress operation and reports stage-restoration results and hashes of any commits already created.

## 15. Exit Codes

| Code | Classification |
| ---: | --- |
| 0 | Success or no target changes |
| 1 | Unexpected internal failure |
| 2 | Usage or configuration error |
| 3 | User cancellation |
| 4 | Safety-condition violation or restoration failure |
| 5 | LLM, Ollama, or JSON-schema error |
| 6 | Verification failure or verification-environment error |
| 7 | Commit or post-commit recovery error |
| 8 | Push error |
| 130 | Interrupted by SIGINT |

## 16. Acceptance Criteria

### AC-001 Target Scope

In a temporary Git repository containing a mixture of staged, unstaged, and untracked changes, verify that invocation without arguments collects only safety-approved targets and that pathspec invocation limits the scope.

### AC-002 Stage Handling

Prepare partial staging and out-of-path staged changes. Verify that for targeted files, staged and unstaged changes are included together as full-file changes in the same commit. Verify that staged content and staged selection for out-of-scope files remain unchanged even on success, and that this protection cannot be disabled by configuration or CLI.

Verify that each created commit's diff against its parent contains only changes corresponding to file IDs assigned to that commit and contains neither out-of-scope staged files nor file IDs assigned to another commit.

For verification failure, user cancellation, or Ctrl-C before commit creation starts, verify that the index, including targeted files, is restored to its initial state. For failure or Ctrl-C after commit creation, verify that created commits are not rolled back and out-of-scope staged state is restored.

### AC-003 Path Types

Prepare renames, deletions, binary files, symlinks, submodule pointers, Unicode paths, and paths containing spaces and newlines. Verify that processing does not recurse outside the target or incorrectly read file contents.

Verify that a rename is represented as one file ID with old and new paths and is not split into separate deletion and addition file IDs. For regular files, untracked files, deletions, renames, binary files, symlinks, and submodule pointers, verify that change_hash is computed as specified and changes when any included content, path, mode, or object-identity element changes.

### AC-004 Sensitive Confirmation

Verify that clearly sensitive files are always automatically excluded without prompting, receive no file ID, and have no CLI or configuration override.

Using fixtures for Unicode paths, ASCII case differences, directory components, basename tokens split on `.`, `-`, and `_`, `.npmrc`, `.pypirc`, `.netrc`, `.docker/config.json`, and basename `kubeconfig`, verify that sensitive candidates are classified according to the exact-match rules in SR-002. Verify that substring-only matches such as `authentication.go` and `tokenizer.go` do not become candidates, and that a path matching a built-in clearly sensitive pattern or `safety.additional_sensitive_patterns` is automatically excluded before candidate confirmation.

Verify that sensitive candidates are confirmed before reading; when approved, their contents are supplied only to the local LLM, and when rejected they are excluded without being read or assigned a file ID. For approved candidates, verify that raw values and raw diffs do not appear in normal output, `--dry-run`, `--json`, metrics, debug logs, or persistent files.

### AC-005 Large Diffs

Prepare fixtures that fit within each 8K, 16K, and 32K tier and a fixture exceeding the limit. Verify that the smallest allowed context tier is selected using the final prompt UTF-8 byte count, the fixed 256-token chat-template allowance, and output-reserve tokens calculated as `max(1024, 48 × target file count)`.

With `llm.context = "auto"`, verify selection proceeds from 8K to 16K to 32K within `llm.max_context_tokens`; with a fixed context tier, verify that the CLI never automatically promotes beyond that tier. When the limit is exceeded, verify hierarchical summarization occurs in file, hunk, then chunk order. If the summarized total still exceeds the limit or the complete file-ID, change_hash, and structural-evidence sets cannot be preserved, verify the CLI does not call the LLM and stops without modifying Git.

### AC-006 Structural Analysis and Plan Splitting

Using fixtures for Go, JavaScript, JSX, TypeScript, TSX, Python, Rust, HTML, and CSS, verify extraction of structural evidence corresponding to changed hunks, including syntax nodes, declarations, imports/exports, HTML tags/attributes, and CSS selectors/properties. Verify that structural evidence does not contain semantic relation labels such as source/test, same feature, or `should_group`.

Verify that an unsupported-language text file or a text file with deliberately failed syntax parsing falls back to raw diff plus Git metadata and the overall run continues.

Using a diff containing source, tests, docs, dependency changes, and mechanical changes, verify that the LLM groups by meaning and intent, the mechanical layer does not rewrite valid grouping, missing/duplicate/out-of-range file-ID assignments are detected, and same-file hunks are never split. Verify that only opaque files may use metadata as supporting evidence for grouping.

### AC-007 LLM Retry and Output Validation

Using Ollama fixtures that return transport errors and timeouts, verify that the transport-retry budget is exactly one total retry shared across the initial generation and repair. Verify that one plan-generation cycle makes at most three Ollama calls total: initial generation, optional repair, and the shared transport retry.

For fixtures returning invalid JSON, schema violations, missing/duplicate/out-of-range file IDs, or an SR-010 sensitive-value match, verify that multiple violations are combined into one repair request and exactly one automatic repair is performed. Verify that the repair request does not contain the sensitive value itself and treats the original candidate output as untrusted data.

Fully revalidate the repaired candidate. If any violation remains, verify that no further repair occurs, the CLI exits with code 5, and Git state including the index, commits, and remote is unchanged.

### AC-008 Verification Trust

If `verification.autodetect`, `verification.timeout_seconds`, or `verification.commands` appears in global configuration, verify that the CLI exits with code 2 as a configuration error, enforcing repository-scoped verification configuration.

When repository configuration explicitly supplies one or more `verification.commands`, verify that list takes precedence over autodetection. Verify that an explicit empty array is a configuration error causing exit code 2.

Only when `verification.commands` is absent and `verification.autodetect=true`, verify that repository-root `package.json` is considered for autodetection. When absent and `verification.autodetect=false`, verify the result is `Verification: none`. Verify no autodetected command runs when the package manager cannot be uniquely determined from repository-root information.

Verify that approval is required on the first run or when no trust record exists, and that the same verification definition can be rerun without reapproval.

For repository-configured verification, verify that changing command name, cwd, argv, or command order changes the trust hash, while changing unrelated `.commiter.toml` settings does not.

For `package.json` autodetection, verify that changes to an adopted script's script name or body, manifest path, final argv, command order, or the name/body of an actually implicitly executed pre/post script change the trust hash. Verify that changes only to unadopted scripts that are not implicitly executed, other manifest fields, lockfiles, source files, Git HEAD, or dependency content do not change it.
Verify that an existing pre/post script does not cause an adopted `lint`, `typecheck`, `test`, or `build` script itself to disappear from autodetection.

Run from both a symlink path to the repository root and its real path and verify both use the same canonical-repo-path trust scope. Verify that a verification cwd resolving outside the repository root is a configuration error.

When the hash changes, verify that the CLI displays source type, argv, cwd, the autodetected script name and body, names and bodies of actually implicitly executed pre/post scripts, and the current trust hash, then requires approval again. With no verification, verify that it displays `Verification: none` and creates no trust record.

### AC-009 Commits and Hooks

Prepare multi-commit plans, successful hooks, failing hooks, and signing configuration. Verify plan order, commit-message format, and prohibition of push when the plan remains incomplete.

Create a fixture where a hook from an earlier commit or an external process changes a file assigned to a later commit. Verify that change_hash revalidation immediately before staging the next commit detects the mismatch and prevents that commit from starting.

Verify that a pre-commit hook that only formats or otherwise modifies files assigned to the current commit is allowed as a normal Git operation and does not require matching the original change_hash.

If a hook introduces an out-of-scope staged file or a file assigned to another commit into the current commit, or causes an assigned file to be omitted, verify that created-commit file-set validation detects a security-invariant violation, does not roll back the created commit, stops later commits and push, and exits with code 7.

### AC-010 Push Resolution

For states with an upstream, one remote without an upstream, multiple remotes, and an unknown branch, verify upstream preference, conditional `git push -u`, and stopping when resolution is impossible.

Prepare local commits that are already ahead of the remote-tracking ref before running commiter, then have commiter create new commits. Verify that under normal Git push semantics both the pre-existing outgoing commits and commits from the current run are included in the push, that auto push does not exclude the pre-existing outgoing commits, that those commits are not reanalyzed by the current diff analysis or sensitive classification, and that this is stated before push.

### AC-011 setup and doctor

For states where Ollama is not installed, the daemon is stopped, the model is missing, and the model is present, verify setup's separate confirmations and doctor's non-destructive diagnostics.

### AC-012 Configuration Precedence

Set different values in global, repository, and CLI configuration and verify that values are applied in CLI, repo, global, then default order, and that safety settings cannot be changed from repository configuration.

For every configuration-schema key, verify its type, default, allowed sources, array replacement behavior, rejection of cwd and globs outside the repository root, and that unknown keys, unsupported schema versions, type mismatches, and keys from disallowed sources cause exit code 2. Verify that global `verification.*` keys are rejected, that `analysis.preserve_outside_staged` does not exist in the schema, and that protection of out-of-scope staged state cannot be disabled through configuration.

### AC-013 Language

Verify that English summaries are generated with the default configuration and Japanese summaries are generated with `ja`, while the same plan schema is preserved.

### AC-014 Metrics and Memory

On an M3 machine with 16 GB of memory, verify that per-phase metrics are displayed and that Ollama state confirms the model is released after inference because of `keep_alive: 0`.

### AC-015 Daemon and Model Lifecycle

For states where the Ollama daemon is stopped, already running, the model is missing, and an update is available, verify reuse of an existing daemon, stopping only a daemon started by commiter, no model pull during normal execution, and setup's separate confirmations.

### AC-016 Configuration and Trust Commands

Verify output and state changes for `config init --global`, `config init --repo`, `config show --effective`, `config path --global`, `config path --repo`, `trust list`, and `trust revoke <repo>`. Verify that `trust list` shows canonical repo path, verification definition hash, source type, and argv, and that verification requires approval again on the next run after `trust revoke <repo>`.

### AC-017 JSON Constraints and Assignment

Verify detection of invalid type, empty scope, multiline summary, body, missing file IDs, duplicate file IDs, out-of-range file IDs, and assignment of one file to multiple commits, and verify the CLI stops without modifying Git.

Verify that `--json` succeeds with `--dry-run` and read-only subcommands and causes exit code 2 as a usage error when used with execution that performs commits or push.

### AC-018 Sensitive-value Inspection of Summaries

For an approved sensitive candidate, prepare fixtures containing non-empty scalar values assigned to the sensitive keys from SR-010, Bearer tokens, JWTs, provider-specific tokens, URI userinfo, and private-key blocks. Verify that extracted values and credential portions are checked against generated output as exact, case-preserving UTF-8 byte-sequence substring matches. Verify that encoded, hashed, or case-transformed derived values are outside the v1 inspection scope.

When generated output contains a sensitive-value match, verify that the single shared automatic repair from FR-009 runs. If a match or another validation violation remains after repair, verify the CLI exits with code 5 without modifying Git. Verify that extracted raw values never remain in terminal output, `--json`, metrics, debug logs, persistent files, or repair reasons that have not had sensitive values removed.

### AC-019 Preserving Local Commits on Push Failure

When the push destination cannot be resolved or a network push fails, verify that approved local commits remain, no rollback occurs, and exit code 8 is returned.

### AC-020 Verification Mutation and Reanalysis

Prepare verification fixtures that modify the tracked working tree, index, and target untracked files respectively. Verify that commit creation does not start, working-tree changes are preserved, the index is restored to its initial state, changed paths and the reanalysis confirmation are displayed, selecting `y` reruns target selection, change_hash calculation, and LLM analysis from the current Git state, and declining exits with code 4.

For a verification fixture that only generates or modifies ignored files, verify that this is not treated as mutation and processing continues. Also verify that if an external process changes a target file after verification and before the commit sequence starts, revalidation of all target change_hash values detects the mismatch.

### AC-021 Terminal-safe Output

Using fixtures containing ANSI escape sequences, control characters, and newlines in paths, LLM output, verification output, and Git-hook output, verify that they are safely encoded or escaped rather than interpreted as terminal control and cannot spoof displayed content or terminal state.

## 17. Requirements Traceability Matrix

| Acceptance criterion | Functional requirements | Safety requirements | Non-functional requirements |
| --- | --- | --- | --- |
| AC-001 | FR-001–FR-004 | SR-008 | NFR-001 |
| AC-002 | FR-002, FR-013 | SR-006, SR-007 | NFR-004 |
| AC-003 | FR-002, FR-003 | SR-008 | NFR-001 |
| AC-004 | FR-004 | SR-002–SR-005, SR-010 | NFR-006 |
| AC-005 | FR-006, FR-007 | SR-001 | NFR-003, NFR-007 |
| AC-006 | FR-005, FR-010 | SR-009 | NFR-001, NFR-005 |
| AC-007 | FR-008, FR-009 | SR-001 | NFR-001 |
| AC-008 | FR-012 | SR-009 | NFR-004 |
| AC-009 | FR-011, FR-013 | SR-007 | NFR-004 |
| AC-010 | FR-014 | SR-007 | NFR-004 |
| AC-011 | FR-015, FR-016 | SR-001 | NFR-005 |
| AC-012 | FR-021, FR-022 | SR-009 | NFR-001 |
| AC-013 | FR-017 | SR-001 | NFR-001 |
| AC-014 | FR-018 | SR-001 | NFR-002, NFR-007 |
| AC-015 | FR-019, FR-020 | SR-001 | NFR-002 |
| AC-016 | FR-021 | SR-009 | NFR-001, NFR-004 |
| AC-017 | FR-009, FR-010, FR-023, FR-024 | SR-009 | NFR-001, NFR-004 |
| AC-018 | FR-009 | SR-004, SR-010 | NFR-006 |
| AC-019 | FR-014 | SR-007 | NFR-004 |
| AC-020 | FR-012, FR-013 | SR-006, SR-007 | NFR-001, NFR-004 |
| AC-021 | FR-016 | SR-009, SR-011 | NFR-004 |

## 18. Future Candidates

After v1 acceptance, consider a llama.cpp backend, additional languages, a Homebrew Tap, and empirically based performance gates.

Future candidates do not change v1 runtime dependencies, CLI, JSON plan schema, or the default behavior of safety confirmations.
