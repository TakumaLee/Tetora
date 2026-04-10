---
title: "Cronで定期タスクをスケジュール — エージェントワークフローを時間起動で自動化"
lang: ja
date: "2026-04-09"
excerpt: "毎朝手動でエージェントを起動するのはやめよう。Tetora の cron スケジューラーを使えば、定期タスクが時間通りに自動実行される。"
description: "Tetora で cron 構文を使って定期エージェントタスクをスケジュールする方法。日次レポート、週次サマリー、時間トリガーのディスパッチを手動作業なしで設定できる。"
---

## 課題

「毎日起きること」はある——日次マーケットスキャン、週次コンテンツサマリー、毎時ヘルスチェック。これを毎日手動でディスパッチするのは不要な摩擦だし、いずれ忘れる。

Tetora の cron スケジューラーを使えば、任意のディスパッチタスクに時間トリガーを紐付けられる。daemon がスケジュール通りに起動するので、張り付く必要はない。

## Cron スケジューリングの仕組み

Tetora は標準的な cron 式を使ってタスクの実行時刻を定義する。cron 文字列とタスク spec をペアにすれば、スケジューラーが残りを処理する。

```yaml
# .tetora/crons/daily-market-scan.yaml
schedule: "0 8 * * 1-5"   # 08:00、月〜金
agent: midori
task: "日次マーケットスキャンを実行し、結果を Discord #money-lab に投稿する"
context:
  output_channel: "discord:money-lab"
  markets: ["tw.main", "polymarket"]
enabled: true
```

このファイルを `.tetora/crons/` に置くだけで、スケジューラーが次の reload 時（または `SIGHUP` 受信後すぐ）に読み込む。

## 最初の Cron タスクを設定する

**Step 1 — cron spec を書く：**

```bash
mkdir -p .tetora/crons

cat > .tetora/crons/weekly-content-summary.yaml << 'EOF'
schedule: "0 9 * * 1"    # 毎週月曜 09:00
agent: kohaku
task: "先週公開されたコンテンツをまとめ、今週の3テーマを提案する"
context:
  source_dir: "site/src/content/tips"
  output: "drafts/weekly-summary.md"
enabled: true
EOF
```

**Step 2 — スケジューラーをリロード：**

```bash
tetora cron reload
# Loaded 1 new cron: weekly-content-summary (next run: Mon 2026-04-13 09:00)
```

**Step 3 — 確認：**

```bash
tetora cron list
# NAME                     SCHEDULE       AGENT    NEXT RUN
# weekly-content-summary   0 9 * * 1      kohaku   2026-04-13 09:00
# daily-market-scan        0 8 * * 1-5    midori   2026-04-10 08:00
```

## Cron 式クイックリファレンス

| 式 | 意味 |
|---|---|
| `0 8 * * 1-5` | 平日 08:00 |
| `0 9 * * 1` | 毎週月曜 09:00 |
| `*/30 * * * *` | 30分ごと |
| `0 0 * * *` | 毎日深夜0時 |
| `0 12 1 * *` | 毎月1日の正午 |

式が正しいか不安なら [crontab.guru](https://crontab.guru) で検証してからコミットしよう。

## 削除せずに無効化する

ファイルを消さずに cron を一時停止したい場合は、spec で `enabled: false` を設定する：

```yaml
enabled: false   # 一時停止 — 再開するときは true に戻すだけ
```

変更後は `tetora cron reload` を忘れずに。

## まとめ

cron スケジューリングは、一回限りのディスパッチを信頼性の高い自動ワークフローに変える。まず1つの日次タスクから始めよう——マーケットスキャン、コンテンツチェック、レポート生成——そして自動で動くのを確認する。安定したら次を重ねていく。目標は「自分が起き上がる前に、エージェントチームがすでに働いている」状態だ。

前回：**Auto-Dispatch 基礎**——手動でキューにタスクを積む。cron があれば、キューは自動で埋まる。
