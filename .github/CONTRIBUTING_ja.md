# Contributing to commiter-cli

[English](CONTRIBUTING.md) | 日本語

IssueやPull Requestを作成する前に、既存のIssueとSRSを確認してください。大きな仕様変更や設計判断が必要な場合は、先にIssueで相談します。

## Issue labels

主種別ラベルは原則1つ付与します。

- `enhancement`: 新機能・実装・改善
- `bug`: 不具合修正
- `documentation`: 文書の追加・変更

必要な場合だけ、主種別に加えて次の補助ラベルを付与します。

- `good first issue`: 初参加者でも取り組みやすいIssue
- `help wanted`: 外部コントリビューターの協力を募集するIssue

主種別を判断できない場合は、無理にラベルを付与しません。ラベルの新設や既存ラベルの大幅な変更は、Issueで運用方針を確認してから行います。

## Issue templates

- Feature / Enhancement：新機能や改善の提案
- Bug report：不具合の報告
- Documentation：文書の追加・修正

SRSから生成するImplementation Issueは、対応する要件、依存、成功・失敗条件、実装方針、検証条件を本文に記載します。Decision、Design、Verificationは、それぞれの目的と成果に応じた内容を記載します。一般の起票では、テンプレートの項目を埋められる範囲で具体的に記載してください。

## Branches and pull requests

- 作業は既定ブランチから目的が分かるブランチを作成して行います。
- 1つのPull Requestには、原則として1つのIssueに対応する変更を含めます。
- Pull Request本文から対応Issueを参照し、変更内容と実行した検証を記載します。
- レビューで仕様が変わる場合は、IssueとSRSの整合を確認します。
