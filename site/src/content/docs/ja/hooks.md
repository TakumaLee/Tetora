---
title: "Claude Code Hooks インテグレーション"
lang: "ja"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Claude Code Hooks インテグレーション

## 概要

Claude Code Hooks は Claude Code に組み込まれたイベントシステムで、セッション中の重要なタイミングでシェルコマンドを発火します。Tetora はフックレシーバーとして自身を登録することで、ポーリングや tmux、ラッパースクリプトの注入なしに、すべての実行中 agent セッションをリアルタイムで監視できます。

**hooks によって実現できること:**

- ダッシュボードでのリアルタイム進捗追跡（ツール呼び出し、セッション状態、ライブワーカーリスト）
- statusline ブリッジ経由のコストとトークン監視
- ツール使用の監査（どのツールが、どのセッションで、どのディレクトリで実行されたか）
- セッション完了の検出とタスクステータスの自動更新
- プランモードゲート: ダッシュボードで人間がプランを承認するまで `ExitPlanMode` を保留
- インタラクティブな質問ルーティング: `AskUserQuestion` が MCP ブリッジにリダイレクトされ、ターミナルをブロックせずにチャットプラットフォームに質問が表示される

Hooks は Tetora v2.0 以降の推奨インテグレーション方式です。古い tmux ベースのアプローチ（v1.x）は引き続き動作しますが、プランゲートや質問ルーティングなどの hooks 専用機能はサポートされません。

---

## アーキテクチャ

```
Claude Code session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan gate: long-poll until approved
  │   (AskUserQuestion)               └─► Deny: redirect to MCP bridge
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► Update worker state
  │                                   └─► Detect plan file writes
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► Mark worker done
  │                                   └─► Trigger task completion
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► Forward to Discord/Telegram
```

フックコマンドは Claude Code の `~/.claude/settings.json` に注入された小さな curl 呼び出しです。すべてのイベントは実行中の Tetora デーモンの `POST /api/hooks/event` にポストされます。

---

## セットアップ

### hooks のインストール

Tetora デーモンを起動した状態で:

```bash
tetora hooks install
```

これにより `~/.claude/settings.json` にエントリが書き込まれ、`~/.tetora/mcp/bridge.json` に MCP ブリッジ設定が生成されます。

`~/.claude/settings.json` に書き込まれる内容の例:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST http://localhost:8991/api/hooks/event -H 'Content-Type: application/json' -d @-"
          }
        ]
      }
    ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "Notification": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [ { "type": "command", "command": "...", "timeout": 600 } ]
      },
      {
        "matcher": "AskUserQuestion",
        "hooks": [ { "type": "command", "command": "..." } ]
      }
    ]
  }
}
```

### ステータスの確認

```bash
tetora hooks status
```

出力には、インストール済みの hooks、登録された Tetora ルールの数、デーモン起動後に受信したイベントの総数が表示されます。

ダッシュボードからも確認できます: **Engineering Details → Hooks** に同じステータスとライブイベントフィードが表示されます。

### hooks の削除

```bash
tetora hooks remove
```

`~/.claude/settings.json` からすべての Tetora エントリを削除します。Tetora 以外の既存の hooks は保持されます。

---

## フックイベント

### PostToolUse

すべてのツール呼び出し完了後に発火します。Tetora はこれを以下の用途に使用します:

- agent が使用しているツールの追跡（`Bash`、`Write`、`Edit`、`Read` など）
- ライブワーカーリストのワーカーの `lastTool` と `toolCount` の更新
- agent がプランファイルに書き込んだことの検出（プランキャッシュの更新をトリガー）

### Stop

Claude Code セッションが終了したとき（正常完了またはキャンセル）に発火します。Tetora はこれを以下の用途に使用します:

- ライブワーカーリストのワーカーを `done` としてマーク
- ダッシュボードへの完了 SSE イベントの発行
- タスクボードタスクの下流タスクステータス更新のトリガー

### Notification

Claude Code が通知を送信するとき（例: パーミッション要求、長い一時停止）に発火します。Tetora はこれらを Discord/Telegram に転送し、ダッシュボードの SSE ストリームに発行します。

### PreToolUse: ExitPlanMode（プランゲート）

agent がプランモードを終了しようとすると、Tetora はロングポール（タイムアウト: 600 秒）でイベントをインターセプトします。プランコンテンツがキャッシュされ、ダッシュボードのセッション詳細ビューに表示されます。

人間はダッシュボードからプランを承認または却下できます。承認されると、フックが戻り Claude Code が処理を続行します。却下された場合（またはタイムアウトが切れた場合）、終了がブロックされ Claude Code はプランモードのままになります。

### PreToolUse: AskUserQuestion（質問ルーティング）

Claude Code がインタラクティブにユーザーに質問しようとすると、Tetora はこれをインターセプトしてデフォルトの動作を拒否します。質問は代わりに MCP ブリッジ経由でルーティングされ、設定済みのチャットプラットフォーム（Discord、Telegram など）に表示されるため、ターミナルに張り付かずに回答できます。

---

## リアルタイム進捗追跡

hooks のインストール後、ダッシュボードの **Workers** パネルにライブセッションが表示されます:

| フィールド | ソース |
|---|---|
| セッション ID | フックイベントの `session_id` |
| 状態 | `working` / `idle` / `done` |
| 最後のツール | 最新の `PostToolUse` ツール名 |
| 作業ディレクトリ | フックイベントの `cwd` |
| ツール呼び出し数 | `PostToolUse` の累計カウント |
| コスト / トークン | statusline ブリッジ（`POST /api/hooks/usage`） |
| 起源 | Tetora からディスパッチされた場合のリンク先タスクまたは cron ジョブ |

コストとトークンのデータは Claude Code statusline スクリプトから来ており、設定可能な間隔で `/api/hooks/usage` にポストします。statusline スクリプトは hooks とは別のものです。Claude Code のステータスバー出力を読み取り、Tetora に転送します。

---

## コスト監視

usage エンドポイント（`POST /api/hooks/usage`）が受信するデータ:

```json
{
  "sessionId": "abc123",
  "costUsd": 0.0042,
  "inputTokens": 8200,
  "outputTokens": 340,
  "contextPct": 12,
  "model": "claude-sonnet-4-5"
}
```

このデータはダッシュボードの Workers パネルに表示され、日次コストチャートに集計されます。セッションのコストが設定されたロールごとまたはグローバルの予算を超えると、予算アラートが発火します。

---

## トラブルシューティング

### hooks が発火しない

**デーモンが動いているか確認:**
```bash
tetora status
```

**hooks がインストールされているか確認:**
```bash
tetora hooks status
```

**settings.json を直接確認:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

hooks キーがない場合は `tetora hooks install` を再実行してください。

**デーモンがフックイベントを受信できるか確認:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# Expected: {"ok":true}
```

デーモンが期待するポートでリッスンしていない場合は、`config.json` の `listenAddr` を確認してください。

### settings.json のパーミッションエラー

Claude Code の `settings.json` は `~/.claude/settings.json` にあります。ファイルが別のユーザー所有または制限的なパーミッションになっている場合:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### ダッシュボードの workers パネルが空

1. hooks がインストールされデーモンが動いていることを確認する。
2. Claude Code セッションを手動で開始し、ツールを 1 つ実行する（例: `ls`）。
3. ダッシュボードの Workers パネルを確認する — 数秒以内にセッションが表示されるはずです。
4. 表示されない場合は、デーモンログを確認する: `tetora logs -f | grep hooks`

### プランゲートが表示されない

プランゲートは Claude Code が `ExitPlanMode` を呼び出そうとしたときにのみ有効になります。これはプランモードセッション（`--plan` で開始、またはロール設定で `permissionMode: "plan"` を設定）でのみ発生します。インタラクティブな `acceptEdits` セッションはプランモードを使用しません。

### 質問がチャットにルーティングされない

`AskUserQuestion` の deny フックには MCP ブリッジの設定が必要です。`tetora hooks install` を再実行してください — ブリッジ設定が再生成されます。その後、ブリッジを Claude Code の MCP 設定に追加してください:

```bash
cat ~/.tetora/mcp/bridge.json
```

このファイルを `~/.claude/settings.json` の `mcpServers` の下に MCP サーバーとして追加してください。

---

## tmux からの移行（v1.x）

Tetora v1.x では、agent は tmux ペインの中で動作し、Tetora はペインの出力を読み取ることで監視していました。v2.0 では、agent は bare な Claude Code プロセスとして動作し、Tetora は hooks を通じて監視します。

**v1.x からアップグレードする場合:**

1. アップグレード後に `tetora hooks install` を一度実行する。
2. `config.json` から tmux セッション管理の設定を削除する（`tmux.*` キーは無視されます）。
3. 既存のセッション履歴は `history.db` に保持されます — マイグレーションは不要です。
4. `tetora session list` コマンドとダッシュボードの Sessions タブは従来通り動作し続けます。

tmux ターミナルブリッジ（`discord_terminal.go`）は Discord 経由のインタラクティブターミナルアクセスとして引き続き利用できます。これは agent の実行とは別のものです — 実行中のターミナルセッションにキーストロークを送信できます。hooks とターミナルブリッジは補完的なもので、相互に排他的ではありません。
