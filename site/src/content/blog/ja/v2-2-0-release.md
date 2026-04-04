---
title: "Tetora v2.2 — デフォルトで安全、マルチテナント Dispatch"
lang: ja
date: "2026-03-30"
tag: release
readTime: "~6 min"
excerpt: "DangerousOpsConfig が危険なコマンドを実行前に阻止。マルチテナント --client フラグ、Worktree 障害保護、History CLI による失敗分析——v2.2 は agent dispatch を本番グレードに引き上げます。"
description: "Tetora v2.2 は DangerousOpsConfig によるコマンド遮断、マルチテナント dispatch 隔離、Worktree 障害保護、Self-liveness watchdog、History CLI 診断ツールを追加。3 リリースで 30 以上の改善。"
---

Tetora v2.2 は 3 つの柱を強化します：**安全性、実行信頼性、マルチテナント隔離**。v2.2.0 から v2.2.2 の 3 リリースに渡る 30 以上の改善で、マルチ agent 並列 dispatch はより堅牢になり、エンタープライズグレード展開の基盤が整いました。

> **TL;DR：** DangerousOpsConfig が agent 実行前に破壊的コマンドを遮断。Worktree 隔離が全タスクをカバー。新しい History CLI で失敗分析が可能に。`--client` フラグでマルチテナントワークスペース隔離。Pipeline 刷新で zombie プロセスを根絶。Self-liveness watchdog が応答不能 daemon を自動再起動。

## 安全優先：DangerousOpsConfig

v2.2 の最も重要な変更は、新機能ではなくガードレールです。

**DangerousOpsConfig** は、パターンベースのコマンド遮断エンジンです。agent がシェルコマンドを実行する前に、Tetora が設定可能なブロックリストと照合します。マッチした場合、コマンドは実行前に拒否され、副作用もデータ損失も発生しません。

デフォルトでブロックされるパターン：
- `rm -rf`（および派生形）
- `DROP TABLE`、`DROP DATABASE`
- `git push --force`
- `find ~/`（`$HOME` 広範スキャン）

`config.json` でカスタム allowlist を設定できます：

```json
{
  "dangerousOps": {
    "enabled": true,
    "extraPatterns": ["truncate", "kubectl delete"],
    "allowlist": ["rm -rf ./dist"]
  }
}
```

agent `AddDirs` からの `$HOME` ブロック修正と組み合わせることで、指示された場合でも agent がホームディレクトリ全体に誤ってアクセスすることはなくなりました。prompt レベルだけでなく、多層防御を実現します。

## 信頼性：Pipeline 全面刷新

v2.2 は本番環境の安定性向上のために pipeline 実行層を大幅に書き直しました：

- **非同期 `scanReviews` + セマフォ** — 並列 review スキャンを最大 3 に制限し、大量 review 時の CPU スパイクを防止
- **Pipeline ヘルスチェックモニター** — バックグラウンドで 30 分ごとに実行し、`ResetStuckDoing` で `doing` 状態に固まったゾンビタスクを自動リセット
- **タイムアウト時にプロセスグループをキル** — ステップがタイムアウトした際、子プロセスも含めてすべて終了させ、孤児プロセスを根絶
- **エスカレート review の自動承認** — 4 時間以上滞留したエスカレート review を自動承認し、無限ブロックを防止

Workspace Git 層も強化されました：`index.lock` 再試行に指数バックオフを追加、`wsGitMu` によるシリアライゼーション、stale lock 閾値を 1 時間から 30 秒に短縮。

## Self-Liveness Watchdog

本番環境デプロイに自動クラッシュ回復機能が追加されました。新しい self-liveness watchdog が Tetora daemon のハートビートを監視し、プロセスが応答不能になった場合に supervisor 管理の再起動をトリガーします。

深夜 3 時にハングした daemon を手動で SSH して再起動する必要はもうありません。

## マルチテナント Dispatch：`--client` フラグ

マルチテナントサポートが正式対応。新しい `--client` フラグでクライアントごとに dispatch 出力を隔離：

```bash
tetora dispatch --client acme "週次レポート workflow を実行"
tetora dispatch --client initech "PR #42 コードレビュー"
```

各クライアントは独立した出力パスを持ち、異なるクライアントのタスク出力が混在しません。Team Builder CLI と組み合わせることで、単一の Tetora インスタンスからマルチクライアントの agent 設定を管理できます。

## Worktree 障害保護

これまで、タスクが途中で失敗した場合、worktree クリーンアップによって未コミットの変更が破棄されていました。v2.2 からは、コミットやローカル変更がある失敗・キャンセルされたタスクが破棄されずに `partial-done` として保持されます。

これが意味すること：
1. 進行中の作業がサイレントに失われることはない
2. agent がどのステップで失敗したかを正確に確認できる
3. 手動リカバリが簡単 — ブランチが完全な状態で残っている

## History CLI：失敗分析

3 つの新しい `tetora history` サブコマンドで agent 実行失敗を診断：

```bash
tetora history fails              # 最近失敗したタスクとエラーサマリーを一覧表示
tetora history streak             # 各 agent の連勝/連敗記録を表示
tetora history trace <task-id>    # 特定タスクの完全な実行トレース
```

agent が繰り返し失敗する場合、`history fails` と `trace` で生ログを掘らずとも根本原因を特定するデータが手に入ります。

## キャンセルボタン（v2.2.1）

v2.2.1 で提供される UX 改善：Dashboard から直接実行中のタスクをキャンセル可能になりました。

- **Task Detail モーダル** — タスクが `doing` 状態の時に黄色の「Cancel」ボタンを表示
- **Workflow 進捗パネル** — 「View Full Run」の隣に「Cancel Run」ボタンを追加

タスクが完了するか `doing` 以外の状態に変わると、ボタンは自動的に非表示になります。

## Provider Preset UI

Dashboard の Settings に Provider Preset UI が追加：

- **Custom `baseUrl`** 入力フィールド（セルフホストまたはプロキシエンドポイント対応）
- **Anthropic ネイティブ provider タイプ** — 正しいヘッダー形式の `x-api-key` 認証を使用
- **接続テストエンドポイント** — タスクをディスパッチする前に provider 設定を検証可能

## メモリの時間的減衰

agent のメモリエントリに時間ベースの関連度減衰が追加されました。数ヶ月前に学習した情報は徐々に優先度が下がり、長期稼働の Tetora デプロイで古い情報が新しいコンテキストを上書きすることを防ぎます。

減衰速度はプロジェクトごとに調整可能——コンテキストが急速に変化し、古い前提が自然に薄れていくべきチームに最適です。

## サイト：Astro マイグレーション

Tetora サイトがレガシー HTML から [Astro](https://astro.build/) へ移行し、パフォーマンスと開発体験が向上：

- **pnpm** による高速で再現性のあるインストール
- **WebP ロゴ** が 909KB の PNG を置き換え（3KB — 99.7% 削減）
- **GA4 遅延ロード** で Total Blocking Time を削減
- **動的サイドバー** でドキュメントナビゲーションを改善
- **i18n ドキュメント** — 6 つのコアドキュメントに対して 9 言語・54 ファイルの翻訳

## セキュリティ修正

v2.2 は内部監査で発見した 2 つのセキュリティ問題を修正：

- **SSRF 修正** — `/api/provider-test` エンドポイントを強化。ユーザー提供の URL はアウトバウンドリクエスト前に検証
- **XSS 修正** — Provider preset UI の入力フィールドをサニタイズし、ダッシュボードビューでのクロスサイトスクリプティングを防止

## v2.2.2 にアップグレード

```bash
tetora upgrade
```

シングルバイナリ。依存関係ゼロ。macOS / Linux / Windows 対応。

[GitHub で完全な Changelog を見る](https://github.com/TakumaLee/Tetora/releases/tag/v2.2.2)
