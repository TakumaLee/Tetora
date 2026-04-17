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

| Agent | Role |
|-------|------|
| arya | 艾麗婭·史塔克 — 刺客 / code surgeon |
| bran | 布蘭 — 資料分析、log 調查、系統健康監控 |
| daenerys | 丹妮莉絲 |
| jon | 瓊恩·雪諾 |
| sansa | 珊莎·史塔克 |
| stannis | 史坦尼斯 |
| tyrion | 提利昂·蘭尼斯特 — workflow dispatcher |
| varys | 瓦里斯 — 需求分析師 |

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
