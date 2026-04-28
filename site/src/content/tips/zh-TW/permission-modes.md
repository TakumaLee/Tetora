---
title: "Permission 模式 — 控制 Agent 可以修改什麼"
lang: zh-TW
date: "2026-04-28"
excerpt: "不要讓你的內容 agent 動到基礎設施設定。學習用 Tetora 的三種 permission 模式鎖定 agent 的寫入權限。"
description: "Tetora permission 模式實戰指南——review、plan、acceptEdits——讓每個 agent 只能碰該碰的檔案。"
---

## 問題

你派了一個 agent 去寫部落格文章。它完成了草稿，接著發現你的 `tetora.config.json` 「看起來不一致」，順手改寫了它。現在你的 cron job 全壞了。

沒有邊界的寫入權限，是小小的「好意」變成大事故的原因。

## 三種 Permission 模式

Tetora 提供三種模式，控制 agent 被允許寫入的範圍：

| 模式 | 可讀取 | 可寫入 | 適用情境 |
|---|---|---|---|
| `review` | ✅ 全部 | ❌ 不可 | 稽核、code review、研究 |
| `plan` | ✅ 全部 | ✅ 僅任務規格 | 規劃、開票 |
| `acceptEdits` | ✅ 全部 | ✅ 限 scope 內 | 實際實作 |

在 `tetora.config.json` 設定每個 agent 的預設模式：

```json
{
  "agents": {
    "kohaku": {
      "permission_mode": "acceptEdits",
      "scope": ["site/src/content/**"]
    },
    "hisui": {
      "permission_mode": "review",
      "scope": ["**/*"]
    },
    "tekkou": {
      "permission_mode": "acceptEdits",
      "scope": ["src/**", "tests/**"]
    }
  }
}
```

`kohaku` 只能寫 `site/src/content/` 以下的內容——其他地方完全不行。`hisui` 在任何地方都是唯讀。`tekkou` 負責 source 和 test 目錄。

## 單次 Dispatch 覆寫

有時候你想為某個任務臨時調整 agent 的權限，不想改設定檔：

```bash
# 臨時提升 hisui 的權限做一次性修復
tetora dispatch --agent hisui --permission acceptEdits --scope "docs/**" \
  "修復所有文件中的失效連結"

# 鎖定 tekkou 為 review 模式做敏感稽核
tetora dispatch --agent tekkou --permission review \
  "稽核 database migration 檔案的正確性"
```

覆寫只對這次 dispatch 有效。下一個任務仍使用設定檔中的預設值。

## Scope 邊界強制執行

當 `acceptEdits` 的 agent 嘗試寫入宣告 `scope` 以外的位置時，Tetora 會阻擋寫入並記錄為 scope violation，而不是靜默失敗：

```
[SCOPE VIOLATION] tekkou attempted write to site/src/content/tips/
  Allowed: src/**, tests/**
  Action: BLOCKED — logged to tasks/scope-violations.log
```

沒有靜默漂移，沒有事後追查。

## 核心重點

給 agent 最窄的、它真正需要的 permission。內容 agent 不需要 production 資料庫存取權。Code reviewer 不需要 commit 權限。在 `tetora.config.json` 設定一次，就能預防一整類「agent 到底動了什麼？」的事故。

下一篇：參閱 **每個角色的成本治理**，把同樣的原則套用到模型費用上。
