# commiter-cli

English | [日本語](README_ja.md)

[![CI](https://github.com/neural-int/commiter-cli/actions/workflows/go.yml/badge.svg)](https://github.com/neural-int/commiter-cli/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`commiter` is a local-first Git commit planning CLI. It mechanically analyzes repository changes, asks a local Ollama model to propose a multi-commit Conventional Commits plan, verifies that plan against the current Git state, and only then creates commits and optionally pushes them.

> **Pre-release status:** no packaged GitHub Release is published yet. The currently available installation path is to build from source.

## Overview

Most AI commit tools ask a model to interpret a raw diff and produce a message. `commiter` deliberately separates deterministic work from model judgment.

The CLI handles Git state, target selection, sensitive-file classification, syntax-aware preprocessing, plan validation, verification, commit execution, and push safety mechanically. The local LLM is used only where judgment is needed: understanding the meaning and intent of changes and grouping files into commits.

This design is intended to keep repository content local, reduce the amount of work delegated to a small local model, and make Git mutation explicit and fail-closed.

## Features

- **Multi-commit planning** — groups file-level changes by purpose and generates Conventional Commits messages.
- **Local LLM inference** — LLM requests are restricted to a loopback Ollama endpoint; repository content is not sent to a cloud LLM by `commiter`.
- **Syntax-aware preprocessing** — uses Tree-sitter where supported instead of asking the model to infer syntax from raw text alone.
- **Adaptive context handling** — uses 8K, 16K, and 32K context levels and hierarchical summarization when required.
- **Sensitive-file protection** — clearly sensitive files are always excluded; ambiguous candidates require approval before their contents are read.
- **Plan validation and approval** — validates model output and, by default, requires approval before commits are created.
- **Repository-scoped verification** — supports explicit verification commands and package-script autodetection with repository-scoped trust.
- **Git-state protection** — revalidates analyzed changes before mutation and stops instead of committing unanalyzed state.
- **Dry run and machine-readable output** — inspect the plan without modifying Git; JSON output is restricted to dry-run and read-only operations.
- **English and Japanese commit messages** — select with configuration or `--language en|ja`.

## How it works

A normal run follows this flow:

1. Validate the repository, branch, HEAD, index, conflicts, and in-progress Git operations.
2. Collect staged, unstaged, and untracked state and determine the target files at file granularity.
3. Classify sensitive paths before reading file contents; automatically exclude clearly sensitive files and ask before reading sensitive candidates.
4. Build Git metadata and syntax-aware structural evidence. Unsupported text formats fall back to raw diff plus Git metadata.
5. Compress the input as needed and ask the configured local Ollama model to generate a constrained JSON commit plan.
6. Validate file assignment and safety rules, then show the plan for approval.
7. Run the approved repository-scoped verification definition and revalidate Git state before creating commits.
8. Create commits in plan order and, when enabled, perform one push after the commit sequence completes.

If the state observed during analysis no longer matches the state immediately before mutation, `commiter` stops or offers re-analysis instead of continuing with stale assumptions.

## Requirements

The v1 target environment is:

- macOS 14 or later
- Apple Silicon
- Git
- Ollama

For the current source-build installation path, you also need:

- Go 1.23 or later
- Xcode Command Line Tools or another working C compiler for the Tree-sitter CGo build

The reference development environment is an M3 Mac with 16 GB of memory. This is a reference environment, not a declared minimum-memory requirement.

The default model is `qwen3.5:4b-q4_K_M`. The default Ollama endpoint is `http://127.0.0.1:11434`.

## Installation

Packaged binaries and a Homebrew distribution are not published yet. Until an actual release is available, build from source:

```sh
git clone https://github.com/neural-int/commiter-cli.git
cd commiter-cli
go install ./cmd/commiter
```

`go install` places the binary in `GOBIN`, or in `$(go env GOPATH)/bin` when `GOBIN` is unset. Ensure that directory is on your `PATH`.

No Ollama model is bundled into the binary.

## Quick Start

Run the following from the Git repository you want to process:

```sh
commiter setup
commiter doctor
commiter --dry-run
commiter
```

`commiter setup` checks the configured Ollama environment. If Ollama is missing and Homebrew is available, it can install Ollama after confirmation. It can also start Ollama temporarily and pull the configured model after confirmation.

`commiter doctor` is read-only and checks the repository, configuration, Git identity, Ollama connectivity, configured model, structured-output support, and related prerequisites.

`commiter --dry-run` performs analysis and plan generation without modifying the index, creating commits, or pushing.

A plain `commiter` run can create commits and push. Review the displayed plan and prompts before approving mutation.

## Usage

### Main invocation

```text
commiter [flags] [--] [pathspec...]
```

With no pathspec, `commiter` considers tracked changes from `HEAD` through the working tree plus untracked files that pass its safety checks. Git pathspecs can limit the target scope.

Examples:

```sh
# Preview a plan without Git mutation
commiter --dry-run

# Generate Japanese commit messages for this run
commiter --dry-run --language ja

# Limit the run to selected paths
commiter --dry-run -- src/ internal/

# Create commits but do not push
commiter --no-push
```

### Commands

| Command | Purpose |
| --- | --- |
| `commiter setup [--update-model]` | Prepare Ollama and the configured local model. |
| `commiter doctor` | Run read-only environment and capability checks. |
| `commiter config init --global\|--repo` | Create a global or repository configuration template. |
| `commiter config show [--effective]` | Show resolved configuration and its sources. |
| `commiter config path --global\|--repo` | Show a configuration path. |
| `commiter trust list` | List repository-scoped verification trust records. |
| `commiter trust revoke <repo>` | Revoke a verification trust record. |
| `commiter version` | Show the CLI version. |

### Main flags

| Flag | Effect |
| --- | --- |
| `--dry-run` | Analyze and plan without modifying Git. |
| `--no-push` | Disable push for the current run. |
| `--no-confirm-commit` | Skip the ordinary commit-plan confirmation. Safety-required confirmations still apply. |
| `--no-confirm-push` | Skip the ordinary push confirmation. Safety-required confirmations still apply. |
| `--language en\|ja` | Override commit-message language for the current run. |
| `--model NAME` | Override the Ollama model for the current run. |
| `--record-metrics` | Persist local run metrics for the current run. |
| `--json` | Emit JSON only for `--dry-run` or supported read-only commands. |

Use `commiter --help` for the compact built-in command summary.

## Configuration

Configuration precedence is:

```text
CLI > repository configuration > global configuration > built-in defaults
```

Create configuration files with:

```sh
commiter config init --global
commiter config init --repo
```

The global configuration is stored at `$XDG_CONFIG_HOME/commiter/config.toml`, or `~/.config/commiter/config.toml` when `XDG_CONFIG_HOME` is unset.

Repository configuration is stored as `.commiter.toml` at the repository root.

Inspect the effective configuration with:

```sh
commiter config show --effective
```

Important defaults include:

| Setting | Default |
| --- | --- |
| `commit.language` | `"en"` |
| `commit.confirm` | `true` |
| `push.enabled` | `true` |
| `push.confirm` | `true` |
| `llm.model` | `"qwen3.5:4b-q4_K_M"` |
| `llm.endpoint` | `"http://127.0.0.1:11434"` |
| `llm.context` | `"auto"` |
| `llm.max_context_tokens` | `32768` |
| `verification.autodetect` | `true` |
| `verification.timeout_seconds` | `600` |
| `metrics.persist` | `false` |

The Ollama endpoint must be a loopback HTTP URL. Verification configuration is repository-scoped and cannot be configured globally.

For the complete configuration schema and source restrictions, see the [Software Requirements Specification](SOFTWARE_REQUIREMENTS_SPECIFICATION_en.md).

## Privacy & Safety

`commiter` treats local-only analysis and conservative Git mutation as core invariants rather than optional modes.

### Local LLM boundary

Repository diffs, prompts, LLM responses, and other repository content used by `commiter` are not sent outside loopback for LLM inference, analytics, or telemetry. The configured LLM endpoint is required to be a loopback HTTP URL.

This boundary does not mean every child process is offline: user-approved Git pushes, verification commands, Git hooks, signing operations, and Ollama model downloads may perform their own network access. They are separate from `commiter` sending repository content to a cloud LLM.

### Sensitive files

- Clearly sensitive paths such as common environment files, private keys, and credential/secret locations are automatically excluded.
- There is no generic CLI or configuration override for including clearly sensitive files.
- Ambiguous sensitive candidates are identified from their paths and require approval before their contents are read.
- If a sensitive candidate is approved, its raw values and raw diff are not printed, including during `--dry-run`.
- A push containing a sensitive candidate approved in the current run requires manual confirmation even when normal push confirmation is disabled.

### Approval, verification, and Git-state checks

- Commit-plan confirmation is enabled by default.
- Verification definitions are repository-scoped; approved definitions are trusted by repository and definition hash.
- After verification, `commiter` revalidates the analyzed Git state before commit creation.
- Git hooks are respected; `commiter` does not bypass them with `--no-verify`.
- `commiter` does not use reset, stash, amend, force push, or automatic rollback as recovery shortcuts.
- Invalid or unsafe model output cannot directly mutate Git.

Persistent metrics are disabled by default. When enabled, they are local records and are not intended to contain repository paths, messages, diffs, prompts, user feedback, or sensitive values.

For vulnerability reporting, see [SECURITY.md](SECURITY.md). For the normative safety requirements, see the [Software Requirements Specification](SOFTWARE_REQUIREMENTS_SPECIFICATION_en.md).

## Supported Languages

### Commit-message language

`commiter` supports:

- English (`en`) — default
- Japanese (`ja`)

Set the persistent value with `commit.language`, or override one run with `--language en|ja`.

### Syntax-aware analysis

Tree-sitter-based structural analysis is available for:

- Go
- JavaScript / JSX
- TypeScript / TSX
- Python
- Rust
- HTML
- CSS

Unsupported text languages, or files for which syntax parsing fails, fall back to raw diff plus Git metadata. Binary, large, or otherwise opaque files are represented with metadata rather than file contents.

## Limitations

The current v1 scope intentionally does not provide:

- Windows, Linux, or Intel Mac support
- cloud LLM backends or a llama.cpp backend
- hunk-level commit splitting; commit assignment is file-level
- semantic static analysis such as type resolution, cross-file symbol resolution, control-flow graphs, or data-flow analysis
- recursive analysis of submodules; only the parent repository's submodule pointer change is handled
- packaged GitHub Release or Homebrew installation at this time

The project currently targets Ollama on loopback and an Apple Silicon macOS environment.

## Documentation

- [Software Requirements Specification — English](SOFTWARE_REQUIREMENTS_SPECIFICATION_en.md)
- [ソフトウェア要求仕様書 — 日本語](SOFTWARE_REQUIREMENTS_SPECIFICATION.md)
- [Contributing Guide — English](CONTRIBUTING.md)
- [コントリビューションガイド — 日本語](CONTRIBUTING_ja.md)
- [Security Policy](SECURITY.md)
- [Code of Conduct — English](CODE_OF_CONDUCT.md)
- [行動規範 — 日本語](CODE_OF_CONDUCT_ja.md)

## Contributing

Contributions are welcome when they preserve the project's local-processing, reproducible Git-state, and conservative safety boundaries.

Before changing behavior, read the SRS and the [Contributing Guide](CONTRIBUTING.md). Keep pull requests focused and update the corresponding documentation and tests when behavior changes.

Security vulnerabilities must be reported privately according to [SECURITY.md](SECURITY.md), not through a public issue or discussion.

## License

`commiter-cli` is licensed under the [MIT License](LICENSE).
