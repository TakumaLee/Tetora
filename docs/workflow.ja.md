# ワークフロー

## 概要

ワークフローは Tetora のマルチステップ・タスクオーケストレーションシステムです。JSON でステップのシーケンスを定義し、異なる agent を連携させ、複雑なタスクを自動化できます。

**ユースケース:**

- 複数の agent が順番または並列で処理するタスク
- 条件分岐とエラーリトライロジックを持つプロセス
- cron スケジュール、イベント、webhook によって起動される自動処理
- 実行履歴とコスト追跡が必要な正式なプロセス

## クイックスタート

### 1. ワークフロー JSON を記述する

`my-workflow.json` を作成します:

```json
{
  "name": "research-and-summarize",
  "description": "Gather information and write a summary",
  "variables": {
    "topic": "AI agents"
  },
  "timeout": "30m",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "Search and organize the latest developments in {{topic}}, listing 5 key points"
    },
    {
      "id": "summarize",
      "agent": "kohaku",
      "prompt": "Write a 300-word summary based on the following:\n{{steps.research.output}}",
      "dependsOn": ["research"]
    }
  ]
}
```

### 2. インポートとバリデーション

```bash
# JSON 構造をバリデーションする
tetora workflow validate my-workflow.json

# ~/.tetora/workflows/ にインポートする
tetora workflow create my-workflow.json
```

### 3. 実行する

```bash
# ワークフローを実行する
tetora workflow run research-and-summarize

# 変数を上書きする
tetora workflow run research-and-summarize --var topic="LLM safety"

# ドライラン（LLM 呼び出しなし、コスト見積もりのみ）
tetora workflow run research-and-summarize --dry-run
```

### 4. 結果を確認する

```bash
# 実行履歴を一覧表示する
tetora workflow runs research-and-summarize

# 特定の実行の詳細ステータスを確認する
tetora workflow status <run-id>
```

## ワークフロー JSON の構造

### トップレベルフィールド

| フィールド | 型 | 必須 | 説明 |
|-----------|------|:----:|------|
| `name` | string | はい | ワークフロー名。英数字、`-`、`_` のみ使用可（例: `my-workflow`） |
| `description` | string | | 説明 |
| `steps` | WorkflowStep[] | はい | 少なくとも 1 つのステップが必要 |
| `variables` | map[string]string | | デフォルト値を持つ入力変数（`""` は必須変数を意味する） |
| `timeout` | string | | 全体のタイムアウト（Go duration 形式、例: `"30m"`、`"1h"`） |
| `onSuccess` | string | | 成功時の通知テンプレート |
| `onFailure` | string | | 失敗時の通知テンプレート |

### WorkflowStep フィールド

| フィールド | 型 | 説明 |
|-----------|------|------|
| `id` | string | **必須** — 一意のステップ識別子 |
| `type` | string | ステップタイプ。デフォルトは `"dispatch"`。後述のタイプ参照 |
| `agent` | string | このステップを実行する agent のロール |
| `prompt` | string | agent への指示（`{{}}` テンプレートをサポート） |
| `skill` | string | スキル名（type=skill の場合） |
| `skillArgs` | string[] | スキルの引数（テンプレートをサポート） |
| `dependsOn` | string[] | 前提ステップの ID（DAG 依存関係） |
| `model` | string | LLM モデルの上書き |
| `provider` | string | プロバイダーの上書き |
| `timeout` | string | ステップごとのタイムアウト |
| `budget` | number | コスト上限（USD） |
| `permissionMode` | string | パーミッションモード |
| `if` | string | 条件式（type=condition） |
| `then` | string | 条件が true の場合にジャンプするステップ ID |
| `else` | string | 条件が false の場合にジャンプするステップ ID |
| `handoffFrom` | string | ソースステップ ID（type=handoff） |
| `parallel` | WorkflowStep[] | 並列実行するサブステップ（type=parallel） |
| `retryMax` | int | 最大リトライ回数（`onError: "retry"` が必要） |
| `retryDelay` | string | リトライ間隔（例: `"10s"`） |
| `onError` | string | エラー処理: `"stop"`（デフォルト）、`"skip"`、`"retry"` |
| `toolName` | string | ツール名（type=tool_call） |
| `toolInput` | map[string]string | ツールの入力パラメータ（`{{var}}` 展開をサポート） |
| `delay` | string | 待機時間（type=delay）、例: `"30s"`、`"5m"` |
| `notifyMsg` | string | 通知メッセージ（type=notify、テンプレートをサポート） |
| `notifyTo` | string | 通知チャネルのヒント（例: `"telegram"`） |

## ステップタイプ

### dispatch（デフォルト）

指定した agent にプロンプトを送って実行させます。最も一般的なステップタイプで、`type` を省略した場合に使用されます。

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**必須:** `prompt`
**省略可能:** `agent`、`model`、`provider`、`timeout`、`budget`、`permissionMode`

### skill

登録済みのスキルを実行します。

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**必須:** `skill`
**省略可能:** `skillArgs`

### condition

条件式を評価して分岐を決定します。true の場合は `then`、false の場合は `else` へ進みます。選択されなかった分岐はスキップ済みとしてマークされます。

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**必須:** `if`、`then`
**省略可能:** `else`

サポートされる演算子:
- `==` — 等しい（例: `{{type}} == 'technical'`）
- `!=` — 等しくない
- 真偽値チェック — 空でなく、`"false"` でも `"0"` でもない場合は true

### parallel

複数のサブステップを並行して実行し、すべての完了を待ちます。サブステップの出力は `\n---\n` で結合されます。

```json
{
  "id": "gather",
  "type": "parallel",
  "parallel": [
    {"id": "search-papers", "agent": "hisui", "prompt": "Search for papers"},
    {"id": "search-code", "agent": "kokuyou", "prompt": "Search open-source projects"}
  ]
}
```

**必須:** `parallel`（少なくとも 1 つのサブステップ）

個々のサブステップの結果は `{{steps.search-papers.output}}` で参照できます。

### handoff

あるステップの出力を別の agent に渡してさらに処理させます。ソースステップの完全な出力が受け取り側 agent のコンテキストになります。

```json
{
  "id": "review",
  "type": "handoff",
  "agent": "ruri",
  "handoffFrom": "draft",
  "prompt": "Review and revise the article",
  "dependsOn": ["draft"]
}
```

**必須:** `handoffFrom`、`agent`
**省略可能:** `prompt`（受け取り側 agent への指示）

### tool_call

ツールレジストリに登録されたツールを呼び出します。

```json
{
  "id": "fetch-data",
  "type": "tool_call",
  "toolName": "http-get",
  "toolInput": {
    "url": "https://api.example.com/data?q={{topic}}"
  }
}
```

**必須:** `toolName`
**省略可能:** `toolInput`（`{{var}}` 展開をサポート）

### delay

指定した時間だけ待機してから次のステップに進みます。

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**必須:** `delay`（Go duration 形式: `"30s"`、`"5m"`、`"1h"`）

### notify

通知メッセージを送信します。メッセージは SSE イベント（type=`workflow_notify`）として発行されるため、外部のコンシューマーが Telegram、Slack などをトリガーできます。

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**必須:** `notifyMsg`
**省略可能:** `notifyTo`（チャネルのヒント）

## 変数とテンプレート

ワークフローはステップ実行前に展開される `{{}}` テンプレート構文をサポートします。

### 入力変数

```
{{varName}}
```

`variables` のデフォルト値または `--var key=value` の上書き値から解決されます。

### ステップ結果

```
{{steps.ID.output}}    — ステップの出力テキスト
{{steps.ID.status}}    — ステップのステータス（success/error/skipped/timeout）
{{steps.ID.error}}     — ステップのエラーメッセージ
```

### 環境変数

```
{{env.KEY}}            — システムの環境変数
```

### 例

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## 依存関係とフロー制御

### dependsOn — DAG 依存関係

`dependsOn` を使用して実行順序を定義します。システムはステップを DAG（有向非巡回グラフ）として自動的に並び替えます。

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` は `step-a` と `step-b` の両方が完了するまで待機します
- `dependsOn` がないステップは即座に開始します（並列実行される場合があります）
- 循環依存はシステムが検出して拒否します

### 条件分岐

`condition` ステップの `then`/`else` によって、どの後続ステップを実行するかが決まります:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

選択されなかった分岐のステップは `skipped` とマークされます。後続ステップは `dependsOn` に基づいて通常どおり評価されます。

## エラー処理

### onError ストラテジー

各ステップに `onError` を設定できます:

| 値 | 動作 |
|----|------|
| `"stop"` | **デフォルト** — 失敗時にワークフローを中止する。残りのステップはスキップ済みとしてマークされる |
| `"skip"` | 失敗したステップをスキップ済みとしてマークして続行する |
| `"retry"` | `retryMax` + `retryDelay` に従ってリトライする。すべてのリトライが失敗した場合はエラーとして扱う |

### リトライ設定

```json
{
  "id": "flaky-step",
  "agent": "hisui",
  "prompt": "...",
  "onError": "retry",
  "retryMax": 3,
  "retryDelay": "10s"
}
```

- `retryMax`: 最大リトライ回数（初回の試行を除く）
- `retryDelay`: リトライ間の待機時間。デフォルトは 5 秒
- `onError: "retry"` の場合のみ有効

## トリガー

トリガーを使用するとワークフローを自動実行できます。`config.json` の `workflowTriggers` 配列で設定します。

### WorkflowTriggerConfig の構造

| フィールド | 型 | 説明 |
|-----------|------|------|
| `name` | string | トリガー名 |
| `workflowName` | string | 実行するワークフロー |
| `enabled` | bool | 有効かどうか（デフォルト: true） |
| `trigger` | TriggerSpec | トリガー条件 |
| `variables` | map[string]string | ワークフローの変数上書き |
| `cooldown` | string | クールダウン期間（例: `"5m"`、`"1h"`） |

### TriggerSpec の構造

| フィールド | 型 | 説明 |
|-----------|------|------|
| `type` | string | `"cron"`、`"event"`、`"webhook"` のいずれか |
| `cron` | string | cron 式（5 フィールド: 分 時 日 月 曜日） |
| `tz` | string | タイムゾーン（例: `"Asia/Taipei"`）。cron のみ |
| `event` | string | SSE イベントタイプ。`*` サフィックスのワイルドカードをサポート（例: `"deploy_*"`） |
| `webhook` | string | webhook パスのサフィックス |

### Cron トリガー

30 秒ごとにチェックされ、1 分あたり最大 1 回起動します（重複排除あり）。

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Event トリガー

SSE の `_triggers` チャネルをリッスンし、イベントタイプをマッチングします。`*` サフィックスのワイルドカードをサポートします。

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Event トリガーは自動的に追加変数を注入します: `event_type`、`task_id`、`session_id`、および `event_` プレフィックスを持つイベントデータフィールド。

### Webhook トリガー

HTTP POST によって起動されます:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

使用方法:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

POST ボディの JSON キーと値のペアは、追加のワークフロー変数として注入されます。

### クールダウン

すべてのトリガーは `cooldown` をサポートしており、短期間での繰り返し起動を防ぎます。クールダウン中のトリガーは黙って無視されます。

### トリガーメタ変数

システムはトリガー時に以下の変数を自動的に注入します:

- `_trigger_name` — トリガー名
- `_trigger_type` — トリガータイプ（cron/event/webhook）
- `_trigger_time` — トリガー時刻（RFC3339）

## 実行モード

### live（デフォルト）

LLM を呼び出し、履歴を記録し、SSE イベントを発行する完全実行モードです。

```bash
tetora workflow run my-workflow
```

### dry-run

LLM 呼び出しなし。各ステップのコストを見積もります。condition ステップは通常どおり評価されます。dispatch/skill/handoff ステップはコスト見積もりを返します。

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

LLM 呼び出しは通常どおり実行しますが、タスク履歴やセッションログには記録しません。テスト用途に適しています。

```bash
tetora workflow run my-workflow --shadow
```

## CLI リファレンス

```
tetora workflow <command> [options]
```

| コマンド | 説明 |
|---------|------|
| `list` | 保存済みのワークフローをすべて一覧表示する |
| `show <name>` | ワークフローの定義を表示する（サマリー + JSON） |
| `validate <name\|file>` | ワークフローをバリデーションする（名前または JSON ファイルパスを受け付ける） |
| `create <file>` | JSON ファイルからワークフローをインポートする（バリデーションを先に実行） |
| `delete <name>` | ワークフローを削除する |
| `run <name> [flags]` | ワークフローを実行する |
| `runs [name]` | 実行履歴を一覧表示する（名前でフィルタリング可） |
| `status <run-id>` | 実行の詳細ステータスを表示する（JSON 出力） |
| `messages <run-id>` | 実行の agent メッセージと handoff レコードを表示する |
| `history <name>` | ワークフローのバージョン履歴を表示する |
| `rollback <name> <version-id>` | 特定のバージョンに戻す |
| `diff <version1> <version2>` | 2 つのバージョンを比較する |

### run コマンドのフラグ

| フラグ | 説明 |
|------|------|
| `--var key=value` | ワークフロー変数を上書きする（複数回使用可） |
| `--dry-run` | ドライランモード（LLM 呼び出しなし） |
| `--shadow` | シャドウモード（履歴記録なし） |

### エイリアス

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## HTTP API リファレンス

### ワークフロー CRUD

| メソッド | パス | 説明 |
|---------|------|------|
| GET | `/workflows` | すべてのワークフローを一覧表示する |
| POST | `/workflows` | ワークフローを作成する（ボディ: Workflow JSON） |
| GET | `/workflows/{name}` | 単一のワークフロー定義を取得する |
| DELETE | `/workflows/{name}` | ワークフローを削除する |
| POST | `/workflows/{name}/validate` | ワークフローをバリデーションする |
| POST | `/workflows/{name}/run` | ワークフローを実行する（非同期、`202 Accepted` を返す） |
| GET | `/workflows/{name}/runs` | ワークフローの実行履歴を取得する |

#### POST /workflows/{name}/run のボディ

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### ワークフロー実行

| メソッド | パス | 説明 |
|---------|------|------|
| GET | `/workflow-runs` | すべての実行レコードを一覧表示する（`?workflow=name` でフィルタリング可） |
| GET | `/workflow-runs/{id}` | 実行の詳細を取得する（handoff + agent メッセージを含む） |

### トリガー

| メソッド | パス | 説明 |
|---------|------|------|
| GET | `/api/triggers` | すべてのトリガーのステータスを一覧表示する |
| POST | `/api/triggers/{name}/fire` | トリガーを手動で起動する |
| GET | `/api/triggers/{name}/runs` | トリガーの実行履歴を表示する（`?limit=N` 追加可） |
| POST | `/api/triggers/webhook/{id}` | webhook トリガー（ボディ: JSON キーと値の変数） |

## バージョン管理

`create` または変更のたびに、バージョンスナップショットが自動的に作成されます。

```bash
# バージョン履歴を確認する
tetora workflow history my-workflow

# 特定のバージョンに戻す
tetora workflow rollback my-workflow <version-id>

# 2 つのバージョンを比較する
tetora workflow diff <version-id-1> <version-id-2>
```

## バリデーションルール

システムは `create` と `run` の両方の前にバリデーションを行います:

- `name` は必須。英数字、`-`、`_` のみ使用可
- 少なくとも 1 つのステップが必要
- ステップ ID は一意でなければならない
- `dependsOn` の参照先は既存のステップ ID でなければならない
- ステップは自身に依存できない
- 循環依存は拒否される（DAG サイクル検出）
- ステップタイプごとの必須フィールド（例: dispatch には `prompt`、condition には `if` + `then` が必要）
- `timeout`、`retryDelay`、`delay` は有効な Go duration 形式でなければならない
- `onError` は `stop`、`skip`、`retry` のみ受け付ける
- condition の `then`/`else` は既存のステップ ID を参照しなければならない
- handoff の `handoffFrom` は既存のステップ ID を参照しなければならない
