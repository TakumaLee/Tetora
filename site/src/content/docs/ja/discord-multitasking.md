---
title: "Discord マルチタスク ガイド"
lang: "ja"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# Discord マルチタスク ガイド

Tetora は Discord 上で **Thread + `/focus`** を使ったマルチタスク並行会話をサポートしています。各 thread は独立した session と agent バインディングを持ちます。

---

## 基本概念

### メインチャネル — 単一セッション

各 Discord チャネルは **1 つのアクティブな session** のみを持ちます。すべてのメッセージが同じ会話コンテキストを共有します。

- Session キーの形式: `discord:{channelID}`
- 同じチャネル内のすべての人のメッセージが同一 session に入ります
- `!new` でリセットするまで会話履歴が継続的に蓄積されます

### Thread — 独立セッション

Discord の thread は `/focus` で特定の agent にバインドでき、完全に独立した session を持てます。

- Session キーの形式: `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- メインチャネルの session と完全に分離されており、コンテキストは互いに干渉しません
- 各 thread を異なる agent にバインドできます

---

## コマンド

| コマンド | 場所 | 説明 |
|---|---|---|
| `/focus <agent>` | Thread 内 | この thread を指定した agent にバインドし、独立した session を作成する |
| `/unfocus` | Thread 内 | Thread の agent バインドを解除する |
| `!new` | メインチャネル | 現在の session をアーカイブし、次のメッセージで新しい session を開始する |

---

## マルチタスクの操作フロー

### Step 1: Discord Thread を作成する

メインチャネルのメッセージを右クリック → **Create Thread**（または Discord の thread 作成機能を使用）。

### Step 2: Thread 内で Agent をバインドする

```
/focus ruri
```

バインドが成功すると、この thread 内のすべての会話が:
- ruri の SOUL.md のキャラクター設定を使用する
- 独立した会話履歴を持つ
- メインチャネルの session に影響しない

### Step 3: 必要に応じて複数の Thread を開く

```
#general（メインチャネル）            ← 日常会話、1 つの session
  └─ Thread: "auth モジュールを重構する"  ← /focus kokuyou → 独立 session
  └─ Thread: "今週のブログを書く"          ← /focus kohaku  → 独立 session
  └─ Thread: "競合分析レポート"            ← /focus hisui   → 独立 session
  └─ Thread: "プロジェクト計画の議論"      ← /focus ruri    → 独立 session
```

各 thread は完全に分離された会話空間で、同時進行できます。

---

## 注意事項

### TTL（有効期限）

- Thread バインドはデフォルトで **24 時間**後に失効します
- 失効後、thread は通常モードに戻ります（メインチャネルのルーティングロジックに従います）
- `config.json` の `threadBindings.ttlHours` で調整できます

### 並行数の制限

- グローバルな最大並行数は `maxConcurrent`（デフォルト: 8）で制御されます
- すべてのチャネルと thread がこの上限を共有します
- 上限を超えたメッセージはキューに入り待機します

### 設定の有効化

thread bindings が config で有効になっていることを確認します:

```json
{
  "discord": {
    "threadBindings": {
      "enabled": true,
      "ttlHours": 24
    }
  }
}
```

### メインチャネルの制限

- メインチャネルでは `/focus` で 2 つ目の session を作成できません
- 会話コンテキストをリセットしたい場合は `!new` を使用してください
- 同じチャネルで複数のメッセージを同時に送信すると session を共有するため、コンテキストが互いに干渉することがあります

---

## 利用シーン別の推奨事項

| シーン | 推奨する方法 |
|---|---|
| 日常会話・簡単な質問応答 | メインチャネルで直接会話する |
| 特定のトピックに集中して議論したい | Thread を作って `/focus` する |
| 異なるタスクを異なる agent に割り当てたい | タスクごとに thread を作り、それぞれ対応する agent に `/focus` する |
| 会話コンテキストが長くなってリセットしたい | メインチャネルは `!new`、thread は `/unfocus` してから `/focus` し直す |
| 複数人が同じトピックで共同作業したい | 共有 thread を 1 つ作り、全員が thread 内で会話する |
