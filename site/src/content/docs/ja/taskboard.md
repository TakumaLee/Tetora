---
title: "タスクボードと自動ディスパッチ ガイド"
lang: "ja"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# タスクボードと自動ディスパッチ ガイド

## 概要

タスクボードは Tetora に組み込まれたカンバンシステムで、タスクの追跡と自動実行を行います。SQLite をバックエンドとする永続的なタスクストアと、準備完了タスクを監視して手動介入なしに agent に引き渡す自動ディスパッチエンジンを組み合わせています。

主なユースケース:

- エンジニアリングタスクのバックログをキューに入れ、overnight で agent に処理させる
- 専門性に基づいて特定の agent にタスクをルーティングする（例: バックエンドは `kokuyou`、コンテンツは `kohaku`）
- 依存関係のあるタスクをチェーンして、ある agent が終わったところから別の agent が引き継ぐ
- git と連携したタスク実行: ブランチの自動作成、コミット、プッシュ、PR/MR 作成

**要件:** `config.json` で `taskBoard.enabled: true` を設定し、Tetora デーモンを起動していること。

---

## タスクのライフサイクル

タスクは以下の順序でステータスが変化します:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| ステータス | 意味 |
|---|---|
| `idea` | 大まかな概念。まだ整理されていない |
| `needs-thought` | 実装前に分析や設計が必要 |
| `backlog` | 定義・優先順位付け済みだが、まだスケジュールされていない |
| `todo` | 実行準備完了 — 担当者が設定されていれば自動ディスパッチが拾い上げる |
| `doing` | 現在実行中 |
| `review` | 実行完了、品質レビュー待ち |
| `done` | 完了・レビュー済み |
| `partial-done` | 実行は成功したが後処理に失敗した場合（例: git マージの競合）。回復可能。 |
| `failed` | 実行失敗または空の出力。`maxRetries` まで自動リトライされます。 |

自動ディスパッチは `status=todo` のタスクを拾い上げます。担当者のないタスクは自動的に `defaultAgent`（デフォルト: `ruri`）に割り当てられます。`backlog` のタスクは設定された `backlogAgent`（デフォルト: `ruri`）によって定期的にトリアージされ、有望なものは `todo` に昇格します。

---

## タスクの作成

### CLI

```bash
# 最小限のタスク（バックログに入り、未割り当て）
tetora task create --title="Add rate limiting to API"

# すべてのオプションを指定
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# タスクの一覧表示
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# 特定のタスクの表示
tetora task show task-abc123
tetora task show task-abc123 --full   # コメント/スレッドを含む

# タスクの手動移動
tetora task move task-abc123 --status=todo

# agent への割り当て
tetora task assign task-abc123 --assignee=kokuyou

# コメントの追加（spec、context、log、system タイプ）
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

タスク ID は `task-<uuid>` 形式で自動生成されます。フル ID またはショートプレフィックスでタスクを参照できます — CLI が一致するものを候補として表示します。

### HTTP API

```bash
# 作成
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# 一覧表示（ステータスでフィルタリング）
curl "http://localhost:8991/api/tasks?status=todo"

# 新しいステータスへ移動
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### ダッシュボード

ダッシュボード（`http://localhost:8991/dashboard`）の **Taskboard** タブを開きます。タスクはカンバンカラムに表示されます。カードをカラム間にドラッグしてステータスを変更し、カードをクリックするとコメントと diff ビューを含む詳細パネルが開きます。

---

## 自動ディスパッチ

自動ディスパッチは `todo` タスクを拾い上げて agent に実行させるバックグラウンドループです。

### 動作の仕組み

1. `interval`（デフォルト: `5m`）ごとにティッカーが発火します。
2. スキャナーが現在実行中のタスク数を確認します。`activeCount >= maxConcurrentTasks` の場合、スキャンはスキップされます。
3. 担当者のある各 `todo` タスクに対して、タスクがその agent にディスパッチされます。未割り当てタスクは `defaultAgent` に自動割り当てされます。
4. タスクが完了すると、フル interval を待たずに次のバッチが開始されるよう即時再スキャンが発火します。
5. デーモン起動時に、前回のクラッシュで孤立した `doing` タスクが確認され、完了の証拠があれば `done` に、本当に孤立していれば `todo` にリセットされます。

### ディスパッチフロー

```
                          ┌─────────┐
                          │  idea   │  (manual concept entry)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (requires analysis)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Triage (backlogAgent, default: ruri) runs periodically:  │
  │   • "ready"     → assign agent → move to todo             │
  │   • "decompose" → create subtasks → parent to doing       │
  │   • "clarify"   → add question comment → stay in backlog  │
  │                                                           │
  │  Fast-path: already has assignee + no blocking deps       │
  │   → skip LLM triage, promote directly to todo             │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Auto-dispatch picks up tasks every scan cycle:           │
  │   • Has assignee       → dispatch to that agent           │
  │   • No assignee        → assign defaultAgent, then run    │
  │   • Has workflow       → run through workflow pipeline     │
  │   • Has dependsOn      → wait until deps are done         │
  │   • Resumable prev run → resume from checkpoint           │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Agent executes task (single prompt or workflow DAG)       │
  │                                                           │
  │  Guard: stuckThreshold (default 2h)                       │
  │   • If workflow still running → refresh timestamp          │
  │   • If truly stuck            → reset to todo              │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     success    partial failure  failure
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     Resume button     │  Retry (up to maxRetries)
           │     in dashboard      │  or escalate
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ escalate │
       └────────┘            │ to human │
                             └──────────┘
```

### トリアージの詳細

トリアージは `backlogTriageInterval`（デフォルト: `1h`）ごとに実行され、`backlogAgent`（デフォルト: `ruri`）が担当します。agent は各バックログタスクをコメントと利用可能な agent ロスターと共に受け取り、以下を決定します:

| アクション | 効果 |
|---|---|
| `ready` | 特定の agent を割り当て、`todo` に昇格 |
| `decompose` | サブタスクを作成（担当者付き）、親タスクは `doing` に移動 |
| `clarify` | 質問をコメントとして追加、タスクは `backlog` のまま |

**ファストパス**: 既に担当者がいてブロッキング依存関係がないタスクは LLM トリアージを完全にスキップし、直接 `todo` に昇格します。

### 自動割り当て

`todo` タスクに担当者がない場合、ディスパッチャーは自動的に `defaultAgent`（設定可能、デフォルト: `ruri`）に割り当てます。これによりタスクが静かに詰まるのを防ぎます。典型的なフロー:

1. 担当者なしでタスクが作成される → `backlog` に入る
2. トリアージが `todo` に昇格（agent の割り当てあり/なし）
3. トリアージで割り当てがなければ → ディスパッチャーが `defaultAgent` を割り当て
4. タスクが通常通り実行される

### 設定

`config.json` に追加:

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| フィールド | デフォルト | 説明 |
|---|---|---|
| `enabled` | `false` | 自動ディスパッチループを有効にする |
| `interval` | `5m` | 準備完了タスクをスキャンする頻度 |
| `maxConcurrentTasks` | `3` | 同時実行する最大タスク数 |
| `defaultAgent` | `ruri` | ディスパッチ前に未割り当ての `todo` タスクに自動割り当てされる agent |
| `backlogAgent` | `ruri` | バックログタスクのレビューと昇格を担当する agent |
| `reviewAgent` | `ruri` | 完了タスクの出力をレビューする agent |
| `escalateAssignee` | `takuma` | 自動レビューが人間の判断を求めた場合に割り当てられるユーザー |
| `stuckThreshold` | `2h` | `doing` にとどまれる最大時間、超過するとリセット |
| `backlogTriageInterval` | `1h` | バックログトリアージ実行の最小間隔 |
| `reviewLoop` | `false` | Dev↔QA ループを有効にする（実行 → レビュー → 修正、最大 `maxRetries` 回） |
| `maxBudget` | 制限なし | タスクごとの最大コスト（USD） |
| `defaultModel` | — | 自動ディスパッチされるすべてのタスクのモデルを上書き |

---

## Slot Pressure

Slot pressure は、自動ディスパッチがすべての同時実行スロットを消費してインタラクティブセッション（人間のチャットメッセージ、オンデマンドディスパッチ）を枯渇させないようにします。

`config.json` で有効にします:

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| フィールド | デフォルト | 説明 |
|---|---|---|
| `reservedSlots` | `2` | インタラクティブ用に確保されるスロット数。空きスロットがこのレベルに落ちると非インタラクティブタスクは待機する。 |
| `warnThreshold` | `3` | 空きスロットがこのレベルに落ちたときに警告が発火。「排程接近滿載」メッセージがダッシュボードと通知チャネルに表示される。 |
| `nonInteractiveTimeout` | `5m` | 非インタラクティブタスクがスロットを待機してキャンセルされるまでの時間。 |

インタラクティブなソース（人間のチャット、`tetora dispatch`、`tetora route`）は常に即座にスロットを確保します。バックグラウンドのソース（タスクボード、cron）は pressure が高い場合に待機します。

---

## Git インテグレーション

`gitCommit`、`gitPush`、`gitPR` が有効な場合、タスクが正常に完了した後にディスパッチャーが git 操作を実行します。

**ブランチ命名**は `gitWorkflow.branchConvention` で制御されます:

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

デフォルトテンプレート `{type}/{agent}-{description}` は `feat/kokuyou-add-rate-limiting` のようなブランチを生成します。`{description}` 部分はタスクタイトルから導出されます（小文字化、スペースをハイフンに置換、40 文字に切り詰め）。

タスクの `type` フィールドがブランチプレフィックスを設定します。タスクにタイプがない場合は `defaultType` が使用されます。

**自動 PR/MR** は GitHub（`gh`）と GitLab（`glab`）の両方に対応しています。`PATH` で利用可能なバイナリが自動的に使用されます。

---

## Worktree モード

`gitWorktree: true` の場合、各タスクは共有作業ディレクトリの代わりに独立した git worktree で実行されます。これにより、同じリポジトリで複数のタスクが同時実行される際のファイル競合が解消されます。

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← このタスク専用の分離コピー
  task-def456/   ← このタスク専用の分離コピー
```

タスク完了時:

- `autoMerge: true`（デフォルト）の場合、worktree ブランチは `main` にマージされ、worktree は削除されます。
- マージに失敗した場合、タスクは `partial-done` ステータスに移動します。手動解決のために worktree は保持されます。

`partial-done` から回復するには:

```bash
# 何が起きたか確認
tetora task show task-abc123 --full

# ブランチを手動でマージ
git merge feat/kokuyou-task-abc123

# エディタで競合を解決してコミット
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# タスクを完了とマーク
tetora task move task-abc123 --status=done
```

---

## ワークフロー インテグレーション

タスクは単一の agent プロンプトの代わりにワークフローパイプラインを通じて実行できます。タスクが複数の協調ステップを必要とする場合（例: リサーチ → 実装 → テスト → ドキュメント）に便利です。

タスクにワークフローを割り当てる:

```bash
# タスク作成時に設定
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# または既存のタスクを更新
tetora task update task-abc123 --workflow=engineering-pipeline
```

特定のタスクでボードレベルのデフォルトワークフローを無効にするには:

```json
{ "workflow": "none" }
```

ボードレベルのデフォルトワークフローは、上書きされない限りすべての自動ディスパッチタスクに適用されます:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

タスクの `workflowRunId` フィールドが特定のワークフロー実行にリンクされ、ダッシュボードの Workflows タブで確認できます。

---

## ダッシュボードビュー

`http://localhost:8991/dashboard` でダッシュボードを開き、**Taskboard** タブに移動します。

**カンバンボード** — 各ステータス用のカラム。カードにはタイトル、担当者、優先度バッジ、コストが表示されます。ドラッグでステータスを変更できます。

**タスク詳細パネル** — カードをクリックすると開きます。表示内容:
- 全説明とすべてのコメント（spec、context、log エントリ）
- セッションリンク（まだ実行中であればライブワーカーターミナルにジャンプ）
- コスト、実行時間、リトライ回数
- 該当する場合のワークフロー実行リンク

**Diff レビューパネル** — `requireReview: true` の場合、完了したタスクがレビューキューに表示されます。レビュアーは変更の diff を確認して承認または変更要求を出せます。

---

## ヒント

**タスクのサイズ。** タスクは 30〜90 分のスコープに抑えてください。大きすぎるタスク（数日かかるリファクタリング）はタイムアウトや空の出力が出やすく、failed とマークされる傾向があります。`parentId` フィールドを使ってサブタスクに分解してください。

**同時ディスパッチの制限。** `maxConcurrentTasks: 3` は安全なデフォルトです。プロバイダーが許可する API 接続数を超えると競合とタイムアウトが発生します。3 から始め、安定した動作を確認してから 5 に上げてください。

**partial-done からの回復。** タスクが `partial-done` になった場合、agent の作業は正常に完了しています — 失敗したのは git マージのステップのみです。競合を手動で解決してからタスクを `done` に移動してください。コストとセッションデータは保持されます。

**`dependsOn` の活用。** 未完了の依存関係があるタスクは、リストされたすべてのタスク ID が `done` になるまでディスパッチャーにスキップされます。上流タスクの結果は「Previous Task Results」として依存タスクのプロンプトに自動注入されます。

**バックログトリアージ。** `backlogAgent` は各 `backlog` タスクを読み取り、実現可能性と優先度を評価し、明確なタスクを `todo` に昇格させます。`backlog` タスクには詳細な説明と受け入れ基準を書いてください — トリアージ agent がそれを使って昇格させるか人間のレビューに残すかを判断します。

**リトライとレビューループ。** `reviewLoop: false`（デフォルト）の場合、失敗したタスクは以前のログコメントを注入して最大 `maxRetries` 回リトライされます。`reviewLoop: true` の場合、各実行は `reviewAgent` によってレビューされてから完了とみなされます — 問題が見つかった場合は agent がフィードバックを受け取り再試行します。
