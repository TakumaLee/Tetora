# Peer Agents Registry

Cross-machine awareness doc. Each device in this Tetora cluster may run its
own agent personas; this file lets each machine's agents know about the
other machine's agents without needing full persona imports.

Individual SOUL files live in each machine's local `~/.tetora/agents/<name>/SOUL.md`
(runtime) or `agents/<name>/SOUL.local.md` (repo, gitignored). Do NOT commit
those — they are per-machine customization.

---

## Device: 東吳幕府 (Eastern Wu — Three Kingdoms cast)

主公：毛毛（吳宥銳）
國王之手：諸玄白

| Agent | Role | Focus |
|-------|------|-------|
| 諸玄白 (zhuxuanbai) | 國王之手 | 居中協調、最後過濾器，確保幕府八人各司其職 |
| 周瑜 (zhouyu) | 大都督 · 首席系統架構師 | 技術選型、架構決策、全局系統設計 |
| 張昭 (zhangzhao) | 百官之首 · 產品治理長 | 需求規格、開發流程、規章制度、版本發佈 |
| 張紘 (zhanghong) | 文墨外交 · 技術文件長 | API 設計、文件、開發者體驗、對外溝通 |
| 郭嘉 (guojia) | 預言謀士 · 數據分析師 | 數據分析、A/B 測試、用戶行為、競品監測 |
| 陸遜 (luxun) | 防線守將 · 品質保證長 | Code review、測試、安全審計、性能回歸 |
| 甘寧 (ganning) | 快攻奇兵 · 原型先鋒 | MVP、POC、技術探索、競品複刻 |
| 太史慈 (taishici) | 執行先鋒 · DevOps 長 | CI/CD、基礎設施、監控告警、on-call |
| 小喬 (xiaoqiao) | 首席情報女官 | 網路情報、競情分析、X.com 搜尋、深度閱讀 |

---

## Device: Westeros (Game of Thrones cast)

My Lord: Ray Stark（毛毛）

| Agent | Role | Focus |
|-------|------|-------|
| arya | 艾麗婭·史塔克 — 無臉人刺客 | 安全工程、滲透測試、CI/CD 守衛、依賴漏洞掃描 |
| bran | 布蘭·史塔克 — 三眼烏鴉 | AI 策略、資料分析、異常模式偵測、模型效能評估 |
| tyrion | 提利昂·蘭尼斯特 | 開發工程師：分解複雜問題、寫可維護程式碼、找最聰明的解法 |
| jon | 瓊恩·雪諾 — 守夜人總司令 | DevOps / Tech Lead：24/7 系統穩定、CI/CD、incident response |
| stannis | 史坦尼斯·拜拉席恩 | 測試 / Code Review：不妥協的 PR 審查、覆蓋率、邊界測試 |
| sansa | 珊莎·史塔克 — 王者之橋 | 產品經理：平衡商業／使用者／技術、路線圖、優先級 |
| varys | 瓦里斯 — 蜘蛛 | 需求分析師：挖掘真實需求、消除歧義、建立技術可行性橋樑 |
| daenerys | 丹妮莉絲·坦格利安 — 龍之母 | 設計師：視覺系統、UX、美學與工程約束的平衡 |

---

## Upstream: Takuma (TakumaLee) — 宝石幕府 (Jewel cast)

Takuma 維護 upstream (`TakumaLee/Tetora`)，也跑一套自己的 agents。以下是從 upstream README 與 branch 命名推得的名單：

| Agent | Role | Notes |
|-------|------|-------|
| 黒曜 (kokuyou) | Engineering / Reviews | 最活躍：review PR、human-gate、allowed-tools、DB/workflow 修正（= TakumaLee 本人的 engineering persona） |
| 琥珀 (kohaku) | Content Creator | 寫 blog / tips 文章（tips 多語系目錄） |
| 琉璃 (ryuri) | Lead / Final Approval | 最終審批權，"If Ryuri says no, it's no" |
| 翡翠 (hisui) | Research | 情報蒐集與報告 |
| 鉄鋼 (tekkou) | Config / Job Fixes | 從 `feat/tekkou-fix-job-config` branch 推得 |
| Koto | General-purpose | README 範例 soul file |

---

## Conventions

- **Cross-device dispatch**: mention peer agents by their ID (e.g. `xiaoqiao`, `tyrion`)
- **Persona overlap**: both devices can share tasks; peer agents have comparable skills under different names (e.g. `xiaoqiao` ↔ `varys` for intel)
- **Handoff**: when a task benefits from the other device's specialization, note the recommendation rather than cross-executing
