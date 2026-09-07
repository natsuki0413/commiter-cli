# commiter-cli ソフトウェア要求仕様書

| 項目 | 内容 |
| --- | --- |
| 文書状態 | Draft v0.1 |
| 作成日 | 2026-08-29 |
| 対象 | 個人利用の v1 |
| 対象実装 | 純 Go の単一バイナリ |

## 1. 目的と背景

対話型コーディングエージェントを毎回起動して差分分析、コミット分割、コミット、push を実行すると、推論時間とトークン消費が発生します。

本システムは、Git 操作と差分前処理を機械的に実行し、コミット計画の生成だけをローカル LLM に委ねることで、実行時間、入力サイズ、外部送信リスクを抑えます。

## 2. 目標

- Git の staged、unstaged、未追跡の変更から、対象範囲を再現可能に確定する。
- ファイル単位で目的を分類し、Conventional Commits の計画を生成する。
- 計画、検証コマンド、機密候補を表示し、既定では明示的な確認後だけ Git の状態を変更する。
- global 設定または CLI によって commit 確認と push 確認を個別に省略できるが、機密内容の読取確認と機密 push 確認は省略できない。
- commit を目的単位に分割し、完了後に一度だけ安全な push を実行する。
- 生成に使用する差分をローカルマシン外へ送信しない。

## 3. 非目標

v1 では Windows と Linux、GUI、クラウド LLM、llama.cpp backend、Homebrew Tap、hunk 単位の分割、AST 解析、submodule 内部への再帰、複数利用者向けの配布を対象にしません。

本リポジトリの初期化フェーズでは README と本仕様書だけを成果物とし、実装コード、Go モジュール、LICENSE、CI、リリース自動化、remote 設定を作成しません。

## 4. 利用者と用語

**利用者**：自分が管理する Git リポジトリで CLI を起動する開発者です。

**pathspec**：Git が解釈する相対パスまたはパスパターンです。

**対象変更**：pathspec、Git status、未追跡ファイルの安全判定で採用されたファイル単位の変更です。tracked file は既存の staged / unstaged の境界を対象選択には使用せず、HEAD から working tree の最終状態までの変更全体を対象とします。

**コミット計画**：対象変更を複数の commit に割り当て、各 commit のメッセージを定めた JSON です。

**機密候補**：`.env*`、`*.pem`、`*.key`、credentials、secret、token、既知の SSH 鍵名など、内容を通常の差分処理へ渡す前に確認が必要なパスです。

**検証 trust**：特定のリポジトリで、特定の設定または manifest のハッシュと実行予定 argv を承認済みとして保存した状態です。

## 5. 前提と制約

v1 の対象 OS は macOS 14 以降、対象アーキテクチャは Apple Silicon、基準機は M3 と 16GB メモリです。

実行時依存は system Git と Ollama だけにし、CGo、Oniguruma、Tree-sitter、クラウド LLM fallback を使用しません。

既定モデルは `qwen3.5:4b-q4_K_M` とし、モデルサイズは公式配布情報を参照して約 3.4GB と扱います。[Qwen3.5 モデル情報](https://ollama.com/library/qwen3.5%3A4b-q4_K_M/blobs/81fb60c7daa8)

## 6. 通常フロー

1. CLI は Git リポジトリの状態、HEAD、branch、index lock、merge、rebase、cherry-pick、revert、conflict、detached HEAD を確認します。
2. CLI は NUL 区切りの Git status から path 一覧と staged / unstaged の状態を取得します。stage 状態は診断と保護のための metadata とし、対象選択の境界には使用しません。
3. CLI は pathspec を適用し、ignored を除外し、tracked file は HEAD から working tree の最終状態までをファイル単位で対象化します。partial stage を含む staged / unstaged 混在ファイルもファイル全体を対象とします。symlink はリンク先へ追従せず Git が追跡するリンク情報として対象化し、submodule は親 repo の pointer 更新だけを対象化します。
4. CLI は機密候補を内容より先に検出し、承認された候補だけをローカル分析へ渡します。
5. CLI は変更をファイル、hunk、言語、サイズ、binary 判定、関連 test、import 差分、hash に整理します。
6. CLI は 8K、16K、32K の順で LLM 入力を作成し、超過時は階層要約を実行します。
7. Ollama は制約された JSON のコミット計画を返します。
8. CLI は全計画と除外一覧を表示し、`Create these N commits? [y/r/N]` を一度だけ提示します。
9. 利用者が承認した場合だけ、信頼済み検証を作業ツリー全体へ一度実行します。
10. 検証成功後、CLI はファイル単位の commit を計画順に作成します。
11. 全 commit 成功後、実際に解決した `<remote>/<branch>` を表示し、通常は `Push to <remote>/<branch>? [y/N]` を提示します。
12. push 成功後、区間別 metrics と作成した commit hash を表示します。

## 7. 公開 CLI

通常実行の形式は次のとおりです。

```text
commiter [flags] [--] [pathspec...]
```

引数なしでは stage 状態にかかわらず、tracked file の HEAD から working tree までの全変更と、安全判定済み未追跡ファイルを対象にします。

pathspec を指定した場合は Git pathspec で対象を限定します。

サブコマンドは `setup [--update-model]`、`doctor`、`config init --global|--repo`、`config show [--effective]`、`config path --global|--repo`、`trust list`、`trust revoke <repo>`、`version` とします。

一時上書きフラグは `--dry-run`、`--no-push`、`--no-confirm-commit`、`--no-confirm-push`、`--language en|ja`、`--model`、`--record-metrics`、`--json` とします。

`--json` は `--dry-run` と読み取り専用サブコマンドに限定し、commit または push を伴う実行との併用を拒否します。

`--dry-run` は差分、除外、計画、検証予定、push 先を表示しますが、index、commit、remote を変更しません。

## 8. 設定と状態

設定の優先順位は `CLI > repo 設定 > global 設定 > 内蔵既定値` とします。

global 設定は `$XDG_CONFIG_HOME/commiter/config.toml` に置き、環境変数が未設定の場合は `~/.config/commiter/config.toml` を使用します。

repo 設定はリポジトリ直下の `.commiter.toml` とします。

状態と trust は `$XDG_STATE_HOME/commiter/` に置き、環境変数が未設定の場合は `~/.local/state/commiter/` を使用します。

metrics の永続化は既定で無効とし、明示設定または `--record-metrics` の場合だけ state 配下の `metrics.jsonl` へ行います。

自動 commit、auto push、機密処理、範囲外 stage 保護、Ollama endpoint は global 設定または明示 CLI だけで変更可能にします。

検証コマンド、glob、言語、モデル、分類補助設定は repo 設定で上書き可能にします。

repo 設定から安全設定を変更する場合は無視して実行せず、設定エラーとして停止します。

### 8.1 設定 schema

設定ファイルは TOML とし、次の v1 schema を使用します。

| key | 型 | 既定値 | 許可元 |
| --- | --- | --- | --- |
| `schema_version` | integer | `1` | global、repo |
| `commit.language` | string | `"en"` | global、repo、CLI |
| `commit.confirm` | boolean | `true` | global、CLI |
| `push.enabled` | boolean | `true` | global、CLI |
| `push.confirm` | boolean | `true` | global、CLI |
| `llm.model` | string | `"qwen3.5:4b-q4_K_M"` | global、repo、CLI |
| `llm.endpoint` | string | `"http://127.0.0.1:11434"` | global |
| `llm.context` | string | `"auto"` | global、repo |
| `llm.max_context_tokens` | integer | `32768` | global、repo |
| `analysis.untracked` | string | `"auto-safe"` | global |
| `analysis.include` | string array | `[]` | global、repo |
| `analysis.exclude` | string array | `[]` | global、repo |
| `analysis.preserve_outside_staged` | boolean | `true` | global |
| `verification.autodetect` | boolean | `true` | global、repo |
| `verification.timeout_seconds` | integer | `600` | global、repo |
| `verification.commands` | table array | `[]` | global、repo |
| `metrics.persist` | boolean | `false` | global、CLI |
| `safety.additional_sensitive_patterns` | string array | `[]` | global |

`verification.commands` の各要素は `name`、`argv`（string array）、`cwd`（string）を必須フィールドとします。

`llm.context` の許容値は `"auto"`、`"8k"`、`"16k"`、`"32k"` とします。

`llm.max_context_tokens` の許容値は 8192、16384、32768 のいずれかとし、`"auto"` の場合は指定された上限まで 8K、16K、32K の順に段階選択します。

`analysis.include` と `analysis.exclude` は doublestar の glob を使用し、repo root 相対に限定します。

配列形式の設定 override は要素追加ではなく置換とします。

`cwd`、`include`、`exclude` は repo root の外側へ移動できないようにします。

`safety.additional_sensitive_patterns` は組み込みの機密 pattern を置換せず、追加 pattern としてだけ適用します。

`provider = "ollama"`、`think = false`、`stream = false`、`keep_alive = 0` は v1 の固定値とし、設定で緩和できないものとします。

未知 key、未対応 `schema_version`、型不一致、repo 設定で禁止された key は exit 2 とします。

## 9. 機能要件

### FR-001 リポジトリ状態の確認

CLI は開始前に対象リポジトリの root、HEAD、branch、index lock、操作中の Git 状態を確認し、曖昧な状態では Git を変更せず停止しなければなりません。

### FR-002 変更範囲の確定

CLI は staged、unstaged、未追跡を別々に取得して状態を把握し、pathspec がある場合は Git pathspec を適用しなければなりません。tracked file の staged / unstaged 境界は対象選択には使用せず、対象となった tracked file は HEAD から working tree の最終状態までの変更全体を一つのファイル変更として扱わなければなりません。

### FR-003 ファイル分類

CLI は各対象ファイルへ安定した file ID、path、status、言語、サイズ、binary 判定、hash を付与しなければなりません。

### FR-004 未追跡ファイルの安全判定

既定モードは `auto-safe` とし、通常テキストは追加差分として扱い、大容量または binary は内容を送らず metadata だけを計画生成へ渡さなければなりません。

### FR-005 差分の構造化

CLI は diff hunk を解析し、変更行、symbol らしき宣言、import 差分、source と test の候補関係を構造化しなければなりません。

### FR-006 入力サイズ制御

CLI は 8K、16K、32K の順に入力を試し、32K を超える場合は file、hunk、chunk の順に要約して最終計画へ渡さなければなりません。

### FR-007 階層要約

階層要約は対象 file ID、path、status、hash の完全な集合を維持し、要約後も対象ファイルの割当漏れを検出できなければなりません。

### FR-008 コミット計画の生成

CLI は Ollama のローカル API へ構造化入力を送り、ファイル単位のコミット計画を取得しなければなりません。

### FR-009 JSON schema 違反の自動修復

JSON schema 違反時はエラー内容を添えて一度だけ自動修復推論を実行し、再度失敗した場合は Git を変更せず停止しなければなりません。

### FR-010 ファイル単位の分割

CLI は同一ファイルを複数 commit へ割り当てず、source と対応 test、依存関係、docs、機械的変更を目的単位にまとめなければなりません。対象ファイルに staged / unstaged が混在する場合も hunk 単位には分割せず、そのファイルの変更全体を同一 commit に含めなければなりません。

### FR-011 計画の確認

CLI は全 commit の順序、メッセージ、割当ファイル、除外ファイルを表示し、一度の確認で承認、再生成、拒否を受け付けなければなりません。

利用者が `r` を選んだ場合は短い補足指示を受けて再推論し、直接編集機能は提供してはなりません。

### FR-012 検証の実行

CLI は計画承認後、commit 前に検証コマンドを作業ツリー全体へ一度だけ実行しなければなりません。

repo 設定がない場合の自動検出は、package.json に実在する `lint`、`typecheck`、`test`、`build` script だけとし、lockfile などから package manager が一意に定まらない場合は自動実行してはなりません。

repo 設定の検証コマンドは argv 配列で指定しなければなりません。

### FR-013 commit の実行

CLI は計画順に明示的なファイル集合の working tree 最終状態を stage し、Git hook と署名設定を尊重して commit を作成しなければなりません。対象ファイルに開始時の partial stage が存在しても、その staged 選択は保持せず、対象ファイル全体の変更として commit に含めなければなりません。対象外ファイルの staged 状態は変更してはなりません。

### FR-014 push の実行

CLI は全 commit 成功後に一度だけ push し、upstream を優先し、upstream がなく remote が一つで branch が安全に確定できる場合だけ `git push -u` を使用しなければなりません。

### FR-015 setup

`commiter setup` は Ollama の導入、daemon 起動、既定モデル取得を個別に確認し、Homebrew 自体を自動導入してはなりません。

### FR-016 doctor

`commiter doctor` は Git、Ollama、loopback API、既定モデル、構造化出力、thinking 無効化、設定、trust、Git identity を読み取り専用で診断しなければなりません。

### FR-017 言語設定

コミット summary は既定で英語とし、設定または `--language ja` により日本語へ切り替えられなければなりません。

言語処理は計画 schema を変更せず追加言語を登録できる境界を持たなければなりません。

### FR-018 metrics

CLI は前処理、model load、prompt 評価、生成、要約、検証、Git、push の時間と、model tag または digest、context 段階、file、line、byte 数、要約回数、終了分類を表示しなければなりません。

### FR-019 daemon lifecycle

通常実行で loopback の Ollama が停止中の場合、CLI は一時的に daemon を起動し、自身が起動した daemon だけを終了しなければなりません。

既存の daemon は再利用し、CLI の終了時に停止してはなりません。

通常実行の Ollama 呼び出しは `keep_alive: 0` を指定しなければなりません。

### FR-020 model lifecycle

通常実行ではモデルの pull と更新を実行してはなりません。

`setup --update-model` は更新内容を表示して明示確認を取得した場合だけモデルを更新しなければなりません。

setup は既存の公式 Ollama App または CLI を再利用し、未導入で Homebrew が存在する場合だけ導入確認、daemon 起動確認、モデル取得確認を個別に提示しなければなりません。

setup は Homebrew 自体を導入してはなりません。

### FR-021 設定と状態コマンド

`config init` は global または repo の初期設定ファイルを生成し、`config show` は有効値と出所を表示し、`config path` は各設定ファイルの絶対パスを表示しなければなりません。

`trust list` は保存済み trust の repo path、hash、argv を表示し、`trust revoke` は指定 trust を削除しなければなりません。

設定の優先順位は CLI、repo、global、内蔵既定値の順とし、安全設定は global または明示 CLI だけで変更可能にしなければなりません。

repo 設定に安全設定の禁止 key が含まれる場合、CLI は設定エラーとして停止しなければなりません。

trust の対象 hash が変化した場合、CLI は再承認を要求しなければなりません。

### FR-022 確認の既定値

commit 確認と push 確認は既定で有効にし、push 自体も既定で有効にしなければなりません。

global 設定と明示 CLI は commit 確認と push 確認を個別に省略できなければなりません。

機密内容の読取確認と機密 commit の push 確認は、設定や CLI によって省略できてはなりません。

### FR-023 commit 計画の完全割当

コミット数には上限を設けず、全対象 file ID をちょうど一つの commit へ割り当てなければなりません。

欠落、重複、範囲外 file ID が一件でもある場合、CLI は Git を変更せず停止しなければなりません。

### FR-024 機械可読出力

`--json` は `--dry-run` と読み取り専用 subcommand だけで使用可能とし、commit または push を伴う通常実行との併用は usage error として exit 2 にしなければなりません。

## 10. LLM 入力と出力

Ollama endpoint は loopback に限定し、`think: false`、`stream: false`、JSON Schema、`keep_alive: 0` を使用します。[Ollama Chat API](https://docs.ollama.com/api/chat) と [Structured Outputs](https://docs.ollama.com/capabilities/structured-outputs) を参照します。

入力には、機械的に計算した repo 状態、対象 file ID、path、status、言語、hash、diff または要約、関連 test 候補だけを含めます。

出力 schema は次の形式を必須とします。

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

`type` は `feat`、`fix`、`docs`、`style`、`refactor`、`perf`、`test`、`build`、`ci`、`chore`、`revert` のいずれかとします。

`scope` は必須の一行文字列とし、空文字列を許可しません。

`breaking` は boolean とし、true の場合だけ subject の scope 後ろへ `!` を付けます。

`summary` は一行とし、commit body、複数行 summary、未割当 file ID、重複 file ID を許可しません。

commit message は通常 `type(scope): summary`、破壊的変更は `type(scope)!: summary` とします。

## 11. 検証 trust

repo 設定を最優先し、設定がない場合は trust 済み `package.json` に実在する `lint`、`typecheck`、`test`、`build` script だけを安全に検出します。

Go、Rust、Python の標準コマンドは自動推測せず、repo 設定の argv 配列で明示します。

初回実行時または設定、manifest、argv の hash が変化した場合は、実行予定 argv、cwd、source hash を表示して承認を求めます。

trust は canonical repo path、設定または manifest の SHA-256、実行予定 argv と紐付けて保存します。

検証コマンドがない場合は `Verification: none` と表示し、追加確認なしで続行します。

## 12. セキュリティと安全要件

### SR-001 ローカル送信境界

差分、prompt、LLM 応答は loopback の Ollama にだけ送信し、クラウド endpoint、テレメトリー、外部 URL へ送信してはなりません。

### SR-002 機密候補の事前確認

CLI は機密候補の内容を読む前に path と検出理由を表示し、一括承認を求めなければなりません。

### SR-003 機密候補の拒否

利用者が拒否した機密候補は内容を読まずに除外し、除外一覧を計画画面へ表示して残りの対象だけを続行しなければなりません。

### SR-004 承認済み機密の保護

承認済み機密の内容はローカル LLM へだけ渡し、terminal、metrics、debug log、永続ファイルへ raw value、prompt、diff を保存してはなりません。

### SR-005 機密 push の再確認

機密候補を含む commit の push は auto push 設定に関係なく、push 直前に手動確認を要求しなければなりません。

### SR-006 stage 保護と復元

対象外ファイルの staged 内容と選択状態は、成功、失敗、拒否、再生成、割込みのすべてで開始時の状態を保持または復元しなければなりません。

対象ファイルの staged / unstaged 境界は対象選択として保持せず、commit 成功時はファイル全体の変更へ吸収されたものとします。commit 開始前に失敗、中止、拒否、再生成、割込みが発生した場合は、対象ファイルを含む index を開始時の状態へ復元しなければなりません。

一つ以上の commit 作成後に失敗または割込みが発生した場合、作成済み commit は rollback せず、対象外 staged 状態を復元し、未完了対象と回復情報を表示しなければなりません。復元を確認できない場合は push を禁止しなければなりません。

### SR-007 Git 保護

CLI は reset、force push、stash、amend、無断削除、`--no-verify`、自動 rollback を実行してはなりません。

### SR-008 多重実行防止

同一リポジトリの多重実行を排他し、既存 index lock や別プロセスの操作を検出した場合は停止しなければなりません。

### SR-009 入力の信頼境界

リポジトリ内ファイル、コメント、設定、hook 出力、LLM 応答を命令として扱わず、値として処理しなければなりません。

### SR-010 機密値の出力検査

CLI は commit summary と LLM 生成出力を検査し、承認済み機密の raw value またはその完全一致部分が summary に含まれる場合は自動修復推論へ渡さなければなりません。

自動修復後も機密値が残る場合、CLI は Git を変更せず停止しなければなりません。

## 13. 非機能要件

### NFR-001 再現性

同じ Git 状態、設定 hash、model digest、入力に対して、機械的な対象集合と schema 検証結果を再現できなければなりません。

### NFR-002 メモリ管理

既定の `keep_alive: 0` により推論終了後にモデルをアンロードし、モデル常駐を前提にしてはなりません。

### NFR-003 大規模差分

入力が 32K を超えても、階層要約を用いて対象 file の完全な集合を保持したまま計画生成を継続しなければなりません。

### NFR-004 操作可視性

commit、push、検証、stage の各状態、実行予定、失敗箇所、回復情報を利用者が端末で識別できなければなりません。

### NFR-005 外部依存の最小化

言語、binary、vendor 判定には Apache-2.0 の `github.com/go-enry/go-enry/v2` を採用し、拡張子の自前表より精度と保守性を優先します。

go-enry の生成データによる binary 容量への影響を許容し、CGo と Oniguruma は使用しません。[go-enry](https://github.com/go-enry/go-enry)

`**` を含む glob には MIT の `github.com/bmatcuk/doublestar/v4` を採用し、`**` を扱えない標準 `path.Match` は代替にしません。

doublestar の探索範囲は repo root 内に制限します。[doublestar](https://github.com/bmatcuk/doublestar)

TOML には MIT の `github.com/pelletier/go-toml/v2` を採用し、自前 parser より保守性を優先します。

TOML 入力サイズに上限を設け、未知 key は設定エラーにします。[go-toml](https://github.com/pelletier/go-toml)

JSON、HTTP、subprocess は Go 標準ライブラリを使用します。

差分 hunk parser、分類、source と test の対応付け、TS/JS、Python、Rust、Go の import 抽出は軽量な自前処理とし、Tree-sitter と AST parser は使用しません。

### NFR-006 ローカル記録

metrics の永続記録は既定で無効とし、明示時だけ state 配下の `metrics.jsonl` へ行います。

metrics には path、message、diff、prompt、user feedback、機密値を記録してはなりません。

### NFR-007 性能計測

v1 では数値性能ゲートを設けず、M3 と 16GB の環境で区間別実測値を収集して次版の基準策定に利用します。

## 14. 障害時の動作

LLM が利用できない、model 未導入、API 互換性診断失敗、timeout、schema 検証失敗、検証失敗、commit 開始前の stage 復元失敗の場合は commit と push を開始しません。

commit の途中で失敗した場合は既に作成した commit を reset せず、hash、未完了の計画、push 未実行を報告します。

push 先を解決できない場合は、利用者が commit を承認していれば local commit まで作成し、push 失敗として停止します。

push 先を解決できない場合の終了コードは 8 とし、ネットワーク push が失敗した場合も作成済み local commit を残して rollback しません。

検証コマンドが予期せずファイルを変更した場合は commit せず停止します。

Ctrl-C を受けた場合は、実行中処理を停止し、stage の復元結果と commit 済み hash を報告します。

## 15. 終了コード

| コード | 分類 |
| ---: | --- |
| 0 | 成功または対象変更なし |
| 1 | 予期しない内部障害 |
| 2 | usage または設定エラー |
| 3 | 利用者による中止 |
| 4 | 安全条件違反または復元失敗 |
| 5 | LLM、Ollama、JSON schema エラー |
| 6 | 検証失敗または検証環境エラー |
| 7 | commit または commit 後の回復エラー |
| 8 | push エラー |
| 130 | SIGINT による割込み |

## 16. 受入条件

### AC-001 対象範囲

staged、unstaged、未追跡を混在させた一時 Git repo で、引数なし実行が安全判定済みの対象だけを収集し、pathspec 実行が範囲を限定することを確認します。

### AC-002 stage の扱い

partial stage と path 外 staged 変更を用意し、対象ファイルでは staged / unstaged の両方がファイル全体の変更として同一 commit に含まれることを確認します。対象外ファイルの staged 内容と選択状態は成功時にも維持され、commit 開始前の検証失敗、利用者中止、Ctrl-C では対象ファイルを含む index が開始時の状態へ復元されることを確認します。commit 作成後の失敗または Ctrl-C では作成済み commit を rollback せず、対象外 staged 状態が復元されることを確認します。

### AC-003 パス種別

rename、delete、binary、symlink、submodule pointer、Unicode path、空白と改行を含む path を用意し、対象外への再帰と誤った内容読み取りがないことを確認します。

### AC-004 機密確認

機密候補の承認時は local LLM へだけ内容を渡し、拒否時は内容を読まずに除外し、どちらも raw value が画面と metrics に現れないことを確認します。

### AC-005 大規模差分

8K を超える変更、16K を超える変更、32K を超える変更を用意し、context 段階、階層要約回数、完全な file ID 集合が記録されることを確認します。

### AC-006 計画分割

source、対応 test、docs、依存変更、機械的変更を含む差分で、file ID の欠落、重複、範囲外割当が検出され、同一ファイルの hunk 分割が行われないことを確認します。

### AC-007 計画再生成

不正 JSON、schema 違反、timeout を返す Ollama fixture を用意し、一度だけ再生成して、失敗時に Git が無変更であることを確認します。

### AC-008 検証 trust

設定または package.json の script、argv、hash が初回と変更後に再承認を要求し、検証なしの場合は `Verification: none` と表示されることを確認します。

### AC-009 commit と hook

複数 commit の計画、成功 hook、失敗 hook、署名設定を用意し、計画順、commit message 形式、未完了時の push 禁止を確認します。

### AC-010 push 解決

upstream あり、remote 一つで upstream なし、複数 remote、branch 不明の各状態で、upstream 優先、条件付き `git push -u`、解決不能時の停止を確認します。

### AC-011 setup と doctor

Ollama 未導入、daemon 停止、model 未取得、model 取得済みの各状態で、setup の個別確認と doctor の非破壊診断を確認します。

### AC-012 設定優先順位

global、repo、CLI に異なる値を設定し、CLI、repo、global、既定値の順に適用され、repo から安全設定を変更できないことを確認します。

設定 schema の全 key について型、既定値、許可元、配列置換、repo root 外の `cwd` と glob の拒否を確認し、未知 key、未対応 schema version、型不一致、禁止 key が exit 2 になることを確認します。

### AC-013 言語

既定設定で英語、`ja` 設定で日本語の summary が生成され、同一計画 schema が維持されることを確認します。

### AC-014 metrics とメモリ

M3 と 16GB の環境で区間別 metrics が表示され、`keep_alive: 0` により推論終了後のモデル解放が Ollama の状態で確認できることを確認します。

### AC-015 daemon と model lifecycle

Ollama daemon の停止中、起動済み、モデル未取得、更新可能の各状態で、既存 daemon の再利用、自身が起動した daemon だけの停止、通常実行での pull なし、setup の個別確認を確認します。

### AC-016 設定と trust コマンド

`config init --global`、`config init --repo`、`config show --effective`、`config path --global`、`config path --repo`、`trust list`、`trust revoke <repo>` の出力と状態変更を確認し、設定 hash の変化で再承認されることを確認します。

### AC-017 JSON 制約と割当

不正な type、空 scope、複数行 summary、body、欠落、重複、範囲外 file ID、複数 commit への同一 file 割当を検出し、Git 無変更で停止することを確認します。

`--json` が `--dry-run` と読み取り専用 subcommand で成功し、commit または push を伴う実行では usage error として exit 2 になることを確認します。

### AC-018 機密値の summary 検査

機密値を含む生成 summary を返す Ollama fixture を用意し、自動修復後も残る場合に Git 無変更で停止し、raw value が metrics とログへ残らないことを確認します。

### AC-019 push 失敗の保持

push 先を解決できない場合とネットワーク push が失敗した場合に、承認済み local commit を残し、rollback せず終了コード 8 を返すことを確認します。

## 17. 要件対応表

| 受入条件 | 対応機能 | 対応安全要件 | 対応非機能要件 |
| --- | --- | --- | --- |
| AC-001 | FR-001〜FR-004 | SR-008 | NFR-001 |
| AC-002 | FR-002、FR-013 | SR-006、SR-007 | NFR-004 |
| AC-003 | FR-002、FR-003 | SR-008 | NFR-001 |
| AC-004 | FR-004 | SR-002〜SR-005、SR-010 | NFR-006 |
| AC-005 | FR-006、FR-007 | SR-001 | NFR-003、NFR-007 |
| AC-006 | FR-005、FR-010 | SR-009 | NFR-001 |
| AC-007 | FR-008、FR-009 | SR-001 | NFR-001 |
| AC-008 | FR-012 | SR-009 | NFR-004 |
| AC-009 | FR-011、FR-013 | SR-007 | NFR-004 |
| AC-010 | FR-014 | SR-007 | NFR-004 |
| AC-011 | FR-015、FR-016 | SR-001 | NFR-005 |
| AC-012 | FR-021、FR-022 | SR-009 | NFR-001 |
| AC-013 | FR-017 | SR-001 | NFR-001 |
| AC-014 | FR-018 | SR-001 | NFR-002、NFR-007 |
| AC-015 | FR-019、FR-020 | SR-001 | NFR-002 |
| AC-016 | FR-021 | SR-009 | NFR-001、NFR-004 |
| AC-017 | FR-009、FR-010、FR-023、FR-024 | SR-009 | NFR-001、NFR-004 |
| AC-018 | FR-009 | SR-004、SR-010 | NFR-006 |
| AC-019 | FR-014 | SR-007 | NFR-004 |

## 18. 将来候補

v1 の受入後に llama.cpp backend、追加言語、Homebrew Tap、実測に基づく性能ゲートを検討します。

将来候補は v1 の実行時依存、CLI、JSON 計画 schema、安全確認の既定値を変更しません。
