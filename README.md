# commiter-cli

`commiter-cli` は、Git の変更を機械的に収集し、ローカル LLM が作成した Conventional Commits の計画を確認してから、複数の commit と push を実行する個人用 CLI です。

## 状態

このリポジトリは個人用 v1 の要件定義段階です。

実装コード、Go モジュール、配布設定はまだありません。

詳細な要件は [SOFTWARE_REQUIREMENTS_SPECIFICATION.md](SOFTWARE_REQUIREMENTS_SPECIFICATION.md) に記載します。

## 対象環境

v1 は macOS 14 以降の Apple Silicon Mac を対象にし、基準機を M3、メモリ 16GB とします。

実行時には system Git と Ollama を使用し、クラウド LLM へ変更内容を送信しません。

## 採用予定の構成

CLI は純 Go の単一バイナリとして実装します。

既定のローカルモデルは `qwen3.5:4b-q4_K_M` です。

差分の収集、ファイル単位の分類、入力の圧縮、JSON 計画の検証、Git 操作は CLI が担当し、Ollama はコミット計画の生成だけを担当します。

## 次段階

文書レビュー後に実装設計と最小実装を作成し、その後にテスト用 Git リポジトリと M3 と 16GB の実機での性能計測を追加します。
