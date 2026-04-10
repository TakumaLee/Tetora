---
title: "用 Cron 排程定期任務——讓 Agent 工作流程自動在時間點觸發"
lang: zh-TW
date: "2026-04-09"
excerpt: "不用再每天早上手動觸發 agent。用 Tetora 的 cron 排程器，設定時間自動執行定期任務。"
description: "學習如何用 cron 語法在 Tetora 中排程定期 agent 任務。設定每日報告、每週摘要、時間觸發 dispatch，無需任何手動操作。"
---

## 問題在哪

有些任務應該「自己發生」——每日市場掃描、每週內容摘要、每小時健康檢查。每天手動 dispatch 是不必要的摩擦，而且你遲早會忘。

Tetora 的 cron 排程器讓你把時間觸發器綁到任何 dispatch 任務上。daemon 到點自動喚起，不需要盯著看。

## Cron 排程怎麼運作

Tetora 底層使用標準 cron 表達式來定義任務執行時間。你把 cron 字串和任務 spec 配對，排程器負責剩下的事。

```yaml
# .tetora/crons/daily-market-scan.yaml
schedule: "0 8 * * 1-5"   # 08:00，週一到週五
agent: midori
task: "執行每日市場掃描並將結果發到 Discord #money-lab"
context:
  output_channel: "discord:money-lab"
  markets: ["tw.main", "polymarket"]
enabled: true
```

把這個檔案放到 `.tetora/crons/`，排程器在下一次 reload 時（或收到 `SIGHUP` 後立即）就會載入。

## 設定你的第一個 Cron 任務

**Step 1 — 寫 cron spec：**

```bash
mkdir -p .tetora/crons

cat > .tetora/crons/weekly-content-summary.yaml << 'EOF'
schedule: "0 9 * * 1"    # 每週一 09:00
agent: kohaku
task: "摘要上週發布的內容，並建議本週 3 個主題"
context:
  source_dir: "site/src/content/tips"
  output: "drafts/weekly-summary.md"
enabled: true
EOF
```

**Step 2 — Reload 排程器：**

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

## Cron 表達式速查

| 表達式 | 意義 |
|---|---|
| `0 8 * * 1-5` | 平日 08:00 |
| `0 9 * * 1` | 每週一 09:00 |
| `*/30 * * * *` | 每 30 分鐘 |
| `0 0 * * *` | 每天午夜 |
| `0 12 1 * *` | 每月 1 日中午 |

不確定表達式對不對？用 [crontab.guru](https://crontab.guru) 驗證後再 commit。

## 不刪除直接停用

在 spec 設 `enabled: false` 即可暫停 cron，不需要刪檔案：

```yaml
enabled: false   # 暫停，之後要恢復直接改回 true
```

改完記得執行 `tetora cron reload`。

## 重點整理

Cron 排程把一次性 dispatch 變成可靠的自動化工作流。從一個每日任務開始——市場掃描、內容檢查、或是報告——看著它自己跑。穩定後再疊加更多。目標是：agent 團隊在你上班前就已經開始工作了。

上一篇：**Auto-Dispatch 基礎**——手動排入任務佇列。現在有了 cron，佇列自己填滿。
