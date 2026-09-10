# commiter-cli ソフトウェア要求仕様書

| 項目 | 内容 |
| --- | --- |
| 文書状態 | Draft v0.1 |
| 作成日 | 2026-08-29 |
| 対象 | 個人利用の v1 |
| 対象実装 | Go を主体とする単一 CLI バイナリ（Tree-sitter CGo バインディングを内包） |

## 1. 目的と背景

対話型コーディングエージェントを毎回起動して差分分析、コミット分割、コミット、push を実行すると、推論時間とトークン消費が発生します。

本システムは、Git 操作と差分前処理を機械的に実行し、Tree-sitter による syntax-aware structural analysis で構文上の事実を抽出したうえで、変更の意味・目的とコミット計画の生成だけをローカル LLM に委ねます。これにより、低パラメータ・量子化モデルへ構文認識まで負担させることを避け、生成精度と速度を維持しながら入力サイズと外部送信リスクを抑えます。

## 2. 目標

- Git の staged、unstaged、未追跡の変更から、対象範囲を再現可能に確定する。
- Git と構文木から機械的に観測可能な事実を構造化し、変更の意味・目的とファイル grouping だけをローカル LLM に判断させて Conventional Commits の計画を生成する。
- 計画、検証コマンド、機密判定結果を表示し、既定では明示的な確認後だけ Git の状態を変更する。明確な機密ファイルは常に自動除外し、疑義のある機密候補だけを読取前に確認する。
- global 設定または CLI によって commit 確認と push 確認を個別に省略できるが、疑義のある機密候補の読取確認と、今回の実行で機密候補として対象化した file を含む commit の push 確認は省略できない。明確な機密ファイルは設定や CLI で override できず、commiter の commit 対象へ含めない。
- commit を目的単位に分割し、完了後に一度だけ安全な push を実行する。
- 生成に使用する差分をローカルマシン外へ送信しない。

## 3. 非目標

v1 では Windows と Linux、GUI、クラウド LLM、llama.cpp backend、Homebrew Tap、hunk 単位の分割、型解決、symbol resolution、control-flow graph、data-flow analysis などの semantic static analysis、submodule 内部への再帰、複数利用者向けの配布を対象にしません。

本リポジトリの初期化フェーズでは README と本仕様書だけを成果物とし、実装コード、Go モジュール、LICENSE、CI、リリース自動化、remote 設定を作成しません。

## 4. 利用者と用語

**利用者**：自分が管理する Git リポジトリで CLI を起動する開発者です。

**pathspec**：Git が解釈する相対パスまたはパスパターンです。

**対象変更**：pathspec、Git status、未追跡ファイルの安全判定、機密判定と利用者確認を適用した後に採用されたファイル単位の変更です。tracked file は既存の staged / unstaged の境界を対象選択には使用せず、HEAD から working tree の最終状態までの変更全体を対象とします。除外されたファイルは対象変更に含めず、file ID も付与しません。

**構造 evidence**：Git diff と構文木から機械的に観測した事実です。path、status、hunk、構文 node kind、宣言名、enclosing declaration、import / export、call expression、HTML tag / attribute、CSS selector / property などを含み得ますが、変更目的、機能上の関連、`test_for`、`should_group` などの意味的分類や grouping 推奨を含みません。

**コミット計画**：対象変更を複数の commit に割り当て、各 commit のメッセージを定めた JSON です。

**明確な機密ファイル**：`.env`、`.env.*`、`*.pem`、`*.key`、既知の SSH 秘密鍵名、credentials や secret を明示する既知 path など、誤検知より漏えい防止を優先して常に自動除外する path です。v1 では設定や CLI による override を提供せず、commiter の分析、file ID 付与、commit 対象へ含めません。

**機密候補**：名前や配置から機密を含む可能性があるものの、通常の設定ファイルである可能性も残る path です。内容を読む前に利用者確認を必要とし、承認された場合だけローカル分析と commit 対象へ含めます。承認後も raw diff や raw value を terminal または JSON 出力へ表示しません。

**opaque file**：binary、大容量その他の理由で内容を LLM 入力へ渡さない対象ファイルです。内容に基づく意味判定ができないため、当該 file に限って path、status、size、type その他の metadata を grouping の補助根拠として使用できます。

**change_hash**：LLM が分析した変更と commit 直前の変更の同一性を検証するため、対象変更の正規化レコードから計算する SHA-256 です。status、old/new path、old/new mode、HEAD 側 object identity、および working tree 最終状態の identity を含みます。

**canonical repo path**：repository root の絶対 path について symlink を解決した実体 path です。verification trust の scope key と repo 内 cwd 判定に使用します。

**verification definition**：commiter が実際に起動する検証処理の意味を表す正規化対象です。source type、実行順の command 一覧、および各 command の name、repo root 相対の正規化済み cwd、完全な argv を含みます。`package.json` 自動検出では、これに manifest path、script name、script body の完全な文字列を加えます。

**検証 trust**：特定の canonical repo path に対して、verification definition の SHA-256 を利用者が承認済みとして保存した状態です。trust は検証コマンド定義の承認であり、検証から間接的に実行される source、dependency、lockfile その他のコード内容の安全性を保証するものではありません。

## 5. 前提と制約

v1 の対象 OS は macOS 14 以降、対象アーキテクチャは Apple Silicon、基準機は M3 と 16GB メモリです。

実行時の外部依存は system Git と Ollama だけにします。syntax-aware structural analysis には公式 `github.com/tree-sitter/go-tree-sitter` と対象言語 grammar を使用し、Tree-sitter の C 実装を CGo 経由で単一 CLI バイナリへ組み込みます。Tree-sitter 用途以外へ CGo の利用範囲を拡大せず、外部 parser executable、runtime shared grammar、Oniguruma、クラウド LLM fallback を要求しません。

既定モデルは `qwen3.5:4b-q4_K_M` とし、モデルサイズは公式配布情報を参照して約 3.4GB と扱います。[Qwen3.5 モデル情報](https://ollama.com/library/qwen3.5%3A4b-q4_K_M/blobs/81fb60c7daa8)

## 6. 通常フロー

1. CLI は Git リポジトリの状態、HEAD、branch、index lock、merge、rebase、cherry-pick、revert、conflict、detached HEAD を確認します。
2. CLI は NUL 区切りの Git status から path 一覧と staged / unstaged の状態を取得します。stage 状態は診断と保護のための metadata とし、対象選択の境界には使用しません。
3. CLI は pathspec を適用し、ignored を除外し、tracked file は HEAD から working tree の最終状態までをファイル単位で対象化します。partial stage を含む staged / unstaged 混在ファイルもファイル全体を対象とします。rename は old path と new path を持つ一つの変更として扱い、一つの file ID を付与します。symlink はリンク先へ追従せず Git が追跡するリンク情報として対象化し、submodule は親 repo の pointer 更新だけを対象化します。
4. CLI は path だけで機密判定を行い、明確な機密ファイルを常に自動除外します。明確な機密ファイルを対象化する override は提供しません。機密候補は内容を読む前に確認し、承認された候補だけをローカル分析へ渡します。
5. CLI は機密判定後の対象変更について、Git metadata と Tree-sitter による構造 evidence を生成します。v1 の構文解析対象は Go、JavaScript、JSX、TypeScript、TSX、Python、Rust、HTML、CSS とし、未対応言語または構文解析に失敗した text file は raw diff と Git metadata へ fallback します。opaque file は内容を渡さず metadata だけを計画生成へ渡します。機械側は source / test、docs / source、同一 feature などの意味的関係や grouping 推奨を生成しません。
6. CLI は各対象変更へ file ID と change_hash を付与し、8K、16K、32K の順で LLM 入力を作成し、超過時は階層要約を実行します。
7. Ollama は制約された JSON のコミット計画を返します。CLI は Git mutation 前に schema、file assignment、安全条件を検証します。
8. CLI は全計画と除外一覧を表示し、`Create these N commits? [y/r/N]` を一度だけ提示します。
9. 利用者が承認した場合だけ、承認済み verification definition に基づく検証を作業ツリー全体へ一度実行します。
10. verification 後、commit 開始直前に CLI は HEAD、対象変更の change_hash、index、および対象 untracked 集合を再検証します。ignored output だけの変化は許容します。tracked working tree、index、または対象 untracked 集合が変化した場合は commit と push を開始せず、verification が生成した working tree の変更は残したまま index を開始時状態へ復元し、変更 path を表示して `Re-analyze changed state? [y/N]` を提示します。利用者が `y` を選んだ場合は現在の Git 状態から手順 1 へ戻り、拒否した場合は exit 4 とします。
11. 再検証が一致した場合だけ、CLI はファイル単位の commit を計画順に作成します。各 commit の stage 直前に、その commit へ割り当てられた未処理 file ID の current change_hash が分析時 snapshot と一致することを再確認します。Git hook は通常の Git operation の一部として原則許容し、割当済み file ID に対応する path の内容を hook が変更すること自体は許容します。commit 作成後は parent との差分に現れる file set が当該 commit の割当 file ID と正確に一致することを検証し、割当外 file の混入または割当 file の欠落があれば後続 commit と push を停止します。
12. 全 commit 成功後、実際に解決した `<remote>/<branch>` を表示し、通常は `Push to <remote>/<branch>? [y/N]` を提示します。通常の Git push semantics に従い、今回の実行前から存在した outgoing commit も push 対象に含まれ得ます。
13. push 成功後、区間別 metrics と作成した commit hash を表示します。

## 7. 公開 CLI

通常実行の形式は次のとおりです。

```text
commiter [flags] [--] [pathspec...]
```

引数なしでは stage 状態にかかわらず、tracked file の HEAD から working tree までの全変更と、安全判定済み未追跡ファイルを対象にします。

pathspec を指定した場合は Git pathspec で対象を限定します。

サブコマンドは `setup [--update-model]`、`doctor`、`config init --global|--repo`、`config show [--effective]`、`config path --global|--repo`、`trust list`、`trust revoke <repo>`、`version` とします。

一時上書きフラグは `--dry-run`、`--no-push`、`--no-confirm-commit`、`--no-confirm-push`、`--language en|ja`、`--model`、`--record-metrics`、`--json` とします。

明確な機密ファイルを対象化する override flag は提供しません。

`--json` は `--dry-run` と読み取り専用サブコマンドに限定し、commit または push を伴う実行との併用を拒否します。

`--dry-run` は通常対象の差分、除外、計画、検証予定、push 先を表示しますが、index、commit、remote を変更しません。機密候補は承認済みであっても raw diff や raw value を terminal または JSON 出力へ表示せず、path、status その他の非機密 metadata だけを表示します。

## 8. 設定と状態

設定の優先順位は `CLI > repo 設定 > global 設定 > 内蔵既定値` とします。

global 設定は `$XDG_CONFIG_HOME/commiter/config.toml` に置き、環境変数が未設定の場合は `~/.config/commiter/config.toml` を使用します。

repo 設定はリポジトリ直下の `.commiter.toml` とします。

状態と trust は `$XDG_STATE_HOME/commiter/` に置き、環境変数が未設定の場合は `~/.local/state/commiter/` を使用します。

metrics の永続化は既定で無効とし、明示設定または `--record-metrics` の場合だけ state 配下の `metrics.jsonl` へ行います。

commit 確認と auto push は global 設定または明示 CLI だけで変更可能にし、機密判定への追加 pattern と Ollama endpoint は global 設定からのみ変更可能にします。対象外 staged 内容と選択状態の保護は v1 の変更不能 invariant とし、設定または CLI で無効化できません。

verification 全体は repo-scoped とし、verification command、autodetect、timeout は repo 設定だけで指定可能にします。global 設定から verification を指定してはなりません。

glob、言語、モデル、分類補助設定は repo 設定で上書き可能にします。

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
| `verification.autodetect` | boolean | `true` | repo |
| `verification.timeout_seconds` | integer | `600` | repo |
| `verification.commands` | table array | unset | repo |
| `metrics.persist` | boolean | `false` | global、CLI |
| `safety.additional_sensitive_patterns` | string array | `[]` | global |

`verification.commands` の各要素は `name`、`argv`（string array）、`cwd`（string）を必須フィールドとします。`verification.commands` は repo 設定でのみ指定でき、1件以上の command を含まなければなりません。空配列は設定エラーとします。

`verification.commands` が repo 設定で明示されている場合はその command 一覧を使用します。未指定の場合だけ `verification.autodetect` を評価し、true なら repo root の `package.json` から自動検出し、false なら `Verification: none` とします。

`llm.context` の許容値は `"auto"`、`"8k"`、`"16k"`、`"32k"` とします。

`llm.max_context_tokens` の許容値は 8192、16384、32768 のいずれかとし、`"auto"` の場合は指定された上限まで 8K、16K、32K の順に段階選択します。

`analysis.include` と `analysis.exclude` は doublestar の glob を使用し、repo root 相対に限定します。

配列形式の設定 override は要素追加ではなく置換とします。

`include` と `exclude` は repo root 相対に限定します。verification command の `cwd` は symlink 解決後の実体 path を canonical repo path と比較し、repository root の外側を指す場合は設定エラーとして拒否します。

`safety.additional_sensitive_patterns` は組み込みの明確な機密 pattern を置換せず、自動除外対象へ追加する pattern としてだけ適用します。

`provider = "ollama"`、`think = false`、`stream = false`、`keep_alive = 0` は v1 の固定値とし、設定で緩和できないものとします。

未知 key、未対応 `schema_version`、型不一致、または schema の許可元と異なる設定ファイルに記述された key は exit 2 とします。したがって global 設定内の `verification.*` は設定エラーとして拒否します。

## 9. 機能要件

### FR-001 リポジトリ状態の確認

CLI は開始前に対象リポジトリの root、HEAD、branch、index lock、操作中の Git 状態を確認し、曖昧な状態では Git を変更せず停止しなければなりません。

### FR-002 変更範囲の確定

CLI は staged、unstaged、未追跡を別々に取得して状態を把握し、pathspec がある場合は Git pathspec を適用しなければなりません。tracked file の staged / unstaged 境界は対象選択には使用せず、対象となった tracked file は HEAD から working tree の最終状態までの変更全体を一つのファイル変更として扱わなければなりません。

Git が rename として認識した変更は削除と追加の二つの file ID へ分割せず、old path と new path を持つ一つの変更として一つの file ID を付与しなければなりません。

### FR-003 ファイル分類

CLI は機密判定と除外処理が完了した後の各対象変更へ安定した file ID、status、old path、new path、言語、サイズ、binary 判定、change_hash を付与しなければなりません。rename 以外では old path と new path のうち非該当側を null として正規化できます。自動除外または利用者拒否されたファイルへ file ID を付与してはなりません。

change_hash は schema version `1`、status、old path、new path、old mode、new mode、HEAD 側の Git object identity、working tree 最終状態の kind と identity を含む canonical JSON の UTF-8 byte 列から SHA-256 で計算しなければなりません。非該当 field は null とし、object key 順序を固定します。

通常ファイルと binary file の working tree identity は最終 file bytes の SHA-256、symlink はリンク先へ追従せず symlink target byte 列の SHA-256、submodule は親 repository が保持する gitlink commit OID、削除は null とします。未追跡 file の HEAD 側 object identity は null とします。

### FR-004 未追跡ファイルの安全判定

既定モードは `auto-safe` とし、通常テキストは追加差分として扱い、大容量または binary は opaque file として内容を LLM へ送らず metadata だけを計画生成へ渡さなければなりません。

v1 では working tree 最終状態の file size が 64 KiB 以上の未追跡通常テキストを大容量と判定します。
binary は size にかかわらず opaque file とします。

opaque file は内容に基づく意味判定ができないため、当該 file に限り path、status、size、type その他の metadata を grouping の補助根拠として使用することを許可します。

### FR-005 syntax-aware structural analysis

CLI は機密判定後の対象 text file について diff hunk と対象ファイルの構文木を対応付け、Git から得られる事実に加えて、変更箇所を含む構文 node kind、宣言名、enclosing declaration、import / export、call expression など、構文から機械的に観測可能な構造 evidence を生成しなければなりません。

v1 の Tree-sitter 対応言語は Go、JavaScript、JSX、TypeScript、TSX、Python、Rust、HTML、CSS とします。HTML では tag と attribute、CSS では selector と declaration property を構造 evidence として扱えるものとします。

未対応言語、grammar 未対応、構文エラーその他の理由で十分な構造 evidence を取得できない text file は、当該ファイルだけ raw diff と Git metadata へ fallback して処理を継続しなければなりません。構文解析の失敗だけを理由に対象ファイルまたは実行全体を除外してはなりません。

機械側は変更目的、feature、source / test、docs / source、同一 logical change、`test_for`、`related_to`、`should_group` などの意味的関係または grouping 推奨を生成してはなりません。

### FR-006 入力サイズ制御

CLI は構造 evidence と必要な diff hunk を優先して LLM 入力を構成しなければなりません。

`llm.context = "auto"` の場合は、`llm.max_context_tokens` を上限として 8K、16K、32K の順に context 段階を選択します。`llm.context` が `"8k"`、`"16k"`、`"32k"` の固定値の場合は、その指定段階を上限とし、より大きい context 段階へ自動昇格してはなりません。

context 選択前に、最終 prompt の UTF-8 byte 数を入力 token 数の保守的上限とし、chat template 用の固定 256 token と、`max(1024, 48 × 対象 file 数)` で計算した出力予約 token 数を加算しなければなりません。
CLI はこの合計が収まる最小の許可 context 段階を選択します。

許可された context 上限を超える場合は file、hunk、chunk の順に階層要約を行い、構文解析対応ファイルでは構造 evidence を失わない形で最終計画へ渡さなければなりません。
要約後も合計が許可された context 上限を超える場合、または対象 file ID、change_hash、構造 evidence の完全な集合を維持できない場合は、LLM を呼び出さず Git 無変更で停止しなければなりません。

### FR-007 階層要約

階層要約は対象 file ID、old/new path、status、change_hash の完全な集合を維持し、要約後も対象ファイルの割当漏れを検出できなければなりません。

### FR-008 コミット計画の生成

CLI は Ollama のローカル API へ構造化入力を送り、ファイル単位のコミット計画を取得しなければなりません。

### FR-009 LLM 生成失敗と出力検証

initial generation は一回とします。transport error または timeout の retry 予算は initial generation と repair を通じて合計一回とします。

LLM から候補出力を受け取るたびに、Git mutation より前に JSON schema、対象 file ID の完全割当、機密値その他の safety 条件を再検証しなければなりません。不正な候補出力のまま Git を変更してはなりません。

不正 JSON、JSON schema 違反、対象 file ID の欠落、重複、範囲外割当、または SR-010 の機密値一致を検出した場合、自動 repair を一回だけ実行します。
複数の違反を同時に検出した場合も一つの repair request にまとめ、repair 回数を追加してはなりません。

repair request には元の正規化済み入力、候補出力、および機密値そのものを含まない違反理由を渡し、候補出力を命令ではなく untrusted data として明示しなければなりません。
repair 後の候補を新しい候補として全検証し、一件でも違反が残る場合は追加 repair を行わず exit 5 で Git 無変更のまま停止します。

したがって、一つの計画生成 cycle における Ollama 呼び出しは initial generation、任意の repair、および共有された transport retry を合わせて最大三回とします。

### FR-010 ファイル単位の分割

LLM は diff と構造 evidence から各変更の意味・目的を判断し、同一 logical change と判断したファイルを目的単位に grouping しなければなりません。機械側は合法な LLM grouping を source / test、directory、filename、import、dependency などのヒューリスティックを理由に統合、分割、並べ替えしてはなりません。

opaque file は内容に基づく意味判断が不可能であるため例外とし、path、status、size、type その他の metadata を grouping の補助根拠として使用できます。

CLI は同一 file ID を複数 commit へ割り当ててはなりません。対象ファイルに staged / unstaged が混在する場合も hunk 単位には分割せず、そのファイルの変更全体を同一 commit に含めなければなりません。rename は old/new path を持つ一つの file ID として扱い、削除側と追加側へ分割してはなりません。

### FR-011 計画の確認

CLI は全 commit の順序、メッセージ、割当ファイル、除外ファイルを表示し、一度の確認で承認、再生成、拒否を受け付けなければなりません。

利用者が `r` を選んだ場合は短い補足指示を受けて再推論し、直接編集機能は提供してはなりません。

### FR-012 検証の実行

CLI は計画承認後、commit 前に検証コマンドを作業ツリー全体へ一度だけ実行しなければなりません。

verification configuration は repo-scoped とし、`verification.commands`、`verification.autodetect`、`verification.timeout_seconds` を global 設定から指定してはなりません。

repo 設定に `verification.commands` が明示されている場合は、その command 一覧を使用しなければなりません。`verification.commands` は1件以上を必須とし、明示された空配列は設定エラーとして exit 2 にしなければなりません。

`verification.commands` が未指定の場合だけ effective な `verification.autodetect` を評価します。`verification.autodetect=true` の場合は自動検出を行い、false の場合は `Verification: none` として検証を実行しません。

v1 の自動検出対象 manifest は repo root の `package.json` 一つだけとし、そこに実在する `lint`、`typecheck`、`test`、`build` script だけをこの順序で候補化します。package manager が repo root の情報から一意に定まらない場合は自動実行してはなりません。Go、Rust、Python の標準コマンドは自動推測しません。

repo 設定の検証コマンドは shell string ではなく argv 配列で指定しなければなりません。verification command は Git-visible state を継続的に変更しないものを設定しなければなりません。

CLI は verification 前後で Git-visible state を比較し、ignored output だけの生成または変更は許容します。tracked working tree、index、または対象 untracked 集合の変化は verification mutation として扱わなければなりません。

verification 完了後かつ commit 列の開始直前に、CLI は HEAD と全対象変更の change_hash を再計算し、分析時 snapshot と一致することを確認しなければなりません。verification mutation または snapshot 不一致を検出した場合は commit と push を開始せず、working tree の変更を残し、index を実行開始時状態へ復元し、変更 path を表示して `Re-analyze changed state? [y/N]` を提示しなければなりません。`y` の場合は現在の状態から分析をやり直し、拒否した場合は exit 4 とします。

### FR-013 commit の実行

CLI は計画順に明示的なファイル集合の working tree 最終状態を stage し、Git hook と署名設定を尊重して commit を作成しなければなりません。対象ファイルに開始時の partial stage が存在しても、その staged 選択は保持せず、対象ファイル全体の変更として commit に含めなければなりません。

対象外ファイルの staged 内容と選択状態の保護は v1 の変更不能 invariant とし、設定や CLI によって無効化できません。

各 commit の stage 直前に、当該 commit へ割り当てられた未処理 file ID の current change_hash を再計算し、分析時 snapshot と一致することを確認しなければなりません。不一致の場合は当該 commit を開始せず、後続 commit と push を停止して安全条件違反として扱わなければなりません。これにより、先行 commit の hook、IDE、外部プロセス等が後続 commit 対象を変更した場合も未分析内容を stage してはなりません。

Git hook は通常の Git operation の一部として原則許容します。hook が当該 commit に割り当てられた file ID に対応する path の内容または index 上の内容を変更すること自体は、commiter 固有の invariant を破らない限り許容します。hook の変更後に元の change_hash と一致することは要求しません。

各作成 commit の parent との差分に現れる file set は、その commit に割り当てられた file ID に対応する file set と正確に一致しなければなりません。hook その他の処理によって対象外 staged 変更または別 commit に割り当てられた file が混入した場合、あるいは割当 file が commit から欠落した場合は commiter 固有の security invariant 違反とします。

commit 作成後にこの invariant 違反を検出した場合、作成済み commit を自動 rollback せず、違反 path を表示し、後続 commit と push を禁止して exit 7 としなければなりません。

### FR-014 push の実行

CLI は全 commit 成功後に一度だけ push し、upstream を優先し、upstream がなく remote が一つで branch が安全に確定できる場合だけ `git push -u` を使用しなければなりません。

push は通常の Git push semantics に従い、現在 branch から解決先 remote ref へ送信される全 outgoing commit を対象とします。今回の commiter 実行より前から存在した outgoing commit がある場合も対象から除外せず、auto push が有効ならそれらを含めて自動 push できます。commiter は既存 outgoing commit の内容を今回の差分分析または機密判定の対象として再解析しません。この挙動を push 前の表示で明示しなければなりません。

### FR-015 setup

`commiter setup` は Ollama の導入、daemon 起動、既定モデル取得を個別に確認し、Homebrew 自体を自動導入してはなりません。

### FR-016 doctor

`commiter doctor` は Git、Ollama、loopback API、既定モデル、構造化出力、thinking 無効化、設定、trust、Git identity を読み取り専用で診断しなければなりません。

### FR-017 言語設定

コミット summary は既定で英語とし、設定または `--language ja` により日本語へ切り替えられなければなりません。

言語処理は計画 schema を変更せず追加言語を登録できる境界を持たなければなりません。

### FR-018 metrics

CLI は Git 前処理、syntax analysis、model load、prompt 評価、生成、要約、検証、Git、push の時間と、model tag または digest、context 段階、file、line、byte 数、Tree-sitter 解析成功 / fallback file 数、要約回数、終了分類を表示しなければなりません。

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

`trust list` は保存済み trust の canonical repo path、verification definition hash、source type、argv を表示し、`trust revoke` は指定 trust を削除しなければなりません。

設定の優先順位は CLI、repo、global、内蔵既定値の順とし、安全設定は global または明示 CLI だけで変更可能にしなければなりません。ただし verification configuration は repo-scoped のみとし、global 設定または CLI から指定できてはなりません。対象外 staged 内容と選択状態の保護、および明確な機密ファイルの常時除外は v1 の変更不能 invariant とし、どの設定元からも緩和できません。

repo 設定に安全設定の禁止 key が含まれる場合、CLI は設定エラーとして停止しなければなりません。

現在の verification definition hash が保存済み trust hash と一致しない場合、CLI は検証を実行する前に再承認を要求しなければなりません。

### FR-022 確認の既定値

commit 確認と push 確認は既定で有効にし、push 自体も既定で有効にしなければなりません。

global 設定と明示 CLI は commit 確認と push 確認を個別に省略できなければなりません。

機密候補の読取確認と、今回の実行で機密候補として対象化した file を含む commit の push 確認は、設定や汎用の確認省略 CLI によって省略できてはなりません。明確な機密ファイルは常に自動除外し、対象化する override を提供してはなりません。

### FR-023 commit 計画の完全割当

コミット数には上限を設けず、全対象 file ID をちょうど一つの commit へ割り当てなければなりません。

欠落、重複、範囲外 file ID が一件でもある場合、CLI は Git を変更せず停止しなければなりません。

### FR-024 機械可読出力

`--json` は `--dry-run` と読み取り専用 subcommand だけで使用可能とし、commit または push を伴う通常実行との併用は usage error として exit 2 にしなければなりません。

## 10. LLM 入力と出力

Ollama endpoint は loopback に限定し、`think: false`、`stream: false`、JSON Schema、`keep_alive: 0` を使用します。commiter v1 が必要とする API 機能と既定モデル `qwen3.5:4b-q4_K_M` の動作互換性を基準に、対応する Ollama は `0.18.2` 以上とし、下位版、`0.18.2` の prerelease、不正な version 応答は API 非互換として扱います。[Ollama Chat API](https://docs.ollama.com/api/chat)、[Structured Outputs](https://docs.ollama.com/capabilities/structured-outputs)、[Ollama v0.18.2](https://github.com/ollama/ollama/releases/tag/v0.18.2) を参照します。

入力には、機械的に計算した repo 状態、対象 file ID、old/new path、status、言語、change_hash、構造 evidence、必要な raw diff hunk または階層要約を含めます。構造 evidence は構文上の観測事実に限定し、source / test、docs / source、同一 feature、同一 logical change などの意味的 relation label や grouping 推奨を含めません。

LLM は各ファイルの実際の変更内容から変更目的を判断し、その目的をファイル間で比較して grouping を決定します。通常ファイルでは path、同一 directory、類似 filename、import 関係、構文 node の近さだけを grouping の決定根拠として扱ってはなりません。opaque file に限り、内容を取得しないことによる情報不足を補うため metadata を grouping の補助根拠として使用できます。

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

verification configuration は repo-scoped とし、global verification configuration は存在しません。

repo 設定に `verification.commands` が明示されている場合は、その command 一覧を最優先します。`verification.commands` は1件以上を必須とし、空配列は設定エラーです。

`verification.commands` が未指定の場合だけ `verification.autodetect` を評価します。`verification.autodetect=true` の場合は repo root の `package.json` に実在する `lint`、`typecheck`、`test`、`build` script をこの順序で安全に自動検出します。false の場合は検証なしとします。package manager が repo root の情報から一意に定まらない場合は自動実行しません。

Go、Rust、Python の標準コマンドは自動推測せず、repo 設定の argv 配列で明示します。

canonical repo path は repository root の絶対 path に対して symlink を解決した実体 path とします。verification command の cwd も実行前に symlink を解決し、その実体 path が canonical repo path 配下に存在する場合だけ許可します。repository 外を指す cwd、解決不能な cwd は設定エラーとして拒否します。

CLI は検証実行前に現在の verification definition を決定し、その正規化表現の SHA-256 を trust hash として計算しなければなりません。verification definition は次を含みます。

- schema version `1`
- source type（`repo_config` または `package_json_autodetect`）
- 実行順を保持した command 一覧
- 各 command の `name`
- repo root 相対へ正規化した `cwd`
- 引数境界と順序を保持した完全な `argv`
- `package_json_autodetect` の場合だけ、manifest path、script name、script body の完全な文字列

正規化表現は UTF-8 の canonical JSON とし、object key 順序を固定し、不要な空白を含めず、command と argv の配列順序を保持します。trust hash はこの byte 列の SHA-256 とします。canonical repo path は hash へ含めず、trust record の scope key として別に保存します。

repo 設定由来では `.commiter.toml` 全体を hash してはならず、verification definition に採用された command 定義だけを hash 対象とします。`package.json` 自動検出では manifest 全体を hash してはならず、採用した script の manifest path、script name、script body だけを hash 対象とします。

source file、Git HEAD、lockfile、dependency content、verification と無関係な manifest script、model、commit、analysis その他の設定は trust hash に含めてはなりません。ただし package manager の解決結果などが変わって最終 argv が変化した場合は、argv の変化として trust hash が変化しなければなりません。

初回実行、trust record 不在、または現在の verification definition hash が保存済み hash と異なる場合、CLI は source type、実行予定 command の name、argv、cwd、自動検出時の script name と script body、現在の trust hash を表示し、検証実行前に承認を求めなければなりません。

hash が一致する場合は再承認を要求せず、同じ verification definition を実行できます。検証コマンドがない場合は `Verification: none` と表示し、trust record の作成や追加確認なしで続行します。

## 12. セキュリティと安全要件

### SR-001 ローカル送信境界

commiter 自身が LLM 推論、分析、telemetry その他の補助処理のために、差分、prompt、LLM 応答その他の repository content を loopback 外へ送信してはなりません。

ただし、利用者が許可した Git push、および利用者が承認した verification command、Git hook、署名処理等の子プロセスによる通信は本制約の対象外とします。

### SR-002 機密ファイルの事前判定

CLI は内容を読む前に path だけで機密判定を行わなければなりません。明確な機密ファイルは質問せず常に自動除外し、除外理由を表示しなければなりません。v1 では設定または CLI による override を提供せず、分析、file ID 付与、commit 対象へ含めてはなりません。

機密候補は内容を読む前に path と検出理由を表示し、一括承認を求めなければなりません。

path 判定は Unicode を保持した repo root 相対の正規化 path に対して component 単位で行い、ASCII の大文字小文字を区別しません。
各 directory component と、extension を除いた basename を `.`, `-`, `_` で分割した token が `auth`、`credential`、`credentials`、`secret`、`secrets`、`token`、`tokens`、`password`、`passwd` のいずれかに完全一致する path は機密候補とします。
部分文字列だけの一致により `authentication.go` や `tokenizer.go` を候補にしてはなりません。

`.npmrc`、`.pypirc`、`.netrc`、`.docker/config.json`、および basename が `kubeconfig` の path は機密候補とします。
ただし、明確な機密ファイルの組み込み pattern または `safety.additional_sensitive_patterns` に一致する場合は候補ではなく常に自動除外します。

### SR-003 機密候補の拒否

利用者が拒否した機密候補は内容を読まずに除外し、除外一覧を計画画面へ表示して残りの対象だけを続行しなければなりません。自動除外または拒否されたファイルは対象変更と file ID 集合から除外しなければなりません。

### SR-004 対象化された機密候補の保護

機密候補として承認されたファイルの内容は、commiter プロセス内の構造解析と loopback のローカル LLM にだけ渡し、terminal、JSON 出力、metrics、debug log、永続ファイルへ raw value、prompt、raw diff を出力または保存してはなりません。

`--dry-run` でも承認済み機密候補の raw diff を表示せず、path、status その他の非機密 metadata だけを表示しなければなりません。

### SR-005 機密候補 push の再確認

今回の実行で機密候補として承認・対象化された file を含む commit の push は auto push 設定に関係なく、push 直前に手動確認を要求しなければなりません。

実行前から存在する outgoing commit は FR-014 のとおり今回の差分分析や機密判定で再解析しないため、本要件の判定対象外とします。

### SR-006 stage 保護、復元、verification mutation

対象外ファイルの staged 内容と選択状態は、成功、失敗、拒否、再生成、割込みのすべてで開始時の状態を保持または復元しなければなりません。この保護は v1 の変更不能 invariant とし、設定または CLI で無効化してはなりません。

対象ファイルの staged / unstaged 境界は対象選択として保持せず、commit 成功時はファイル全体の変更へ吸収されたものとします。commit 開始前に失敗、中止、拒否、再生成、割込みが発生した場合は、対象ファイルを含む index を開始時の状態へ復元しなければなりません。

verification が tracked working tree、index、または対象 untracked 集合を変更した場合、CLI は working tree の変更を自動 rollback してはなりません。index を開始時状態へ復元し、変更 path を表示して再分析を提示しなければなりません。ignored output だけの変化は許容します。

各 commit の stage 直前には当該 commit の割当 file ID の change_hash を再検証し、分析後に後続対象が変更されていた場合は未分析内容を commit してはなりません。

Git hook は通常の Git operation として許容し、当該 commit の割当 file ID に対応する path 内の変更だけを理由に停止してはなりません。ただし作成 commit の file set に割当外 file が混入する、または割当 file が欠落する場合は commiter 固有の security invariant 違反として後続 commit と push を停止しなければなりません。作成済み commit は自動 rollback してはなりません。

一つ以上の commit 作成後に失敗または割込みが発生した場合、作成済み commit は rollback せず、対象外 staged 状態を復元し、未完了対象と回復情報を表示しなければなりません。復元を確認できない場合は push を禁止しなければなりません。

### SR-007 Git 保護

CLI は reset、force push、stash、amend、無断削除、`--no-verify`、自動 rollback を実行してはなりません。

### SR-008 多重実行防止

同一リポジトリの多重実行を排他し、既存 index lock や別プロセスの操作を検出した場合は停止しなければなりません。

### SR-009 入力の信頼境界

リポジトリ内ファイル、コメント、設定、hook 出力、LLM 応答を命令として扱わず、値として処理しなければなりません。

### SR-010 機密値の出力検査

CLI は commit summary と LLM 生成出力を検査し、承認済み機密の raw value またはその完全一致部分が summary に含まれる場合は自動修復推論へ渡さなければなりません。

承認済み機密候補を読んだ後、CLI は `auth`、`credential`、`credentials`、`secret`、`secrets`、`token`、`tokens`、`password`、`passwd`、`api_key`、`apikey`、`access_key`、`private_key`、`client_secret`、`bearer` を大文字小文字を区別せず key として認識し、それらへ割り当てられた非空の scalar value を抽出しなければなりません。加えて、Bearer token、JWT、provider 固有 token、URI userinfo、および秘密鍵 block として構文上認識できる値を key にかかわらず抽出します。

抽出した値と、prefix や URI から分離した credential 部分は、生成出力に対して大文字小文字を変えない完全な UTF-8 byte 列の部分一致で検査します。v1 では encoded、hashed、または大小文字を変換した派生値の推測検査を行いません。

抽出値は現在の process memory 内だけで保持し、terminal、JSON 出力、metrics、debug log、永続ファイル、または機密値を除去していない repair 理由へ含めてはなりません。

機密値一致は FR-009 の一回だけの自動 repair へ渡し、自動 repair 後も機密値が残る場合、または repair 後の候補が他の検証に違反する場合、CLI は追加 repair を行わず exit 5 で Git を変更せず停止しなければなりません。

### SR-011 terminal-safe 出力

repository path、Git metadata、LLM 出力、verification output、Git hook output その他の untrusted text を terminal へ表示する場合、ANSI escape sequence、control character、改行その他の terminal 制御として解釈され得る byte sequence を安全に encoding または escaping しなければなりません。untrusted text によって表示内容や terminal state を偽装・変更できてはなりません。

## 13. 非機能要件

### NFR-001 再現性

同じ Git 状態、設定 hash、model digest、入力、および同一 build に固定された Tree-sitter / grammar version に対して、機械的な対象集合、構造 evidence、schema 検証結果を再現できなければなりません。

### NFR-002 メモリ管理

既定の `keep_alive: 0` により推論終了後にモデルをアンロードし、モデル常駐を前提にしてはなりません。

### NFR-003 大規模差分

入力が 32K を超えても、階層要約を用いて対象 file の完全な集合を保持したまま計画生成を継続しなければなりません。

### NFR-004 操作可視性

commit、push、検証、stage の各状態、実行予定、失敗箇所、回復情報を利用者が端末で識別できなければなりません。

### NFR-005 外部依存の最小化

言語、binary、vendor 判定には Apache-2.0 の `github.com/go-enry/go-enry/v2` を採用し、拡張子の自前表より精度と保守性を優先します。

go-enry の生成データによる binary 容量への影響を許容し、Oniguruma は使用しません。[go-enry](https://github.com/go-enry/go-enry)

syntax-aware structural analysis には公式 `github.com/tree-sitter/go-tree-sitter` と公式 grammar の Go bindings を採用します。Tree-sitter core と grammar の C code は CGo で build 時に単一 CLI バイナリへ組み込み、実行時に外部 parser executable や shared grammar library を要求しません。CGo の利用はこの構文解析境界に限定します。[go-tree-sitter](https://github.com/tree-sitter/go-tree-sitter)

`**` を含む glob には MIT の `github.com/bmatcuk/doublestar/v4` を採用し、`**` を扱えない標準 `path.Match` は代替にしません。

doublestar の探索範囲は repo root 内に制限します。[doublestar](https://github.com/bmatcuk/doublestar)

TOML には MIT の `github.com/pelletier/go-toml/v2` を採用し、自前 parser より保守性を優先します。

TOML 入力サイズに上限を設け、未知 key は設定エラーにします。[go-toml](https://github.com/pelletier/go-toml)

JSON、HTTP、subprocess は Go 標準ライブラリを使用します。

差分 hunk と構文 node range の対応付け、構造 evidence の正規化、LLM 入力への圧縮は Go 側で実装します。Tree-sitter は syntax-aware structural analysis に限定して使用し、型解決、cross-file symbol resolution、call graph、control-flow graph、data-flow analysis などの semantic static analysis へ拡張しません。

### NFR-006 ローカル記録

metrics の永続記録は既定で無効とし、明示時だけ state 配下の `metrics.jsonl` へ行います。

metrics には path、message、diff、prompt、user feedback、機密値を記録してはなりません。

### NFR-007 性能計測

v1 では数値性能ゲートを設けず、M3 と 16GB の環境で区間別実測値を収集して次版の基準策定に利用します。

## 14. 障害時の動作

LLM が利用できない、model 未導入、API 互換性診断失敗、許容された retry 後も transport error または timeout が解消しない、最終候補が schema / assignment / safety 検証を通過しない、検証失敗、commit 開始前の stage 復元失敗の場合は commit と push を開始しません。

個別ファイルの Tree-sitter grammar 未対応、構文エラー、または構造 evidence 抽出失敗は致命エラーとせず、当該ファイルだけ raw diff と Git metadata へ fallback します。

verification 後の再検証で tracked working tree、index、対象 untracked 集合、HEAD、または対象 change_hash の変化を検出した場合は、verification が生成した working tree の変更を残し、index を開始時状態へ復元します。変更 path を terminal-safe に表示し、`Re-analyze changed state? [y/N]` を提示します。`y` なら現在の状態から対象選択と分析をやり直し、拒否した場合は exit 4 とします。verification command は Git-visible state を継続的に変更しないものを設定する必要があり、再分析後も同じ verification mutation が発生する場合は利用者が verification definition を修正できるよう原因 command を表示します。

ignored file だけの生成または変更は verification mutation とみなさず処理を継続できます。

各 commit の stage 直前の change_hash 再検証で不一致を検出した場合は、その commit を開始せず、既に作成済みの commit は保持したまま後続 commit と push を停止し、変更された path と未完了計画を報告します。

Git hook その他の通常 Git operation により作成済み commit の file set が割当 file ID と一致しない場合は、作成済み commit を reset せず、違反 path、未完了計画、push 未実行を報告して exit 7 とします。

commit の途中でその他の失敗が発生した場合も既に作成した commit を reset せず、hash、未完了の計画、push 未実行を報告します。

push 先を解決できない場合は、利用者が commit を承認していれば local commit まで作成し、push 失敗として停止します。

push 先を解決できない場合の終了コードは 8 とし、ネットワーク push が失敗した場合も作成済み local commit を残して rollback しません。

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

partial stage と path 外 staged 変更を用意し、対象ファイルでは staged / unstaged の両方がファイル全体の変更として同一 commit に含まれることを確認します。対象外ファイルの staged 内容と選択状態は成功時にも維持され、この保護を設定または CLI から無効化できないことを確認します。

各作成 commit の parent との差分が、その commit に割り当てられた file ID の変更だけを含み、対象外 staged file や別 commit の file ID が混入しないことを確認します。

commit 開始前の検証失敗、利用者中止、Ctrl-C では対象ファイルを含む index が開始時の状態へ復元されることを確認します。commit 作成後の失敗または Ctrl-C では作成済み commit を rollback せず、対象外 staged 状態が復元されることを確認します。

### AC-003 パス種別

rename、delete、binary、symlink、submodule pointer、Unicode path、空白と改行を含む path を用意し、対象外への再帰と誤った内容読み取りがないことを確認します。

rename が old path と new path を持つ一つの file ID として表現され、削除と追加の二つの file ID へ分割されないことを確認します。通常 file、未追跡、削除、rename、binary、symlink、submodule pointer について change_hash が定義どおり計算され、内容、path、mode、object identity の対象要素が変化した場合に change_hash が変化することを確認します。

### AC-004 機密確認

明確な機密ファイルが質問なしで常に自動除外され、file ID を付与されず、CLI または設定による override が存在しないことを確認します。

Unicode path、ASCII 大文字小文字、directory component、`.`、`-`、`_` で分割される basename token、`.npmrc`、`.pypirc`、`.netrc`、`.docker/config.json`、basename `kubeconfig` の fixture を用意し、SR-002 の完全一致規則に従って機密候補が判定されることを確認します。`authentication.go` や `tokenizer.go` の部分文字列一致だけでは候補にならず、組み込みの明確な機密 pattern または `safety.additional_sensitive_patterns` に一致する path は候補確認より自動除外が優先されることを確認します。

機密候補は読取前に確認され、承認時は local LLM へだけ内容を渡し、拒否時は内容を読まずに除外して file ID を付与しないことを確認します。承認済み候補についても raw value と raw diff が通常表示、`--dry-run`、`--json`、metrics、debug log、永続ファイルに現れないことを確認します。

### AC-005 大規模差分

8K、16K、32K の各段階に収まる fixture と上限を超える fixture を用意し、最終 prompt の UTF-8 byte 数、chat template 用 256 token、`max(1024, 48 × 対象 file 数)` の出力予約 token に基づいて、許可された最小 context 段階が選択されることを確認します。

`llm.context = "auto"` では `llm.max_context_tokens` の範囲内で 8K、16K、32K の順に段階選択され、固定 context では指定段階を超えて自動昇格しないことを確認します。上限超過時は file、hunk、chunk の順に階層要約され、要約後も上限を超える場合、または完全な file ID、change_hash、構造 evidence 集合を維持できない場合は、LLM を呼び出さず Git 無変更で停止することを確認します。

### AC-006 構造解析と計画分割

Go、JavaScript、JSX、TypeScript、TSX、Python、Rust、HTML、CSS の fixture で、変更 hunk に対応する構文 node、宣言、import / export、HTML tag / attribute、CSS selector / property などの構造 evidence が取得されることを確認します。構造 evidence に source / test、同一 feature、`should_group` などの意味的 relation label が含まれないことを確認します。

未対応言語または意図的に構文解析を失敗させた text file が raw diff と Git metadata へ fallback し、実行全体が継続することを確認します。

source、test、docs、依存変更、機械的変更を含む差分で、LLM が意味・目的に基づいて grouping し、機械側が合法な grouping を書き換えないこと、file ID の欠落、重複、範囲外割当が検出され、同一ファイルの hunk 分割が行われないことを確認します。opaque file についてだけ metadata を grouping の補助根拠として使用できることを確認します。

### AC-007 LLM retry と出力検証

transport error と timeout を返す Ollama fixture を用意し、transport retry の予算が initial generation と repair を通じて合計一回だけ共有されることを確認します。一つの計画生成 cycle における Ollama 呼び出しが、initial generation、任意の repair、共有 transport retry を合わせて最大三回に制限されることを確認します。

不正 JSON、schema 違反、欠落・重複・範囲外 file ID、SR-010 の機密値一致を返す fixture では、複数違反を一つの repair request にまとめ、自動 repair が合計一回だけ実行されることを確認します。repair request に機密値そのものが含まれず、元の候補出力が untrusted data として扱われることを確認します。

repair 後の候補を全検証し、一件でも違反が残る場合は追加 repair を行わず exit 5 となり、index、commit、remote を含む Git state が変更されないことを確認します。

### AC-008 検証 trust

global 設定に `verification.autodetect`、`verification.timeout_seconds`、`verification.commands` を記述した場合は設定エラーとして exit 2 になり、verification configuration が repo-scoped に限定されることを確認します。

repo 設定で `verification.commands` が1件以上明示された場合はその command 一覧が autodetect より優先されることを確認します。明示された空配列は設定エラーとして exit 2 になることを確認します。

`verification.commands` が未指定かつ `verification.autodetect=true` の場合だけ repo root の `package.json` が自動検出対象になり、未指定かつ `verification.autodetect=false` では `Verification: none` になることを確認します。repo root の情報から package manager が一意に定まらない場合は自動実行されないことを確認します。

初回または trust record 不在では承認が要求され、同一 verification definition の再実行では再承認されないことを確認します。

repo 設定由来では command の name、cwd、argv、command 順序の変更で trust hash が変化し、verification と無関係な `.commiter.toml` の設定変更では変化しないことを確認します。

`package.json` 自動検出では採用 script の script name または script body、manifest path、最終 argv、command 順序の変更で trust hash が変化し、採用されていない script やその他の manifest field、lockfile、source file、Git HEAD、dependency content の変更だけでは変化しないことを確認します。

repository root を symlink 経由と実体 path 経由の双方から起動し、同じ canonical repo path の trust scope として扱われることを確認します。verification cwd が symlink 解決後に repo root 外を指す場合は設定エラーになることを確認します。

hash 変化時は source type、argv、cwd、自動検出時の script name と script body、現在の trust hash を表示して再承認を要求し、検証なしの場合は `Verification: none` と表示して trust を作成しないことを確認します。

### AC-009 commit と hook

複数 commit の計画、成功 hook、失敗 hook、署名設定を用意し、計画順、commit message 形式、未完了時の push 禁止を確認します。

先行 commit の hook または外部プロセスが後続 commit に割り当てられた file を変更する fixture を用意し、次の commit の stage 直前 change_hash 検証で不一致を検出して当該 commit を開始しないことを確認します。

pre-commit hook が現在の commit に割り当てられた file の内容だけを formatter 等で変更する場合は通常の Git operation として許容され、元の change_hash との一致を要求しないことを確認します。

hook が対象外 staged file または別 commit の file を現在の commit へ混入させる場合、および割当 file を commit から欠落させる場合は、作成 commit の file set 検証で security invariant 違反を検出し、作成済み commitを rollbackせず、後続 commit と push を停止して exit 7 になることを確認します。

### AC-010 push 解決

upstream あり、remote 一つで upstream なし、複数 remote、branch 不明の各状態で、upstream 優先、条件付き `git push -u`、解決不能時の停止を確認します。

実行前から remote tracking ref より ahead の local commit を用意し、その後 commiter が新しい commit を作成した場合、通常の Git push semantics に従って既存 outgoing commit と今回作成 commit の両方が push 対象になること、auto push 有効時にも既存 outgoing commit を除外しないこと、既存 outgoing commit は今回の差分分析や機密判定で再解析されないことが push 前に明示されることを確認します。

### AC-011 setup と doctor

Ollama 未導入、daemon 停止、model 未取得、model 取得済みの各状態で、setup の個別確認と doctor の非破壊診断を確認します。

### AC-012 設定優先順位

global、repo、CLI に異なる値を設定し、CLI、repo、global、既定値の順に適用され、repo から安全設定を変更できないことを確認します。

設定 schema の全 key について型、既定値、許可元、配列置換、repo root 外の `cwd` と glob の拒否を確認し、未知 key、未対応 schema version、型不一致、許可元外の key が exit 2 になることを確認します。global 設定の `verification.*` が拒否されること、`analysis.preserve_outside_staged` が schema に存在せず対象外 staged 保護を設定から無効化できないことを確認します。

### AC-013 言語

既定設定で英語、`ja` 設定で日本語の summary が生成され、同一計画 schema が維持されることを確認します。

### AC-014 metrics とメモリ

M3 と 16GB の環境で区間別 metrics が表示され、`keep_alive: 0` により推論終了後のモデル解放が Ollama の状態で確認できることを確認します。

### AC-015 daemon と model lifecycle

Ollama daemon の停止中、起動済み、モデル未取得、更新可能の各状態で、既存 daemon の再利用、自身が起動した daemon だけの停止、通常実行での pull なし、setup の個別確認を確認します。

### AC-016 設定と trust コマンド

`config init --global`、`config init --repo`、`config show --effective`、`config path --global`、`config path --repo`、`trust list`、`trust revoke <repo>` の出力と状態変更を確認します。`trust list` が canonical repo path、verification definition hash、source type、argv を表示し、`trust revoke <repo>` 後の次回検証で再承認されることを確認します。

### AC-017 JSON 制約と割当

不正な type、空 scope、複数行 summary、body、欠落、重複、範囲外 file ID、複数 commit への同一 file 割当を検出し、Git 無変更で停止することを確認します。

`--json` が `--dry-run` と読み取り専用 subcommand で成功し、commit または push を伴う実行では usage error として exit 2 になることを確認します。

### AC-018 機密値の summary 検査

承認済み機密候補に対して SR-010 の機密 key に割り当てられた非空 scalar value、Bearer token、JWT、provider 固有 token、URI userinfo、秘密鍵 block を含む fixture を用意し、抽出値と credential 部分が生成出力に対する大文字小文字を変えない完全な UTF-8 byte 列の部分一致で検査されることを確認します。encoded、hashed、または大小文字を変換した派生値は v1 の検査対象にならないことを確認します。

機密値一致を含む生成出力では FR-009 の共有された一回だけの自動 repair が実行され、repair 後も一致または他の検証違反が残る場合は exit 5 で Git 無変更のまま停止することを確認します。抽出した raw value が terminal、`--json`、metrics、debug log、永続ファイル、機密値を除去していない repair 理由へ残らないことを確認します。

### AC-019 push 失敗の保持

push 先を解決できない場合とネットワーク push が失敗した場合に、承認済み local commit を残し、rollback せず終了コード 8 を返すことを確認します。

### AC-020 verification mutation と再分析

verification fixture が tracked working tree、index、対象 untracked file をそれぞれ変更するケースを用意し、commit が開始されず、working tree の変更が保持され、index が開始時状態へ復元され、変更 path と再分析確認が表示されることを確認します。`y` では現在の Git 状態から対象選択、change_hash 計算、LLM 分析をやり直し、拒否時は exit 4 になることを確認します。

ignored file だけを生成または変更する verification fixture では mutation とみなさず処理を継続することを確認します。verification 後かつ commit 列開始前に対象 file を外部プロセスから変更したケースでも、全対象 change_hash の再検証が不一致を検出することを確認します。

### AC-021 terminal-safe 出力

ANSI escape sequence、control character、改行を含む path、LLM 出力、verification output、Git hook output の fixture を用意し、terminal 制御として解釈されず安全に encoding または escaping され、表示内容や terminal state を偽装できないことを確認します。

## 17. 要件対応表

| 受入条件 | 対応機能 | 対応安全要件 | 対応非機能要件 |
| --- | --- | --- | --- |
| AC-001 | FR-001〜FR-004 | SR-008 | NFR-001 |
| AC-002 | FR-002、FR-013 | SR-006、SR-007 | NFR-004 |
| AC-003 | FR-002、FR-003 | SR-008 | NFR-001 |
| AC-004 | FR-004 | SR-002〜SR-005、SR-010 | NFR-006 |
| AC-005 | FR-006、FR-007 | SR-001 | NFR-003、NFR-007 |
| AC-006 | FR-005、FR-010 | SR-009 | NFR-001、NFR-005 |
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
| AC-020 | FR-012、FR-013 | SR-006、SR-007 | NFR-001、NFR-004 |
| AC-021 | FR-016 | SR-009、SR-011 | NFR-004 |

## 18. 将来候補

v1 の受入後に llama.cpp backend、追加言語、Homebrew Tap、実測に基づく性能ゲートを検討します。

将来候補は v1 の実行時依存、CLI、JSON 計画 schema、安全確認の既定値を変更しません。
