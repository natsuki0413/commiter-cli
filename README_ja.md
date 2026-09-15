# commiter-cli

[English](README.md) | 日本語

[![CI](https://github.com/neural-int/commiter-cli/actions/workflows/go.yml/badge.svg)](https://github.com/neural-int/commiter-cli/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`commiter` は、ローカル実行を前提とした Git コミット計画 CLI です。リポジトリの変更を機械的に解析し、ローカルの Ollama モデルに複数コミットの Conventional Commits 計画を生成させ、その計画と現在の Git 状態を検証したうえで、コミット作成と必要に応じた push を実行します。

> **プレリリース状態:** 現時点ではパッケージ化された GitHub Release は公開されていません。現在利用できる導入方法はソースからのビルドです。

## 概要

一般的な AI ベースのコミット支援ツールでは、生の diff をモデルへ渡し、その解釈からメッセージ生成までを任せることがあります。`commiter` は、機械的に決定できる処理と、モデルによる判断が必要な処理を明確に分離します。

Git 状態の確認、対象ファイルの選定、機密ファイルの分類、構文を考慮した前処理、計画の検証、verification、コミット実行、push の安全確認は CLI 側で機械的に行います。ローカル LLM が担当するのは、変更の意味・目的の判断と、ファイルをどのコミットへまとめるかという判断です。

この構成により、リポジトリ内容をローカルに保ち、小規模なローカルモデルへ任せる処理量を抑えながら、Git の変更操作を明示的かつ fail-closed に扱うことを目的としています。

## 主な機能

- **複数コミットの計画生成** — ファイル単位の変更を目的別にまとめ、Conventional Commits 形式のメッセージを生成します。
- **ローカル LLM 推論** — LLM リクエスト先を loopback 上の Ollama に限定し、`commiter` 自身がリポジトリ内容をクラウド LLM へ送信しません。
- **構文を考慮した前処理** — 対応言語では Tree-sitter を利用し、生テキストの構文認識までモデルへ任せる構成を避けます。
- **適応的なコンテキスト処理** — 8K、16K、32K のコンテキストを段階的に使用し、必要な場合は階層要約を行います。
- **機密ファイル保護** — 明らかに機密性の高いファイルは常に除外し、判断が必要な候補は内容を読む前に確認します。
- **計画検証と承認** — モデル出力を検証し、既定ではコミット作成前にユーザー承認を求めます。
- **リポジトリ単位の verification** — 明示的な verification command と package script の自動検出に対応し、信頼情報はリポジトリ単位で管理します。
- **Git 状態の保護** — 解析済みの状態を Git 変更前に再検証し、未解析の状態をそのままコミットしません。
- **dry-run と機械可読出力** — Git を変更せずに計画を確認できます。JSON 出力は dry-run と読み取り専用操作に限定されます。
- **英語・日本語のコミットメッセージ** — 設定または `--language en|ja` で選択できます。

## 処理の流れ

通常の実行では、次の順序で処理します。

1. リポジトリ、branch、HEAD、index、conflict、進行中の Git 操作を検証します。
2. staged / unstaged / untracked の状態を収集し、対象ファイルをファイル単位で決定します。
3. ファイル内容を読む前にパスだけで機密性を分類し、明らかに機密なファイルは自動除外、機密候補は読み取り前に確認します。
4. Git metadata と構文を考慮した structural evidence を生成します。未対応のテキスト形式は raw diff と Git metadata へフォールバックします。
5. 必要に応じて入力を圧縮し、設定されたローカル Ollama モデルに制約付き JSON のコミット計画を生成させます。
6. ファイル割り当てと安全条件を検証し、計画を表示して承認を求めます。
7. 承認済みのリポジトリ単位 verification を実行し、コミット作成前に Git 状態を再検証します。
8. 計画順にコミットを作成し、push が有効な場合はコミット列の完了後に 1 回だけ push します。

解析時に確認した状態と Git 変更直前の状態が一致しない場合、`commiter` は古い前提のまま続行せず、停止または再解析を提案します。

## 必要環境

v1 の対象環境は次のとおりです。

- macOS 14 以降
- Apple Silicon
- Git
- Ollama

現在のソースビルドによる導入では、さらに次が必要です。

- Go 1.23 以降
- Tree-sitter の CGo ビルドに必要な Xcode Command Line Tools、または利用可能な C コンパイラ

開発時の基準環境は M3 Mac / 16 GB メモリです。これは基準環境であり、最小メモリ要件として定義しているわけではありません。

既定モデルは `qwen3.5:4b-q4_K_M`、既定の Ollama endpoint は `http://127.0.0.1:11434` です。

## インストール

パッケージ化されたバイナリおよび Homebrew 配布はまだ公開されていません。実際の Release が提供されるまでは、ソースからビルドします。

```sh
git clone https://github.com/neural-int/commiter-cli.git
cd commiter-cli
go install ./cmd/commiter
```

`go install` は、`GOBIN` が設定されている場合はそのディレクトリへ、未設定の場合は `$(go env GOPATH)/bin` へバイナリを配置します。そのディレクトリが `PATH` に含まれていることを確認してください。

Ollama のモデルはバイナリへ同梱されません。

## クイックスタート

処理対象の Git リポジトリ内で次を実行します。

```sh
commiter setup
commiter doctor
commiter --dry-run
commiter
```

`commiter setup` は、設定された Ollama 環境を確認します。Ollama が未導入で Homebrew が利用できる場合は、確認後に Ollama をインストールできます。また、Ollama の一時起動や設定モデルの pull も確認後に行います。

`commiter doctor` は読み取り専用で、リポジトリ、設定、Git identity、Ollama 接続、設定モデル、structured output 対応などの前提条件を確認します。

`commiter --dry-run` は解析と計画生成を実行しますが、index の変更、コミット作成、push は行いません。

引数なしの `commiter` はコミット作成と push を行う可能性があります。表示される計画と確認プロンプトを確認したうえで承認してください。

## 使い方

### 基本形式

```text
commiter [flags] [--] [pathspec...]
```

pathspec を指定しない場合、`HEAD` から working tree までの tracked change と、安全性チェックを通過した untracked file が対象候補になります。Git pathspec を指定すると処理対象を限定できます。

例:

```sh
# Git を変更せず計画を確認
commiter --dry-run

# この実行だけ日本語のコミットメッセージを生成
commiter --dry-run --language ja

# 対象パスを限定
commiter --dry-run -- src/ internal/

# コミットは作成するが push しない
commiter --no-push
```

### コマンド

| コマンド | 用途 |
| --- | --- |
| `commiter setup [--update-model]` | Ollama と設定されたローカルモデルを準備します。 |
| `commiter doctor` | 環境・capability の読み取り専用チェックを実行します。 |
| `commiter config init --global\|--repo` | global または repository 設定のテンプレートを作成します。 |
| `commiter config show [--effective]` | 解決後の設定値とその参照元を表示します。 |
| `commiter config path --global\|--repo` | 設定ファイルのパスを表示します。 |
| `commiter trust list` | リポジトリ単位の verification trust を一覧表示します。 |
| `commiter trust revoke <repo>` | verification trust を取り消します。 |
| `commiter version` | CLI バージョンを表示します。 |

### 主なフラグ

| フラグ | 動作 |
| --- | --- |
| `--dry-run` | Git を変更せず、解析と計画生成を実行します。 |
| `--no-push` | この実行では push しません。 |
| `--no-confirm-commit` | 通常のコミット計画確認を省略します。安全上必須の確認は省略されません。 |
| `--no-confirm-push` | 通常の push 確認を省略します。安全上必須の確認は省略されません。 |
| `--language en\|ja` | この実行のコミットメッセージ言語を上書きします。 |
| `--model NAME` | この実行で使用する Ollama モデルを上書きします。 |
| `--record-metrics` | この実行のローカル metrics を永続化します。 |
| `--json` | `--dry-run` または対応する読み取り専用コマンドでのみ JSON を出力します。 |

簡易的なコマンド一覧は `commiter --help` で確認できます。

## 設定

設定の優先順位は次のとおりです。

```text
CLI > repository configuration > global configuration > built-in defaults
```

設定ファイルは次のコマンドで作成できます。

```sh
commiter config init --global
commiter config init --repo
```

global 設定は `$XDG_CONFIG_HOME/commiter/config.toml`、`XDG_CONFIG_HOME` が未設定の場合は `~/.config/commiter/config.toml` に保存されます。

repository 設定はリポジトリルートの `.commiter.toml` です。

実際に適用される設定は次で確認できます。

```sh
commiter config show --effective
```

主な既定値は次のとおりです。

| 設定 | 既定値 |
| --- | --- |
| `commit.language` | `"en"` |
| `commit.confirm` | `true` |
| `push.enabled` | `true` |
| `push.confirm` | `true` |
| `llm.model` | `"qwen3.5:4b-q4_K_M"` |
| `llm.endpoint` | `"http://127.0.0.1:11434"` |
| `llm.context` | `"auto"` |
| `llm.max_context_tokens` | `32768` |
| `verification.autodetect` | repository 設定で `true` |
| `verification.timeout_seconds` | repository 設定で `600` |
| `metrics.persist` | `false` |

Ollama endpoint は loopback HTTP URL である必要があります。verification 設定は repository scope のみで、global 設定には記述できません。

設定スキーマ全体と設定元ごとの制約は [ソフトウェア要求仕様書](SOFTWARE_REQUIREMENTS_SPECIFICATION.md) を参照してください。

## プライバシーと安全性

`commiter` では、ローカル解析と保守的な Git 変更を任意のモードではなく設計上の不変条件として扱います。

### ローカル LLM の境界

`commiter` が扱うリポジトリの diff、prompt、LLM response、その他のリポジトリ内容は、LLM 推論・analytics・telemetry のために loopback 外へ送信されません。設定可能な LLM endpoint も loopback HTTP URL に制限されます。

ただし、すべての子プロセスがオフラインになるという意味ではありません。ユーザーが承認した Git push、verification command、Git hook、署名処理、Ollama のモデルダウンロードは、それぞれ独自にネットワーク通信を行う可能性があります。これは `commiter` がリポジトリ内容をクラウド LLM に送信することとは別です。

### 機密ファイル

- 一般的な環境ファイル、private key、credential / secret の保存場所など、明らかに機密性の高いパスは自動除外されます。
- 明らかに機密なファイルを含めるための汎用 CLI / 設定 override はありません。
- 判断が必要な機密候補はパスから判定され、内容を読む前に承認が必要です。
- 機密候補を承認した場合でも、raw value と raw diff は `--dry-run` を含めて表示されません。
- 現在の実行で承認・追加された機密候補を含む push は、通常の push 確認を無効化していても手動確認が必要です。

### 承認・verification・Git 状態確認

- コミット計画の確認は既定で有効です。
- verification definition はリポジトリ単位で管理し、承認済み定義はリポジトリと definition hash の組み合わせで trust されます。
- verification 後、コミット作成前に解析済みの Git 状態を再検証します。
- Git hook は通常どおり実行され、`--no-verify` で回避しません。
- recovery のために reset、stash、amend、force push、automatic rollback を利用しません。
- 無効または安全条件を満たさないモデル出力から直接 Git を変更することはありません。

永続 metrics は既定で無効です。有効化した場合もローカル記録であり、リポジトリパス、メッセージ、diff、prompt、ユーザー feedback、機密値を記録することを意図していません。

脆弱性の報告方法は [SECURITY.md](SECURITY.md)、規範的な安全要件は [ソフトウェア要求仕様書](SOFTWARE_REQUIREMENTS_SPECIFICATION.md) を参照してください。

## 対応言語

### コミットメッセージ言語

`commiter` は次の言語に対応しています。

- English (`en`) — 既定
- 日本語 (`ja`)

永続設定では `commit.language`、実行単位では `--language en|ja` を使用します。

### 構文を考慮した解析

Tree-sitter ベースの structural analysis は次の言語に対応しています。

- Go
- JavaScript / JSX
- TypeScript / TSX
- Python
- Rust
- HTML
- CSS

未対応のテキスト言語、または構文解析に失敗したファイルは raw diff と Git metadata にフォールバックします。binary、large file など内容を扱わない opaque file は、内容ではなく metadata のみで表現されます。

## 制約

現在の v1 scope では、意図的に次を対象外としています。

- Windows、Linux、Intel Mac
- cloud LLM backend、llama.cpp backend
- hunk 単位のコミット分割。コミットへの割り当てはファイル単位です。
- 型解決、cross-file symbol resolution、control-flow graph、data-flow analysis などの semantic static analysis
- submodule の再帰解析。親リポジトリから見える submodule pointer の変更のみを扱います。
- 現時点でのパッケージ化された GitHub Release / Homebrew インストール

現在は loopback 上の Ollama と Apple Silicon macOS 環境を対象としています。

## ドキュメント

- [Software Requirements Specification — English](SOFTWARE_REQUIREMENTS_SPECIFICATION_en.md)
- [ソフトウェア要求仕様書 — 日本語](SOFTWARE_REQUIREMENTS_SPECIFICATION.md)
- [Contributing Guide — English](CONTRIBUTING.md)
- [コントリビューションガイド — 日本語](CONTRIBUTING_ja.md)
- [Security Policy](SECURITY.md)
- [Code of Conduct — English](CODE_OF_CONDUCT.md)
- [行動規範 — 日本語](CODE_OF_CONDUCT_ja.md)

## コントリビューション

プロジェクトのローカル処理、再現可能な Git 状態管理、保守的な安全境界を維持する変更を歓迎します。

挙動を変更する前に SRS と [コントリビューションガイド](CONTRIBUTING_ja.md) を確認してください。Pull Request は対象を絞り、挙動を変更する場合は対応するドキュメントとテストも更新してください。

セキュリティ上の脆弱性は公開 Issue / Discussion ではなく、[SECURITY.md](SECURITY.md) に従って非公開で報告してください。

## ライセンス

`commiter-cli` は [MIT License](LICENSE) で公開されています。
