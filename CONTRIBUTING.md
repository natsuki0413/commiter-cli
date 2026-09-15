# Contributing to commiter-cli

English | [日本語](CONTRIBUTING_ja.md)

Thank you for considering a contribution to `commiter-cli`.

`commiter-cli` is designed around reproducible Git-state handling, local-only LLM analysis, and conservative safety boundaries. Contributions should preserve those properties rather than trading them away for convenience.

## Before You Start

Read the software requirements specification before changing behavior:

- [Software Requirements Specification (English)](SOFTWARE_REQUIREMENTS_SPECIFICATION_en.md)
- [ソフトウェア要求仕様書 (日本語)](SOFTWARE_REQUIREMENTS_SPECIFICATION.md)

Before opening an issue or pull request, review existing issues and the SRS. If a change needs a substantial specification update or a design decision, discuss it in an issue first.

For v1, the primary target is macOS 14 or later on Apple Silicon. The CLI is implemented primarily in Go and uses system Git and Ollama at runtime.

If a change alters behavior described by an FR, SR, NFR, or AC requirement, update the affected specification text and acceptance criteria in the same pull request. Keep the English and Japanese SRS versions aligned.

## Issues

### Issue labels

Apply one primary type label in principle.

- `enhancement`: new features, implementation, or improvements
- `bug`: bug fixes
- `documentation`: documentation additions or changes

Add the following secondary labels only when needed, in addition to the primary type.

- `good first issue`: issues that first-time contributors can take on
- `help wanted`: issues seeking help from external contributors

If the primary type is unclear, do not force a label. Create new labels or make substantial changes to existing labels only after confirming the policy in an issue.

### Issue templates

- Feature / Enhancement: propose a new feature or improvement
- Bug report: report a defect
- Documentation: add or fix documentation

Implementation issues generated from the SRS should include the related requirements, dependencies, success and failure conditions, implementation approach, and verification conditions in the body. Decision, Design, and Verification issues should include content that matches their purpose and expected outcome. For ordinary issue reports, fill in the template fields as specifically as you can.

## Development Setup

Requirements:

- Go 1.23 or later
- Git
- Ollama when working on or testing Ollama-dependent behavior

Clone the repository and download dependencies:

```sh
git clone https://github.com/neural-int/commiter-cli.git
cd commiter-cli
go mod download
```

Run the standard checks:

```sh
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/commiter
```

Do not commit generated binaries or unrelated local files.

## Making Changes

Create a focused branch from the latest `main` and keep each pull request limited to one coherent purpose. In principle, one pull request should correspond to one issue.

Prefer small changes that fit the existing package boundaries. Avoid unrelated refactors, formatting-only churn, or dependency additions that are not needed for the requested behavior.

For Go code:

- Run `gofmt`.
- Prefer explicit errors and deterministic behavior.
- Preserve argument boundaries when executing subprocesses; do not replace argv-based execution with shell-string execution.
- Add or update tests for behavior changes and regressions.
- Keep terminal output safe for untrusted repository paths, Git output, hook output, verification output, and LLM output.

## Safety-sensitive Changes

Several behaviors are intentional security invariants. Changes touching them require particular care and corresponding tests.

Do not weaken these guarantees without an explicit specification change:

- repository content used for LLM analysis stays within loopback/local processing;
- clearly sensitive files are automatically excluded and cannot be opted in through a generic override;
- sensitive candidates are confirmed before their contents are read;
- out-of-scope staged content and staged selection are preserved;
- verification trust remains repository-scoped;
- Git hooks are respected without using `--no-verify`;
- commiter does not perform force push, reset, stash, amend, or automatic rollback;
- invalid or unsafe LLM output cannot cause Git mutation.

Never add real credentials, private keys, tokens, `.env` contents, or other secrets to fixtures or examples.

## Tests

New behavior should include tests at the narrowest useful package boundary. Add regression tests for bug fixes.

Before opening a pull request, run:

```sh
go test ./...
go vet ./...
go build ./cmd/commiter
```

When a change affects CGo or Tree-sitter integration, also verify the supported Apple Silicon build path.

When a change affects Git mutation, verification, hooks, sensitive-file handling, or LLM-plan validation, include failure-path tests that confirm Git state is left in the state required by the SRS.

## Documentation

Use repository-relative links for repository documents.

When changing requirements, commands, configuration, safety behavior, or contributor workflow, update the relevant documentation in the same pull request. Keep translated documents semantically aligned; code blocks, configuration keys, requirement IDs, command names, file names, and numeric thresholds should remain identical across translations unless the underlying specification changes.

## Pull Requests

A pull request should:

- explain the problem and the chosen approach;
- keep the diff focused on the stated purpose;
- include relevant tests and documentation updates;
- state the validation commands that were run;
- call out security or Git-state implications when applicable;
- reference the related issue when one exists;
- confirm that the issue and SRS remain consistent if review changes the specification.

When the pull request fully resolves an issue, include an automatic-closing keyword such as:

```text
Closes #123
```

Review feedback should be addressed with the same focus: fix the identified issue without expanding the scope unless the broader change is required for correctness or safety.

## Commit Messages

Use Conventional Commits where practical, for example:

```text
feat(cli): add command
fix(git): preserve staged state
docs: add English SRS
test(safety): cover sensitive-file exclusion
```

Keep each commit internally coherent and avoid mixing unrelated changes.

## License and Conduct

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE) that applies to this repository.

All participants must follow the [Code of Conduct](CODE_OF_CONDUCT.md). Security vulnerabilities must be reported according to the [Security Policy](SECURITY.md), not through a public issue or discussion.
