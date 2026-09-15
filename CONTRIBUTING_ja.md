# commiter-cli へのコントリビューション

[English](CONTRIBUTING.md) | 日本語

`commiter-cli` へのコントリビューションを検討していただきありがとうございます。

`commiter-cli` は、再現可能な Git state の取り扱い、ローカル限定の LLM 分析、保守的な安全境界を重視して設計しています。利便性のためにこれらの性質を弱めるのではなく、維持する変更を前提とします。

## はじめに

挙動を変更する前に、ソフトウェア要求仕様書を確認してください。

- [ソフトウェア要求仕様書 (日本語)](SOFTWARE_REQUIREMENTS_SPECIFICATION.md)
- [Software Requirements Specification (English)](SOFTWARE_REQUIREMENTS_SPECIFICATION_en.md)

Issue や Pull Request を作成する前に、既存の Issue と SRS を確認してください。大きな仕様変更や設計判断が必要な場合は、先に Issue で相談してください。

v1 の主対象は macOS 14 以降の Apple Silicon です。CLI は主に Go で実装し、実行時には system Git と Ollama を使用します。

FR、SR、NFR、AC のいずれかで定義された挙動を変更する場合は、同じ Pull Request で該当する仕様本文と受入条件も更新してください。英語版と日本語版の SRS は内容を一致させてください。

## Issue

### Issue labels

主種別ラベルは原則1つ付与します。

- `enhancement`: 新機能・実装・改善
- `bug`: 不具合修正
- `documentation`: 文書の追加・変更

必要な場合だけ、主種別に加えて次の補助ラベルを付与します。

- `good first issue`: 初参加者でも取り組みやすい Issue
- `help wanted`: 外部コントリビューターの協力を募集する Issue

主種別を判断できない場合は、無理にラベルを付与しません。ラベルの新設や既存ラベルの大幅な変更は、Issue で運用方針を確認してから行います。

### Issue templates

- Feature / Enhancement: 新機能や改善の提案
- Bug report: 不具合の報告
- Documentation: 文書の追加・修正

SRS から生成する Implementation Issue は、対応する要件、依存、成功・失敗条件、実装方針、検証条件を本文に記載します。Decision、Design、Verification は、それぞれの目的と成果に応じた内容を記載します。一般の起票では、テンプレートの項目を埋められる範囲で具体的に記載してください。

## 開発環境

必要なもの:

- Go 1.23 以降
- Git
- Ollama（Ollama に依存する挙動を開発・テストする場合）

リポジトリを clone し、依存関係を取得します。

```sh
git clone https://github.com/neural-int/commiter-cli.git
cd commiter-cli
go mod download
```

標準の確認コマンドを実行します。

```sh
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/commiter
```

生成したバイナリや、変更と無関係なローカルファイルは commit しないでください。

## 変更の作成

最新の `main` から目的ごとの branch を作成し、1つの Pull Request は1つの一貫した目的に限定してください。原則として、1つの Pull Request は1つの Issue に対応させます。

既存の package 境界に沿った小さな変更を優先してください。要求された挙動に不要なリファクタリング、フォーマットだけの大規模差分、不要な依存追加は避けてください。

Go コードでは以下を守ってください。

- `gofmt` を実行する。
- 明示的な error と決定的な挙動を優先する。
- subprocess 実行時の引数境界を保持し、argv ベースの実行を shell string 実行へ置き換えない。
- 挙動変更や回帰に対する test を追加または更新する。
- repository path、Git output、hook output、verification output、LLM output などの untrusted text を terminal-safe に扱う。

## 安全性に関わる変更

以下の挙動には意図的な security invariant が含まれます。これらに触れる変更では、特に慎重な確認と対応する test が必要です。

明示的な仕様変更なしに、以下の保証を弱めないでください。

- LLM 分析に使用する repository content は loopback / local processing の外へ送信しない。
- 明確な機密ファイルは自動除外し、汎用 override で対象化できない。
- 機密候補は内容を読む前に確認する。
- 対象外 staged content と staged selection を保護する。
- verification trust は repository-scoped のままにする。
- `--no-verify` を使用せず Git hook を尊重する。
- commiter は force push、reset、stash、amend、自動 rollback を実行しない。
- 不正または安全でない LLM output によって Git mutation が発生しない。

fixture や例に実際の credentials、private key、token、`.env` の内容、その他の secret を追加しないでください。

## テスト

新しい挙動には、意味のある最小の package 境界で test を追加してください。bug fix には回帰 test を追加してください。

Pull Request を作成する前に以下を実行します。

```sh
go test ./...
go vet ./...
go build ./cmd/commiter
```

CGo または Tree-sitter integration に影響する変更では、対応する Apple Silicon build path も確認してください。

Git mutation、verification、hook、機密ファイル処理、LLM plan validation に影響する変更では、失敗経路の test も追加し、Git state が SRS で要求された状態に保たれることを確認してください。

## ドキュメント

repository 内のドキュメントへのリンクには相対リンクを使用してください。

要件、command、configuration、安全挙動、contributor workflow を変更する場合は、同じ Pull Request で関連ドキュメントも更新してください。翻訳ドキュメントは意味を一致させ、元仕様が変更されない限り code block、configuration key、requirement ID、command name、file name、数値 threshold は翻訳間で同一にしてください。

## Pull Request

Pull Request には以下を含めてください。

- 問題と採用したアプローチの説明
- 目的に限定された差分
- 必要な test と documentation update
- 実行した validation command
- 該当する場合は security または Git state への影響
- 対応する Issue がある場合はその参照
- review で仕様が変わる場合は、Issue と SRS の整合確認

Pull Request で Issue を完全に解決する場合は、次のような自動 close keyword を含めてください。

```text
Closes #123
```

review 指摘への対応も同じく焦点を維持し、正確性や安全性のために必要でない限り、指摘範囲を超えて変更を拡大しないでください。

## Commit Message

可能な範囲で Conventional Commits を使用してください。

```text
feat(cli): add command
fix(git): preserve staged state
docs: add English SRS
test(safety): cover sensitive-file exclusion
```

各 commit は内部的に一貫した目的へまとめ、無関係な変更を混在させないでください。

## License と Conduct

コントリビューションは、このリポジトリに適用される [MIT License](LICENSE) の下で提供されるものとします。

すべての参加者は [Code of Conduct (行動規範)](CODE_OF_CONDUCT_ja.md) に従ってください。セキュリティ脆弱性は公開 Issue や Discussion ではなく、[Security Policy](SECURITY.md) に従って報告してください。
