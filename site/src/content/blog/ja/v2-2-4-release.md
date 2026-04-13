---
title: "Tetora v2.2.4 — Discord の信頼性向上、Worktree 安全ロック、Dispatch の強化"
lang: ja
date: "2026-04-13"
tag: release
readTime: "~5 分"
excerpt: "v2.2.4 はランタイムの安定性を強化します。Discord はメッセージ送信のリトライと古い Session の自動修復に対応。Worktree ロックが Bash ツールの永続的な障害を防止。Dispatch したタスクは HTTP 切断後も継続実行されます。"
description: "Tetora v2.2.4 リリースノート：Discord 送信リトライ、Stale Session 自動修復、Worktree Session ロック、HTTP Dispatch の分離、Provider のフォールトトレランス、Coordinator の切り捨て修正、エージェントごとの並行数上限設定。"
---

v2.2.4 は安定性に特化したリリースです。ほとんどの変更は正常動作時には目に見えませんが、高負荷時・Provider 切り替え後・長時間エージェントセッション中にシステムを安定させる仕組みがここにあります。

> **TL;DR：** Discord がメッセージ送信を自動リトライし、古い Session を自動修復するようになりました。Session ロックが Worktree の削除による Bash ツール障害を防ぎます。Dispatch されたタスクは HTTP 切断後も実行を継続します。Provider エラーが分類され自動リトライされます。Findings サマリーの切り捨てが廃止されました。`maxTasksPerAgent` でエージェントごとの並行数を制限できます。

---

## Discord：高負荷下での信頼性

Discord はこのリリースで最も改善されたインターフェースです。3 つの修正が同時に提供されます。

### 送信リトライ

Discord へのメッセージ送信が一時的な失敗時に自動リトライするようになりました。修正前は、長いエージェント応答の途中での API 失敗によりメッセージが無音で消失することがありました。Tetora はバックオフ付きでリトライし、すべての試行が失敗した場合に警告ログを出力します。

### 送信前の出力の永続化

エージェント出力は、Discord メッセージのチャンクに分割される前にディスクへ書き込まれるようになりました。以前はチャンク分割の途中でクラッシュが発生すると、部分的な配信が起こりリカバリーができませんでした。永続化により、再起動後に最後の書き込みチェックポイントから再開できます。

### Stale Session の自動修復

Provider の切り替え・別マシンへの移行・Daemon の再起動によって Discord Session が古くなった場合、Tetora が自動的に検出して修復するようになりました。以前は手動での `/reset` または設定の再起動が必要でした。

```
[discord] stale session detected for agent hisui — recovering
[discord] session recovered: channel 1234567890 → agent hisui
```

ユーザー操作は不要です。

---

## エージェントの安全性：Worktree Session ロック

エージェントが git worktree を使用している場合、Tetora はその worktree パスに Session ロックを取得するようになりました。定期メンテナンスや `tetora worktree prune` コマンドを含むクリーンアップジョブは、ロックが解放されるまで待機します。

**なぜ重要か：** 以前は、Claude セッションが worktree を使用中に削除されると、Bash ツールがセッション終了まで永続的な障害状態に入ることがありました。エージェントが動いているように見えても、シェルコマンドを実行できなくなっていました。長時間タスクでは特に問題でした。

この修正では `SessionLockFile` 定数とアドバイザリーロック機構が導入されています：

```go
// Worktree で Claude セッションが開始されるときに Tetora がこのロックを取得する
const SessionLockFile = ".tetora-session.lock"

// クリーンアップジョブは削除前にロックを確認する
func pruneWorktree(path string) error {
    if isLocked(path) {
        return ErrSessionActive
    }
    return os.RemoveAll(path)
}
```

クリーンアップジョブがロックされた worktree を検出した場合、スキップして警告をログに記録します。セッション終了後に次回 prune が実行されたときにクリーンアップされます。

---

## HTTP：Dispatch Context の分離

Dispatch されたタスクは、それを作成した HTTP リクエストとは独立した Context で実行されるようになりました。

修正前は、長時間実行するエージェントタスクが `/api/dispatch` HTTP リクエストの Context を継承していました。HTTP クライアントが切断するか、上流のプロキシがタイムアウトすると Context がキャンセルされ、実行中のタスクが強制終了されました。

各 Dispatch タスク用に独立した Context を作成することで修正されています：

```go
// 修正前：HTTP リクエスト切断でタスクも終了
taskCtx := r.Context()

// 修正後：タスクが独立して実行
taskCtx := context.WithoutCancel(r.Context())
```

HTTP レスポンスはタスクがキューに追加された直後に返ります。クライアントの接続状態に関係なく、タスクは完了まで実行されます。

---

## Provider のフォールトトレランス

### Claude エラー分類

Tetora が Claude API の一時的なエラー（レート制限、一時的な過負荷）と永続的な障害（無効な API キー、アカウント問題）を区別するようになりました。一時的なエラーは指数バックオフで自動リトライします。永続的なエラーはリトライ予算を使わずに即座に表面化します。

### Codex クォータ検出

stdout または stderr のいずれかに現れる可能性がある Codex のクォータおよび使用量上限エラーが正しく検出・処理されるようになりました。クォータエラーが検出されると、タスクを失敗としてマークするのではなく、バックオフ後にリトライをスケジュールします。

```
[provider] codex quota exceeded — retrying in 45s (attempt 2/3)
```

---

## Coordinator：Findings の切り捨て廃止

Coordinator の Findings サマリーは、マルチエージェントチェーンの次のエージェントに渡される前に 500 文字に切り捨てられていました。エージェントの出力が詳細な場合、受信エージェントが不完全な情報に基づいて意思決定を行うというサイレントなデータロスを引き起こしていました。

500 文字の制限を廃止しました。Findings はエージェント間で完全に渡されるようになります。

---

## エージェントごとの並行数上限

`config.json` の新しい `maxTasksPerAgent` フィールドで、単一エージェントで同時実行できるタスク数を制限できます：

```json
{
  "agents": [
    {
      "name": "hisui",
      "role": "researcher",
      "maxTasksPerAgent": 2
    }
  ]
}
```

エージェントが上限に達すると、新しいタスクはすぐに Dispatch されるのではなく、キューに入ります。これにより、並行リクエストのバーストが単一エージェントのパフォーマンスを低下させることを防ぎます。レート制限のある API キーやローカルハードウェアで実行されるエージェントに特に有効です。

デフォルト（未設定の場合）は無制限で、後方互換性が維持されます。

---

## Workflow の堅牢化

Workflow エンジンの信頼性を向上させる 2 つの修正：

**Template ref バリデーション** — 存在しないステップやエージェントを参照している Workflow テンプレートが、実行途中でサイレントに失敗するのではなく、Dispatch 時に明確なエラーメッセージで即座に失敗するようになりました。

**DB 書き込みの正確性** — `InitWorkflowRunsTable` などの書き込み操作が `db.Query` ではなく `db.Exec` を使用するようになりました。SQLite では機能的に同等ですが、意味的に誤りで、高負荷時に接続プールの警告が発生していました。すべての書き込みパスが `db.Exec` を使用するようになりました。

---

## その他の修正

- **Workspace シンボリックリンクの追跡** — `tetora workspace files` がファイルを一覧表示する際にシンボリックリンクを追跡するようになり、エージェントが Workspace をナビゲートする動作と一致するようになりました。
- **Session 圧縮での URL 保持** — URL と一意な識別子（ハッシュ、ID）が Session 圧縮中に保持されるようになり、長いセッションでの参照破損を防ぎます。
- **Exit-0 警告ログ** — CLI 呼び出しがコード 0 で終了したが出力がない場合、Runner が `WARN` ログを出力するようになりました。サイレントな成功とサイレントな失敗を区別します。
- **ctx 伝播** — Context のキャンセルが goroutine 内部の DB 呼び出しを通じて正しく伝播されるようになり、タスクキャンセル時の Context リークを防ぎます。

---

## アップグレード

```bash
tetora upgrade
```

単一バイナリ。外部依存なし。macOS / Linux / Windows 対応。

[GitHub で完全な Changelog を確認する](https://github.com/TakumaLee/Tetora/releases/tag/v2.2.4)
