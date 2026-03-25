---
title: "設定リファレンス"
lang: "ja"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# 設定リファレンス

## 概要

Tetora の設定は `~/.tetora/config.json` という単一の JSON ファイルで管理されます。

**主な動作:**

- **`$ENV_VAR` 置換** — `$` で始まる文字列値は起動時に対応する環境変数に置き換えられます。シークレット（API キー、トークン）はハードコードせずこの方式を使用してください。
- **ホットリロード** — デーモンに `SIGHUP` を送信すると設定が再読み込みされます。不正な設定は拒否され、実行中の設定が維持されます。デーモンはクラッシュしません。
- **相対パス** — `jobsFile`、`historyDB`、`defaultWorkdir`、各ディレクトリフィールドは設定ファイルのディレクトリ（`~/.tetora/`）からの相対パスで解決されます。
- **後方互換性** — 旧来の `"roles"` キーは `"agents"` の別名です。`smartDispatch` 内の旧来の `"defaultRole"` キーは `"defaultAgent"` の別名です。

---

## トップレベルフィールド

### コア設定

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | API とダッシュボードの HTTP リッスンアドレス。形式: `host:port`。 |
| `apiToken` | string | `""` | すべての API リクエストに必要な Bearer トークン。空の場合は認証なし（本番環境では非推奨）。`$ENV_VAR` 対応。 |
| `maxConcurrent` | int | `8` | 同時実行できる agent タスクの最大数。20 を超えると起動時に警告が出ます。 |
| `defaultModel` | string | `"sonnet"` | デフォルトの Claude モデル名。agent ごとに上書きしない限りプロバイダーに渡されます。 |
| `defaultTimeout` | string | `"1h"` | デフォルトのタスクタイムアウト。Go duration 形式: `"15m"`、`"1h"`、`"30s"`。 |
| `defaultBudget` | float64 | `0` | タスクごとのデフォルトコスト上限（USD）。`0` は制限なし。 |
| `defaultPermissionMode` | string | `"acceptEdits"` | デフォルトの Claude パーミッションモード。一般的な値: `"acceptEdits"`、`"default"`。 |
| `defaultAgent` | string | `""` | ルーティングルールにマッチしない場合のシステム全体のフォールバック agent 名。 |
| `defaultWorkdir` | string | `""` | agent タスクのデフォルト作業ディレクトリ。ディスク上に存在する必要があります。 |
| `claudePath` | string | `"claude"` | `claude` CLI バイナリへのパス。デフォルトでは `$PATH` から `claude` を検索します。 |
| `defaultProvider` | string | `"claude"` | agent レベルの上書きがない場合に使用するプロバイダー名。 |
| `log` | bool | `false` | ファイルロギングを有効にするレガシーフラグ。代わりに `logging.level` を使用してください。 |
| `maxPromptLen` | int | `102400` | プロンプトの最大長（バイト単位、100 KB）。これを超えるリクエストは拒否されます。 |
| `configVersion` | int | `0` | 設定スキーマのバージョン。自動マイグレーションに使用されます。手動では設定しないでください。 |
| `encryptionKey` | string | `""` | 機密データのフィールドレベル暗号化用の AES キー。`$ENV_VAR` 対応。 |
| `streamToChannels` | bool | `false` | 接続されたメッセージングチャネル（Discord、Telegram など）にライブのタスクステータスをストリーミングします。 |
| `cronNotify` | bool\|null | `null` (true) | `false` にするとすべての cron ジョブ完了通知を抑制します。`null` または `true` で有効。 |
| `cronReplayHours` | int | `2` | デーモン起動時に見逃した cron ジョブを何時間前まで遡って確認するか。 |
| `diskBudgetGB` | float64 | `1.0` | 最小空きディスク容量（GB）。この値を下回ると cron ジョブが拒否されます。 |
| `diskWarnMB` | int | `500` | 空きディスクの警告閾値（MB）。WARN ログが出力されますがジョブは継続します。 |
| `diskBlockMB` | int | `200` | 空きディスクのブロック閾値（MB）。ジョブは `skipped_disk_full` ステータスでスキップされます。 |

### ディレクトリの上書き

デフォルトではすべてのディレクトリは `~/.tetora/` 以下に配置されます。標準外のレイアウトが必要な場合のみ上書きしてください。

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | ワークスペースのナレッジファイル用ディレクトリ。 |
| `agentsDir` | string | `~/.tetora/agents/` | agent ごとの SOUL.md ファイルを格納するディレクトリ。 |
| `workspaceDir` | string | `~/.tetora/workspace/` | ルール、メモリ、スキル、ドラフトなどのディレクトリ。 |
| `runtimeDir` | string | `~/.tetora/runtime/` | セッション、出力、ログ、キャッシュのディレクトリ。 |
| `vaultDir` | string | `~/.tetora/vault/` | 暗号化されたシークレット vault のディレクトリ。 |
| `historyDB` | string | `history.db` | ジョブ履歴の SQLite データベースパス。設定ディレクトリからの相対パス。 |
| `jobsFile` | string | `jobs.json` | cron ジョブ定義ファイルへのパス。設定ディレクトリからの相対パス。 |

### グローバル許可ディレクトリ

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| フィールド | 型 | 説明 |
|---|---|---|
| `allowedDirs` | string[] | agent が読み書きできるディレクトリ。グローバルに適用され、agent ごとに絞り込めます。 |
| `defaultAddDirs` | string[] | すべてのタスクで `--add-dir` として注入されるディレクトリ（読み取り専用コンテキスト）。 |
| `allowedIPs` | string[] | API の呼び出しを許可する IP アドレスまたは CIDR レンジ。空の場合はすべて許可。例: `["192.168.1.0/24", "10.0.0.1"]`。 |

---

## プロバイダー

プロバイダーは Tetora が agent タスクを実行する方法を定義します。複数のプロバイダーを設定して agent ごとに選択できます。

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `type` | string | 必須 | プロバイダータイプ。`"claude-cli"`、`"openai-compatible"`、`"claude-api"`、`"claude-code"` のいずれか。 |
| `path` | string | `""` | バイナリパス。`claude-cli` および `claude-code` タイプで使用されます。空の場合は `claudePath` にフォールバック。 |
| `baseUrl` | string | `""` | API ベース URL。`openai-compatible` では必須。 |
| `apiKey` | string | `""` | API キー。`$ENV_VAR` 対応。`claude-api` では必須、`openai-compatible` ではオプション。 |
| `model` | string | `""` | このプロバイダーのデフォルトモデル。このプロバイダーを使用するタスクの `defaultModel` を上書きします。 |
| `maxTokens` | int | `8192` | 最大出力トークン数（`claude-api` で使用）。 |
| `firstTokenTimeout` | string | `"60s"` | タイムアウトするまでの最初のレスポンストークンの待機時間（SSE ストリーム）。 |

**プロバイダータイプ:**
- `claude-cli` — `claude` バイナリをサブプロセスとして実行（デフォルト、最も互換性が高い）
- `claude-api` — HTTP 経由で Anthropic API を直接呼び出す（`ANTHROPIC_API_KEY` が必要）
- `openai-compatible` — OpenAI 互換の REST API（OpenAI、Ollama、Groq など）
- `claude-code` — Claude Code CLI モードを使用

---

## Agents

Agent は固有のモデル、soul ファイル、ツールアクセスを持つ名前付きペルソナを定義します。

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `soulFile` | string | 必須 | agent のパーソナリティファイル（SOUL.md）へのパス。`agentsDir` からの相対パス。 |
| `model` | string | `defaultModel` | この agent に使用するモデル。 |
| `description` | string | `""` | 人が読める説明。LLM クラシファイアによるルーティングにも使用されます。 |
| `keywords` | string[] | `[]` | スマートディスパッチでこの agent へのルーティングをトリガーするキーワード。 |
| `provider` | string | `defaultProvider` | プロバイダー名（`providers` マップのキー）。 |
| `permissionMode` | string | `defaultPermissionMode` | この agent の Claude パーミッションモード。 |
| `allowedDirs` | string[] | `allowedDirs` | この agent がアクセスできるファイルシステムパス。グローバル設定を上書きします。 |
| `docker` | bool\|null | `null` | agent ごとの Docker サンドボックス上書き。`null` はグローバルの `docker.enabled` を継承。 |
| `fallbackProviders` | string[] | `[]` | プライマリが失敗した場合のフォールバックプロバイダー名の順序付きリスト。 |
| `trustLevel` | string | `"auto"` | トラストレベル: `"observe"`（読み取り専用）、`"suggest"`（提案のみ、適用しない）、`"auto"`（完全な自律性）。 |
| `tools` | AgentToolPolicy | `{}` | ツールアクセスポリシー。[Tool Policy](#tool-policy) 参照。 |
| `toolProfile` | string | `"standard"` | 名前付きツールプロファイル: `"minimal"`、`"standard"`、`"full"`。 |
| `workspace` | WorkspaceConfig | `{}` | ワークスペース分離設定。 |

---

## スマートディスパッチ

スマートディスパッチは、ルール、キーワード、LLM 分類に基づいて、受信タスクを最適な agent に自動ルーティングします。

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | スマートディスパッチルーティングを有効にする。 |
| `coordinator` | string | 最初の agent | LLM ベースのタスク分類に使用される agent。 |
| `defaultAgent` | string | 最初の agent | ルールにマッチしない場合のフォールバック agent。 |
| `classifyBudget` | float64 | `0.1` | 分類 LLM 呼び出しのコスト上限（USD）。 |
| `classifyTimeout` | string | `"30s"` | 分類呼び出しのタイムアウト。 |
| `review` | bool | `false` | タスク完了後に出力に対してレビュー agent を実行する。 |
| `reviewLoop` | bool | `false` | Dev↔QA リトライループを有効にする: レビュー → フィードバック → リトライ（最大 `maxRetries` 回）。 |
| `maxRetries` | int | `3` | レビューループでの最大 QA リトライ回数。 |
| `reviewAgent` | string | coordinator | 出力のレビューを担当する agent。対抗レビュー用に厳格な QA agent を設定できます。 |
| `reviewBudget` | float64 | `0.2` | レビュー LLM 呼び出しのコスト上限（USD）。 |
| `fallback` | string | `"smart"` | フォールバック戦略: `"smart"`（LLM ルーティング）または `"coordinator"`（常にデフォルト agent）。 |
| `rules` | RoutingRule[] | `[]` | LLM 分類の前に評価されるキーワード/正規表現ルーティングルール。 |
| `bindings` | RoutingBinding[] | `[]` | チャネル/ユーザー/ギルド → agent バインディング（最高優先度、最初に評価）。 |

### `rules` — `RoutingRule`

| フィールド | 型 | 説明 |
|---|---|---|
| `agent` | string | ターゲット agent 名。 |
| `keywords` | string[] | 大文字小文字を区別しないキーワード。いずれかにマッチするとこの agent にルーティング。 |
| `patterns` | string[] | Go の正規表現パターン。いずれかにマッチするとこの agent にルーティング。 |

### `bindings` — `RoutingBinding`

| フィールド | 型 | 説明 |
|---|---|---|
| `channel` | string | プラットフォーム: `"telegram"`、`"discord"`、`"slack"` など。 |
| `userId` | string | そのプラットフォームでのユーザー ID。 |
| `channelId` | string | チャネルまたはチャット ID。 |
| `guildId` | string | ギルド/サーバー ID（Discord のみ）。 |
| `agent` | string | ターゲット agent 名。 |

---

## セッション

複数回のインタラクションにわたって会話コンテキストがどのように維持・圧縮されるかを制御します。

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `contextMessages` | int | `20` | 新しいタスクのコンテキストとして注入する最近のメッセージの最大数。 |
| `compactAfter` | int | `30` | メッセージ数がこれを超えると圧縮する。非推奨: `compaction.maxMessages` を使用してください。 |
| `compactKeep` | int | `10` | 圧縮後に保持する最新メッセージ数。非推奨: `compaction.compactTo` を使用してください。 |
| `compactTokens` | int | `200000` | 合計入力トークン数がこの閾値を超えると圧縮する。 |

### `session.compaction` — `CompactionConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | 自動セッション圧縮を有効にする。 |
| `maxMessages` | int | `50` | セッションのメッセージ数がこれを超えると圧縮をトリガー。 |
| `compactTo` | int | `10` | 圧縮後に保持する最新メッセージ数。 |
| `model` | string | `"haiku"` | 圧縮サマリーの生成に使用する LLM モデル。 |
| `maxCost` | float64 | `0.02` | 圧縮呼び出し 1 回あたりの最大コスト（USD）。 |
| `provider` | string | `defaultProvider` | 圧縮サマリー呼び出しに使用するプロバイダー。 |

---

## タスクボード

組み込みのタスクボードは作業アイテムを追跡し、agent に自動でディスパッチできます。

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | タスクボードを有効にする。 |
| `maxRetries` | int | `3` | 失敗とマークするまでのタスクの最大リトライ回数。 |
| `requireReview` | bool | `false` | 品質ゲート: タスクが完了とマークされる前にレビューをパスする必要がある。 |
| `defaultWorkflow` | string | `""` | 自動ディスパッチされるすべてのタスクに実行するワークフロー名。空の場合はワークフローなし。 |
| `gitCommit` | bool | `false` | タスクが完了とマークされたときに自動コミット。 |
| `gitPush` | bool | `false` | コミット後に自動プッシュ（`gitCommit: true` が必要）。 |
| `gitPR` | bool | `false` | プッシュ後に GitHub PR を自動作成（`gitPush: true` が必要）。 |
| `gitWorktree` | bool | `false` | タスク分離に git worktree を使用（同時タスク間のファイル競合を解消）。 |
| `idleAnalyze` | bool | `false` | ボードがアイドル状態のときに分析を自動実行。 |
| `problemScan` | bool | `false` | 完了後にタスク出力の潜在的な問題をスキャン。 |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | キューに入ったタスクの自動ポーリングとディスパッチを有効にする。 |
| `interval` | string | `"5m"` | 準備完了タスクをスキャンする頻度。 |
| `maxConcurrentTasks` | int | `3` | 1 回のスキャンサイクルでディスパッチするタスクの最大数。 |
| `defaultModel` | string | `""` | 自動ディスパッチされるタスクのモデルを上書き。 |
| `maxBudget` | float64 | `0` | タスクごとの最大コスト（USD）。`0` = 制限なし。 |
| `defaultAgent` | string | `""` | 割り当てなしタスクのフォールバック agent。 |
| `backlogAgent` | string | `""` | バックログトリアージ担当の agent。 |
| `reviewAgent` | string | `""` | 完了タスクのレビュー担当 agent。 |
| `escalateAssignee` | string | `""` | レビュー却下タスクをこのユーザーに割り当て。 |
| `stuckThreshold` | string | `"2h"` | `doing` にこれ以上とどまっているタスクは `todo` にリセット。 |
| `backlogTriageInterval` | string | `"1h"` | バックログトリアージの実行間隔。 |
| `reviewLoop` | bool | `false` | ディスパッチされたタスクの自動 Dev↔QA ループを有効にする。 |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | ブランチ命名テンプレート。変数: `{type}`、`{agent}`、`{description}`。 |
| `types` | string[] | `["feat","fix","refactor","chore"]` | 許可されるブランチタイプのプレフィックス。 |
| `defaultType` | string | `"feat"` | タイプが指定されない場合のフォールバックタイプ。 |
| `autoMerge` | bool | `false` | タスク完了時に自動的に main にマージ（`gitWorktree: true` の場合のみ）。 |

---

## Slot Pressure

`maxConcurrent` スロット制限に近づいたときのシステム動作を制御します。インタラクティブ（人間が開始した）セッションには予約スロットが確保され、バックグラウンドタスクは待機します。

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | slot pressure 管理を有効にする。 |
| `reservedSlots` | int | `2` | インタラクティブセッション用に予約されるスロット数。バックグラウンドタスクはこれを使用できません。 |
| `warnThreshold` | int | `3` | 空きスロット数がこれを下回ったときにユーザーに警告。 |
| `nonInteractiveTimeout` | string | `"5m"` | バックグラウンドタスクがスロットを待機してタイムアウトするまでの時間。 |
| `pollInterval` | string | `"2s"` | バックグラウンドタスクが空きスロットをチェックする頻度。 |
| `monitorEnabled` | bool | `false` | 通知チャネル経由の積極的な slot pressure アラートを有効にする。 |
| `monitorInterval` | string | `"30s"` | pressure アラートを確認・送信する頻度。 |

---

## ワークフロー

ワークフローはディレクトリ内の YAML ファイルとして定義されます。`workflowDir` はそのディレクトリを指します。変数はデフォルトのテンプレート値を提供します。

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | ワークフロー YAML ファイルを格納するディレクトリ。 |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | システムイベントによる自動ワークフロートリガー。 |

---

## インテグレーション

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | Telegram ボットを有効にする。 |
| `botToken` | string | `""` | @BotFather から取得した Telegram ボットトークン。`$ENV_VAR` 対応。 |
| `chatID` | int64 | `0` | 通知を送信する Telegram チャットまたはグループ ID。 |
| `pollTimeout` | int | `30` | メッセージ受信のロングポールタイムアウト（秒）。 |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | Discord ボットを有効にする。 |
| `botToken` | string | `""` | Discord ボットトークン。`$ENV_VAR` 対応。 |
| `guildID` | string | `""` | 特定の Discord サーバー（ギルド）に制限する。 |
| `channelIDs` | string[] | `[]` | ボットがすべてのメッセージに返信するチャネル ID（`@` メンション不要）。 |
| `mentionChannelIDs` | string[] | `[]` | ボットが `@` メンションされた場合のみ返信するチャネル ID。 |
| `notifyChannelID` | string | `""` | タスク完了通知用チャネル（タスクごとにスレッドを作成）。 |
| `showProgress` | bool | `true` | Discord でライブの「作業中...」ストリーミングメッセージを表示する。 |
| `webhooks` | map[string]string | `{}` | アウトバウンド専用通知用の名前付き webhook URL。 |
| `routes` | map[string]DiscordRouteConfig | `{}` | チャネルごとのルーティング用、チャネル ID から agent 名へのマッピング。 |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | Slack ボットを有効にする。 |
| `botToken` | string | `""` | Slack ボット OAuth トークン（`xoxb-...`）。`$ENV_VAR` 対応。 |
| `signingSecret` | string | `""` | リクエスト検証用の Slack 署名シークレット。`$ENV_VAR` 対応。 |
| `appToken` | string | `""` | Socket Mode 用の Slack アプリレベルトークン（`xapp-...`）。任意。`$ENV_VAR` 対応。 |
| `defaultChannel` | string | `""` | アウトバウンド通知のデフォルトチャネル ID。 |

### アウトバウンド Webhook

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `url` | string | 必須 | Webhook エンドポイント URL。 |
| `headers` | map[string]string | `{}` | 含める HTTP ヘッダー。値は `$ENV_VAR` 対応。 |
| `events` | string[] | すべて | 送信するイベント: `"success"`、`"error"`、`"timeout"`、`"all"`。空の場合はすべて。 |

### インカミング Webhook

インカミング webhook は外部サービスが HTTP POST 経由で Tetora タスクをトリガーできるようにします。

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### 通知チャネル

異なる Slack/Discord エンドポイントへのタスクイベントルーティング用の名前付き通知チャネル。

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `name` | string | `""` | ジョブの `channel` フィールドで使用する名前付き参照（例: `"discord:alerts"`）。 |
| `type` | string | 必須 | `"slack"` または `"discord"`。 |
| `webhookUrl` | string | 必須 | Webhook URL。`$ENV_VAR` 対応。 |
| `events` | string[] | すべて | イベントタイプでフィルタリング: `"all"`、`"error"`、`"success"`。 |
| `minPriority` | string | すべて | 最低優先度: `"critical"`、`"high"`、`"normal"`、`"low"`。 |

---

## ストア（テンプレートマーケットプレイス）

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | テンプレートストアを有効にする。 |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | テンプレートの閲覧とインストール用のリモートレジストリ URL。 |
| `authToken` | string | `""` | レジストリの認証トークン。`$ENV_VAR` 対応。 |

---

## コストとアラート

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | 1 日の支出上限（USD）。`0` = 制限なし。 |
| `weeklyLimit` | float64 | `0` | 週の支出上限（USD）。`0` = 制限なし。 |
| `dailyTokenLimit` | int | `0` | 1 日の合計トークン上限（入力 + 出力）。`0` = 制限なし。 |
| `action` | string | `"warn"` | 上限超過時のアクション: `"warn"`（ログと通知）または `"pause"`（新しいタスクをブロック）。 |

### `estimate` — `EstimateConfig`

タスク実行前の事前コスト見積もり。

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | 見積もりコストがこの USD 値を超えると確認を求める。 |
| `defaultOutputTokens` | int | `500` | 実際の使用量が不明な場合のフォールバック出力トークン見積もり。 |

### `budgets` — `BudgetConfig`

agent レベルおよびチームレベルのコスト予算。

---

## ロギング

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `level` | string | `"info"` | ログレベル: `"debug"`、`"info"`、`"warn"`、`"error"`。 |
| `format` | string | `"text"` | ログ形式: `"text"`（人が読みやすい形式）または `"json"`（構造化）。 |
| `file` | string | `runtime/logs/tetora.log` | ログファイルのパス。runtime ディレクトリからの相対パス。 |
| `maxSizeMB` | int | `50` | ローテーション前の最大ログファイルサイズ（MB）。 |
| `maxFiles` | int | `5` | 保持するローテーション済みログファイルの数。 |

---

## セキュリティ

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | ダッシュボードで HTTP Basic 認証を有効にする。 |
| `username` | string | `"admin"` | Basic 認証のユーザー名。 |
| `password` | string | `""` | Basic 認証のパスワード。`$ENV_VAR` 対応。 |
| `token` | string | `""` | 代替手段: Cookie として渡される静的トークン。 |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| フィールド | 型 | 説明 |
|---|---|---|
| `certFile` | string | TLS 証明書 PEM ファイルへのパス。`keyFile` と合わせて設定すると HTTPS が有効になります。 |
| `keyFile` | string | TLS 秘密鍵 PEM ファイルへのパス。 |

### `rateLimit` — `RateLimitConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | IP ごとのリクエストレート制限を有効にする。 |
| `maxPerMin` | int | `60` | IP ごとの 1 分あたりの最大 API リクエスト数。 |

### `securityAlert` — `SecurityAlertConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | 繰り返しの認証失敗に対するセキュリティアラートを有効にする。 |
| `failThreshold` | int | `10` | アラートを発する前のウィンドウ内の失敗回数。 |
| `failWindowMin` | int | `5` | スライディングウィンドウ（分単位）。 |

### `approvalGates` — `ApprovalGateConfig`

特定のツールの実行前に人間の承認を必要とします。

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | 承認ゲートを有効にする。 |
| `timeout` | int | `120` | キャンセルするまでの承認待ち時間（秒）。 |
| `tools` | string[] | `[]` | 実行前に承認が必要なツール名。 |
| `autoApproveTools` | string[] | `[]` | 起動時に事前承認されるツール（プロンプト不要）。 |

---

## 信頼性

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `true` | プロバイダーフェイルオーバー用のサーキットブレーカーを有効にする。 |
| `failThreshold` | int | `5` | サーキットをオープンする前の連続失敗回数。 |
| `successThreshold` | int | `2` | クローズする前のハーフオープン状態での成功回数。 |
| `openTimeout` | string | `"30s"` | 再試行（ハーフオープン）するまでのオープン状態の継続時間。 |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

デフォルトプロバイダーが失敗した場合のグローバルフォールバックプロバイダーの順序付きリスト。

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | agent ハートビート監視を有効にする。 |
| `interval` | string | `"30s"` | 実行中タスクのストール確認頻度。 |
| `stallThreshold` | string | `"5m"` | この時間出力がないとタスクはストールとみなされる。 |
| `timeoutWarnRatio` | float64 | `0.8` | 経過時間がタスクタイムアウトのこの割合を超えたときに警告。 |
| `autoCancel` | bool | `false` | `2x stallThreshold` を超えてストールしているタスクを自動キャンセル。 |
| `notifyOnStall` | bool | `true` | タスクのストールが検出されたときに通知を送信。 |

### `retention` — `RetentionConfig`

古いデータの自動クリーンアップを制御します。

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `history` | int | `90` | ジョブ実行履歴の保持日数。 |
| `sessions` | int | `30` | セッションデータの保持日数。 |
| `auditLog` | int | `365` | 監査ログエントリの保持日数。 |
| `logs` | int | `14` | ログファイルの保持日数。 |
| `workflows` | int | `90` | ワークフロー実行レコードの保持日数。 |
| `reflections` | int | `60` | リフレクションレコードの保持日数。 |
| `sla` | int | `90` | SLA チェックレコードの保持日数。 |
| `trustEvents` | int | `90` | トラストイベントレコードの保持日数。 |
| `handoffs` | int | `60` | agent ハンドオフ/メッセージレコードの保持日数。 |
| `queue` | int | `7` | オフラインキューアイテムの保持日数。 |
| `versions` | int | `180` | 設定バージョンスナップショットの保持日数。 |
| `outputs` | int | `30` | agent 出力ファイルの保持日数。 |
| `uploads` | int | `7` | アップロードファイルの保持日数。 |
| `memory` | int | `30` | 古いメモリエントリがアーカイブされるまでの日数。 |
| `claudeSessions` | int | `3` | Claude CLI セッションアーティファクトの保持日数。 |
| `piiPatterns` | string[] | `[]` | 保存コンテンツの PII リダクション用の正規表現パターン。 |

---

## クワイエットアワーとダイジェスト

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | クワイエットアワーを有効にする。この時間帯は通知が抑制されます。 |
| `start` | string | `""` | クワイエット期間の開始時刻（現地時間、`"HH:MM"` 形式）。 |
| `end` | string | `""` | クワイエット期間の終了時刻（現地時間）。 |
| `tz` | string | ローカル | タイムゾーン（例: `"Asia/Taipei"`、`"UTC"`）。 |
| `digest` | bool | `false` | クワイエットアワー終了時に抑制された通知のダイジェストを送信する。 |

### `digest` — `DigestConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | スケジュールされた日次ダイジェストを有効にする。 |
| `time` | string | `"08:00"` | ダイジェストを送信する時刻（`"HH:MM"`）。 |
| `tz` | string | ローカル | タイムゾーン。 |

---

## ツール

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `maxIterations` | int | `10` | タスクごとのツール呼び出し最大イテレーション数。 |
| `timeout` | int | `120` | グローバルツールエンジンのタイムアウト（秒）。 |
| `toolOutputLimit` | int | `10240` | ツール出力あたりの最大文字数（超過分は切り捨て）。 |
| `toolTimeout` | int | `30` | ツールごとの実行タイムアウト（秒）。 |
| `defaultProfile` | string | `"standard"` | デフォルトのツールプロファイル名。 |
| `builtin` | map[string]bool | `{}` | 名前で個々の組み込みツールを有効/無効にする。 |
| `profiles` | map[string]ToolProfile | `{}` | カスタムツールプロファイル。 |
| `trustOverride` | map[string]string | `{}` | ツール名ごとのトラストレベル上書き。 |

### `tools.webSearch` — `WebSearchConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `provider` | string | `""` | 検索プロバイダー: `"brave"`、`"tavily"`、`"searxng"`。 |
| `apiKey` | string | `""` | プロバイダーの API キー。`$ENV_VAR` 対応。 |
| `baseURL` | string | `""` | カスタムエンドポイント（セルフホスト型 searxng 用）。 |
| `maxResults` | int | `5` | 返す最大検索結果数。 |

### `tools.vision` — `VisionConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `provider` | string | `""` | ビジョンプロバイダー: `"anthropic"`、`"openai"`、`"google"`。 |
| `apiKey` | string | `""` | API キー。`$ENV_VAR` 対応。 |
| `model` | string | `""` | ビジョンプロバイダーのモデル名。 |
| `maxImageSize` | int | `5242880` | 最大画像サイズ（バイト単位、デフォルト 5 MB）。 |
| `baseURL` | string | `""` | カスタム API エンドポイント。 |

---

## MCP（Model Context Protocol）

### `mcpConfigs`

名前付き MCP サーバー設定。各キーは MCP 設定名で、値は完全な MCP JSON 設定です。Tetora はこれらを一時ファイルに書き込み、`--mcp-config` 経由で claude バイナリに渡します。

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

Tetora が直接管理するシンプルな MCP サーバー定義。

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `command` | string | 必須 | 実行コマンド。 |
| `args` | string[] | `[]` | コマンド引数。 |
| `env` | map[string]string | `{}` | プロセスの環境変数。値は `$ENV_VAR` 対応。 |
| `enabled` | bool | `true` | この MCP サーバーが有効かどうか。 |

---

## プロンプト予算

システムプロンプトの各セクションの最大文字数を制御します。プロンプトが予期せず切り捨てられる場合に調整してください。

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `soulMax` | int | `8000` | agent のソウル/パーソナリティプロンプトの最大文字数。 |
| `rulesMax` | int | `4000` | ワークスペースルールの最大文字数。 |
| `knowledgeMax` | int | `8000` | ナレッジベースコンテンツの最大文字数。 |
| `skillsMax` | int | `4000` | 注入されたスキルの最大文字数。 |
| `maxSkillsPerTask` | int | `3` | タスクごとに注入するスキルの最大数。 |
| `contextMax` | int | `16000` | セッションコンテキストの最大文字数。 |
| `totalMax` | int | `40000` | システムプロンプト全体のハード上限（全セクション合計）。 |

---

## Agent 通信

ネストされたサブ agent ディスパッチ（agent_dispatch ツール）を制御します。

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| フィールド | 型 | デフォルト | 説明 |
|---|---|---|---|
| `enabled` | bool | `false` | ネストされたサブ agent 呼び出し用の `agent_dispatch` ツールを有効にする。 |
| `maxConcurrent` | int | `3` | グローバルな `agent_dispatch` 呼び出しの最大同時実行数。 |
| `defaultTimeout` | int | `900` | サブ agent のデフォルトタイムアウト（秒）。 |
| `maxDepth` | int | `3` | サブ agent の最大ネスト深度。 |
| `maxChildrenPerTask` | int | `5` | 親タスクあたりの最大同時子 agent 数。 |

---

## 設定例

### 最小設定

Claude CLI プロバイダーで始める最小設定:

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### スマートディスパッチを使ったマルチ agent 設定

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### フル設定（主要セクションすべて）

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
