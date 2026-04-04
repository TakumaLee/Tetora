---
title: "Auto-Dispatch の基本 — タスクをエージェントのキューに積む"
lang: ja
date: "2026-04-02"
excerpt: "Tetora の dispatch システムを使ってタスクをキューに入れ、エージェントが自動でピックアップする仕組みを学ぼう。"
description: "Tetora auto-dispatch 入門：タスクのキューイング、エージェント指定、シンプルな JSON 設定で最初の自動化ワークフローを作る方法。"
---

## 問題

エージェントにやらせたいことが3つある——調査、下書き執筆、Discord への投稿。dispatch がなければ、各ステップの出力をコピペして次のターミナルで手動実行することになる。

それは「agentic」じゃない。それはベビーシッターだ。

Tetora の dispatch システムを使えば、作業をキューに積んで特定のエージェントに任せ、あとは放置できる。各エージェントがタスクを受け取り、完了後に次のステップへ通知する。

## Dispatch の仕組み

dispatch の核心はタスクキューだ。タスクの仕様（誰が何をするか、どんなコンテキストで）を書いてキューに積めば、指定したエージェントが自動でクレームする。

タスクには3つの必須フィールドがある：

```json
{
  "agent": "kokuyou",
  "task": "MCP セキュリティについての Twitter スレッドを書く",
  "context": {
    "source": "intel/MARKETING-WEEKLY.md",
    "tone": "技術的だが読みやすく"
  }
}
```

これを queue ディレクトリに置けば、エージェントデーモンが次のポーリング（デフォルト30秒）で自動的に拾い上げる。

## はじめての Dispatch

**Step 1 — タスクファイルを定義：**

```bash
cat > tasks/queue/draft-tips-article.json << 'EOF'
{
  "agent": "kohaku",
  "task": "Tetora サイト用に auto-dispatch の tips 記事を書く",
  "context": {
    "output_path": "site/src/content/tips/ja/",
    "word_count": "300-500",
    "include_code_example": true
  }
}
EOF
```

**Step 2 — キューに積む：**

```bash
tetora dispatch push tasks/queue/draft-tips-article.json
```

**Step 3 — 実行を見守る：**

```bash
tetora dispatch status
# kohaku  draft-tips-article  IN_PROGRESS  12秒前に開始
```

以上。ターミナルの受け渡しも、コピペも不要。

## タスクのチェーン

本当の威力は、あるエージェントの出力が次のエージェントの入力になるときに発揮される。`depends_on` でチェーンしよう：

```json
{
  "agent": "spinel",
  "task": "下書き記事のリンクを Discord の #content チャンネルに投稿する",
  "depends_on": "draft-tips-article"
}
```

`draft-tips-article` が完了するまで Spinel は動き出さない。上流タスクが失敗すれば、依存タスクは自動キャンセル——中途半端な投稿は起きない。

## まとめ

Auto-dispatch は「人間がリレー役になる」というボトルネックを取り除く。エージェントを手動で調整するのではなく、ワークフローを一度定義してキューにタスクを積めば、システムが順序通りに実行する。まずはシングルエージェントのキューから始めて、ワークフローが育ったら依存関係を追加していこう。

次のステップ：**Cron でリピートタスクをスケジュールする** を試して、時間トリガーで dispatch を自動化しよう。
