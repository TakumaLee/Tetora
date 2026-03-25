---
title: "MCP（Model Context Protocol）インテグレーション"
lang: "ja"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# MCP（Model Context Protocol）インテグレーション

Tetora には組み込みの MCP サーバーが含まれており、AI agent（Claude Code など）が標準の MCP プロトコルを通じて Tetora の API を操作できます。

## アーキテクチャ

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

MCP サーバーは **stdio JSON-RPC 2.0 ブリッジ** です — stdin からリクエストを読み取り、Tetora の HTTP API にプロキシして、stdout にレスポンスを書き込みます。Claude Code がこれを子プロセスとして起動します。

## セットアップ

### 1. Claude Code の設定に MCP サーバーを追加

`~/.claude/settings.json` に以下を追加します:

```json
{
  "mcpServers": {
    "tetora": {
      "command": "/Users/you/.tetora/bin/tetora",
      "args": ["mcp-server"]
    }
  }
}
```

パスを実際の `tetora` バイナリの場所に置き換えてください。場所の確認方法:

```bash
which tetora
# または
ls ~/.tetora/bin/tetora
```

### 2. Tetora デーモンが起動していることを確認

MCP ブリッジは Tetora の HTTP API にプロキシするため、デーモンが起動している必要があります:

```bash
tetora start
```

### 3. 確認

Claude Code を再起動します。MCP ツールが `tetora_` プレフィックス付きの利用可能なツールとして表示されます。

## 利用可能なツール

### タスク管理

| ツール | 説明 |
|------|-------------|
| `tetora_taskboard_list` | カンバンボードのチケットを一覧表示する。任意フィルター: `project`、`assignee`、`priority`。 |
| `tetora_taskboard_update` | タスクを更新する（ステータス、担当者、優先度、タイトル）。`id` が必須。 |
| `tetora_taskboard_comment` | タスクにコメントを追加する。`id` と `comment` が必須。 |

### メモリ

| ツール | 説明 |
|------|-------------|
| `tetora_memory_get` | メモリエントリを読み取る。`agent` と `key` が必須。 |
| `tetora_memory_set` | メモリエントリを書き込む。`agent`、`key`、`value` が必須。 |
| `tetora_memory_search` | すべてのメモリエントリを一覧表示する。任意フィルター: `role`。 |

### ディスパッチ

| ツール | 説明 |
|------|-------------|
| `tetora_dispatch` | タスクを別の agent にディスパッチする。新しい Claude Code セッションを作成する。`prompt` が必須。任意: `agent`、`workdir`、`model`。 |

### ナレッジ

| ツール | 説明 |
|------|-------------|
| `tetora_knowledge_search` | 共有ナレッジベースを検索する。`q` が必須。任意: `limit`。 |

### 通知

| ツール | 説明 |
|------|-------------|
| `tetora_notify` | Discord/Telegram 経由でユーザーに通知を送信する。`message` が必須。任意: `level`（info/warn/error）。 |
| `tetora_ask_user` | Discord 経由でユーザーに質問し、回答を待つ（最大 6 分）。`question` が必須。任意: `options`（クイック返信ボタン、最大 4 つ）。 |

## ツールの詳細

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

すべてのパラメータは任意です。タスクの JSON 配列を返します。

### tetora_taskboard_update

```json
{
  "id": "TASK-42",
  "status": "in_progress",
  "assignee": "kokuyou",
  "priority": "P1",
  "title": "New title"
}
```

必須は `id` のみです。指定された場合のみ他のフィールドが更新されます。ステータス値: `todo`、`in_progress`、`review`、`done`。

### tetora_taskboard_comment

```json
{
  "id": "TASK-42",
  "comment": "Started working on this",
  "author": "kokuyou"
}
```

### tetora_dispatch

```json
{
  "prompt": "Fix the broken CSS on the dashboard sidebar",
  "agent": "kokuyou",
  "workdir": "/path/to/project",
  "model": "sonnet"
}
```

必須は `prompt` のみです。`agent` を省略すると、Tetora のスマートディスパッチが最適な agent にルーティングします。

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

これは**ブロッキング呼び出し**です — Discord 経由でユーザーが返答するまで最大 6 分間待機します。ユーザーにはオプションのクイック返信ボタンとともに質問が表示され、カスタム回答をタイプすることもできます。

## CLI コマンド

### 外部 MCP サーバーの管理

Tetora は MCP **ホスト**としても機能し、外部 MCP サーバーに接続できます:

```bash
# 設定済みの MCP サーバーを一覧表示
tetora mcp list

# サーバーのフル設定を表示
tetora mcp show <name>

# 新しい MCP サーバーを追加
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# サーバー設定を削除
tetora mcp remove <name>

# サーバー接続をテスト
tetora mcp test <name>
```

### MCP ブリッジの起動

```bash
# MCP ブリッジサーバーを起動（通常は Claude Code が起動するため手動実行は不要）
tetora mcp-server
```

初回実行時に、正しいバイナリパスを含む `~/.tetora/mcp/bridge.json` が生成されます。

## 設定

`config.json` の MCP 関連設定:

| フィールド | 型 | デフォルト | 説明 |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | 外部 MCP サーバー設定のマップ（名前 → {command, args, env}）。 |

ブリッジサーバーはメイン設定から `listenAddr` と `apiToken` を読み取ってデーモンに接続します。

## 認証

`config.json` に `apiToken` が設定されている場合、MCP ブリッジはデーモンへのすべての HTTP リクエストに自動的に `Authorization: Bearer <token>` を付加します。追加の MCP レベル認証は不要です。

## トラブルシューティング

**ツールが Claude Code に表示されない:**
- `settings.json` のバイナリパスが正しいか確認する
- Tetora デーモンが起動していることを確認する（`tetora start`）
- Claude Code のログで MCP 接続エラーを確認する

**"HTTP 401" エラー:**
- `config.json` の `apiToken` が一致している必要があります。ブリッジは自動的にそれを読み取ります。

**"connection refused" エラー:**
- デーモンが起動していないか、`listenAddr` が一致していません。デフォルト: `127.0.0.1:8991`。

**`tetora_ask_user` がタイムアウトする:**
- ユーザーは Discord 経由で 6 分以内に返答する必要があります。Discord ボットが接続されていることを確認してください。
