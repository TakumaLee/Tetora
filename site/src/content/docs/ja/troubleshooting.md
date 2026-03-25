---
title: "トラブルシューティングガイド"
lang: "ja"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# トラブルシューティングガイド

このガイドでは Tetora の運用中によく発生する問題を取り上げます。各問題について、最も可能性の高い原因を先頭に記載しています。

---

## tetora doctor

まずここから始めてください。インストール後や何かが動かなくなったときは `tetora doctor` を実行してください:

```
=== Tetora Doctor ===

  ✓ Config          /Users/you/.tetora/config.json
  ✓ Claude CLI      claude 1.2.3
  ✓ Provider        claude-cli
  ✓ Port            localhost:8991 in use (daemon running)
  ✓ Telegram        enabled (chatID=123456)
  ✓ Jobs            jobs.json (4 jobs, 3 enabled)
  ✓ History DB      12 tasks
  ✓ Workdir         /Users/you/dev
  ✓ Agent/ruri      Commander
  ✓ Binary          /Users/you/.tetora/bin/tetora
  ✓ Encryption      key configured
  ✓ ffmpeg          available
  ✓ sqlite3         available
  ✓ Agents Dir      /Users/you/.tetora/agents (3 agents)
  ✓ Workspace       /Users/you/.tetora/workspace

All checks passed.
```

各行が 1 つのチェックです。赤い `✗` はハード失敗（修正しないとデーモンが動作しません）、黄色の `~` は提案（任意だが推奨）を意味します。

失敗したチェックの一般的な修正方法:

| 失敗したチェック | 修正方法 |
|---|---|
| `Config: not found` | `tetora init` を実行する |
| `Claude CLI: not found` | `config.json` に `claudePath` を設定するか、Claude Code をインストールする |
| `sqlite3: not found` | `brew install sqlite3`（macOS）または `apt install sqlite3`（Linux） |
| `Agent/name: soul file missing` | `~/.tetora/agents/{name}/SOUL.md` を作成するか `tetora init` を実行する |
| `Workspace: not found` | `tetora init` を実行してディレクトリ構造を作成する |

---

## "session produced no output"

タスクが完了したが出力が空の場合。タスクは自動的に `failed` とマークされます。

**原因 1: コンテキストウィンドウが大きすぎる。** セッションに注入されたプロンプトがモデルのコンテキスト制限を超えた。Claude Code はコンテキストが収まらないと即座に終了します。

修正: `config.json` でセッション圧縮を有効にします:

```json
{
  "sessionCompaction": {
    "enabled": true,
    "tokenThreshold": 150000,
    "messageThreshold": 100,
    "strategy": "auto"
  }
}
```

または、タスクに注入されるコンテキストの量を減らします（説明を短くする、spec コメントを減らす、`dependsOn` チェーンを短くするなど）。

**原因 2: Claude Code CLI の起動失敗。** `claudePath` のバイナリが起動時にクラッシュしている — 通常は API キーの問題、ネットワーク障害、またはバージョンの不一致が原因です。

修正: Claude Code バイナリを手動で実行してエラーを確認します:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

報告されたエラーを修正してからタスクをリトライします:

```bash
tetora task move task-abc123 --status=todo
```

**原因 3: プロンプトが空。** タスクにタイトルはあるが説明がなく、タイトルだけでは agent が行動するには曖昧すぎる。セッションが実行され、空チェックを満たさない出力が生成され、フラグが立てられます。

修正: 具体的な説明を追加します:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## ダッシュボードで "unauthorized" エラー

ダッシュボードが 401 を返すか、リロード後に空白ページが表示される。

**原因 1: Service Worker が古い認証トークンをキャッシュしている。** PWA の Service Worker はレスポンス（認証ヘッダーを含む）をキャッシュします。新しいトークンでデーモンを再起動すると、キャッシュされたバージョンが古くなります。

修正: ページをハード再読み込みします。Chrome/Safari の場合:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

または DevTools → Application → Service Workers を開き「Unregister」をクリックしてからリロードします。

**原因 2: Referer ヘッダーの不一致。** ダッシュボードの認証ミドルウェアが `Referer` ヘッダーを検証します。ブラウザ拡張、プロキシ、または `Referer` ヘッダーなしの curl からのリクエストは拒否されます。

修正: プロキシを経由せず `http://localhost:8991/dashboard` に直接アクセスしてください。外部ツールから API にアクセスする必要がある場合は、ブラウザセッション認証の代わりに API トークンを使用してください。

---

## ダッシュボードが更新されない

ダッシュボードは読み込まれるが、アクティビティフィード、ワーカーリスト、タスクボードが古いままになっている。

**原因: Service Worker のバージョン不一致。** PWA の Service Worker は `make bump` 更新後もダッシュボードの JS/HTML のキャッシュバージョンを提供し続けます。

修正:

1. ハード再読み込み（`Cmd + Shift + R` / `Ctrl + Shift + R`）
2. それで解決しない場合は DevTools → Application → Service Workers を開き「Update」または「Unregister」をクリック
3. ページをリロード

**原因: SSE 接続が切れた。** ダッシュボードは Server-Sent Events でライブ更新を受信します。接続が切れると（ネットワークの不具合、ラップトップのスリープなど）フィードの更新が止まります。

修正: ページをリロードします。SSE 接続はページロード時に自動的に再確立されます。

---

## "排程接近滿載" 警告

Discord/Telegram やダッシュボードの通知フィードにこのメッセージが表示される。

これは slot pressure 警告です。利用可能な同時実行スロットが `warnThreshold`（デフォルト: 3）以下に落ちたときに発火します。Tetora が限界近くで稼働していることを意味します。

**対処方法:**

- 多くのタスクが実行中であれば: 特に対処不要。この警告は情報提供目的です。
- 多くのタスクを実行していない場合: `doing` ステータスで詰まっているタスクを確認します:

```bash
tetora task list --status=doing
```

- 容量を増やしたい場合: `config.json` の `maxConcurrent` を増やし、`slotPressure.warnThreshold` を適宜調整します。
- インタラクティブセッションが遅延している場合: インタラクティブ用に確保するスロットを増やすために `slotPressure.reservedSlots` を増やします。

---

## "doing" で詰まっているタスク

タスクが `status=doing` を示しているが、agent が積極的に作業していない。

**原因 1: タスク実行中にデーモンが再起動した。** タスクが実行中にデーモンが強制終了された。次の起動時に Tetora は孤立した `doing` タスクを確認し、コスト/実行時間の証拠があれば `done` に、本当に孤立していれば `todo` にリセットします。

これは自動的に処理されます — 次のデーモン起動を待ってください。デーモンがすでに起動中でタスクがまだ詰まっている場合、ハートビートまたはスタックタスクリセットが `stuckThreshold`（デフォルト: 2h）以内に対処します。

即座にリセットするには:

```bash
tetora task move task-abc123 --status=todo
```

**原因 2: ハートビート/ストール検出。** ハートビートモニター（`heartbeat.go`）が実行中のセッションを確認します。セッションがストール閾値の間出力を生成しない場合、自動的にキャンセルされてタスクは `failed` に移動します。

タスクのコメントで `[auto-reset]` または `[stall-detected]` のシステムコメントを確認します:

```bash
tetora task show task-abc123 --full
```

**API 経由での手動キャンセル:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Worktree のマージ失敗

タスクが完了し `[worktree] merge failed` のようなコメントとともに `partial-done` に移動した。

これは、タスクブランチの agent の変更が `main` と競合していることを意味します。

**回復手順:**

```bash
# タスクの詳細と作成されたブランチを確認
tetora task show task-abc123 --full

# プロジェクトリポジトリに移動
cd /path/to/your/repo

# ブランチを手動でマージ
git merge feat/kokuyou-task-abc123

# エディタで競合を解決してからコミット
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# タスクを完了とマーク
tetora task move task-abc123 --status=done
```

worktree ディレクトリは手動でクリーンアップするかタスクを `done` に移動するまで `~/.tetora/runtime/worktrees/task-abc123/` に保持されます。

---

## トークンコストが高い

セッションが予想より多くのトークンを使用している。

**原因 1: コンテキストが圧縮されていない。** セッション圧縮なしでは、各ターンに完全な会話履歴が蓄積されます。長時間実行タスク（多くのツール呼び出し）はコンテキストが線形に増加します。

修正: `sessionCompaction` を有効にします（上記「session produced no output」セクション参照）。

**原因 2: 大きなナレッジベースやルールファイル。** `workspace/rules/` と `workspace/knowledge/` のファイルはすべての agent プロンプトに注入されます。これらのファイルが大きいと、毎回の呼び出しでトークンを消費します。

修正:
- `workspace/knowledge/` を監査する — 個々のファイルを 50 KB 以下に保つ。
- あまり使わない参照資料を自動注入パスから移動する。
- `tetora knowledge list` を実行して注入されているものとそのサイズを確認する。

**原因 3: 誤ったモデルルーティング。** 高価なモデル（Opus）が定型タスクに使用されている。

修正: agent 設定の `defaultModel` を確認し、バルクタスク用に安価なデフォルトを設定します:

```json
{
  "taskBoard": {
    "autoDispatch": {
      "defaultModel": "sonnet"
    }
  }
}
```

---

## プロバイダーのタイムアウトエラー

`context deadline exceeded` や `provider request timed out` などのタイムアウトエラーでタスクが失敗する。

**原因 1: タスクのタイムアウトが短すぎる。** デフォルトのタイムアウトが複雑なタスクには短すぎる場合があります。

修正: agent 設定またはタスクごとに長いタイムアウトを設定します:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

または、タスクの説明に詳細を追加してタイムアウト見積もりを増やします（Tetora は高速モデル呼び出しで説明を使って見積もりを行います）。

**原因 2: API のレート制限または競合。** 同じプロバイダーに同時リクエストが多すぎる。

修正: `maxConcurrentTasks` を減らすか、高コストタスクをスロットリングするために `maxBudget` を追加します:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` がワークフローを中断した

ワークフローやタスクの実行中に `make bump` を実行した。デーモンがタスク実行中に再起動した。

再起動により Tetora の孤立回復ロジックがトリガーされます:

- 完了の証拠があるタスク（コストが記録済み、実行時間が記録済み）は `done` に復元される。
- 完了の証拠がなくグレース期間（2 分）を過ぎたタスクは、再ディスパッチのために `todo` にリセットされる。
- 直近 2 分以内に更新されたタスクは、次のスタックタスクスキャンまで放置される。

**何が起きたか確認するには:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

`[auto-restore]` または `[auto-reset]` エントリのタスクコメントを確認します。

**アクティブなタスク実行中の bump を防ぐには**（まだフラグとして利用できません）、bump 前にタスクが実行中でないか確認します:

```bash
tetora task list --status=doing
# 空であれば bump しても安全
make bump
```

---

## SQLite エラー

ログに `database is locked`、`SQLITE_BUSY`、または `index.lock` などのエラーが表示される。

**原因 1: WAL モードのプラグマが欠如している。** WAL モードなしでは SQLite は排他ファイルロックを使用するため、同時読み書き時に `database is locked` が発生します。

すべての Tetora DB 呼び出しは `queryDB()` と `execDB()` を通じて行われ、先頭に `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;` が付加されます。スクリプトで sqlite3 を直接呼び出す場合は、これらのプラグマを追加してください:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**原因 2: 古い `index.lock` ファイル。** git 操作が中断されると `index.lock` が残ります。worktree マネージャーは git 操作の開始前に古いロックを確認しますが、クラッシュにより残ることがあります。

修正:

```bash
# 古いロックファイルを検索
find ~/.tetora/runtime/worktrees -name "index.lock"

# 削除（git 操作がアクティブに実行されていない場合のみ）
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram が応答しない

ボットへのメッセージに返信がない。

**原因 1: チャネル設定の誤り。** Discord には 2 つのチャネルリストがあります: `channelIDs`（すべてのメッセージに直接返信）と `mentionChannelIDs`（@メンションされた場合のみ返信）。どちらのリストにもないチャネルのメッセージは無視されます。

修正: `config.json` を確認します:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**原因 2: ボットトークンが期限切れまたは誤っている。** Telegram のボットトークンは期限切れになりませんが、Discord のトークンはサーバーからボットが追い出されたりトークンが再生成されると無効になることがあります。

修正: Discord 開発者ポータルでボットトークンを再作成し、`config.json` を更新します。

**原因 3: デーモンが起動していない。** ボットゲートウェイは `tetora serve` が実行中の場合のみアクティブです。

修正:

```bash
tetora status
tetora serve   # 起動していない場合
```

---

## glab / gh CLI エラー

`glab` または `gh` からのエラーで git インテグレーションが失敗する。

**よくあるエラー: `gh: command not found`**

修正:
```bash
brew install gh      # macOS
gh auth login        # 認証
```

**よくあるエラー: `glab: You are not logged in`**

修正:
```bash
brew install glab    # macOS
glab auth login      # GitLab インスタンスで認証
```

**よくあるエラー: `remote: HTTP Basic: Access denied`**

修正: リポジトリホスト用の SSH キーまたは HTTPS クレデンシャルが設定されていることを確認します。GitLab の場合:

```bash
glab auth status
ssh -T git@gitlab.com   # SSH 接続をテスト
```

GitHub の場合:

```bash
gh auth status
ssh -T git@github.com
```

**PR/MR の作成は成功するが間違ったベースブランチを指している**

デフォルトでは PR はリポジトリのデフォルトブランチ（`main` または `master`）を対象とします。ワークフローで別のベースを使用する場合は、ポストタスクの git 設定で明示的に設定するか、ホスティングプラットフォームでリポジトリのデフォルトブランチが正しく設定されていることを確認してください。
