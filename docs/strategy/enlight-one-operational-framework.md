# 翔耀一號 (Enlight One AI) 營運框架 v2.1

> **日期**：2026 年 4 月 9 日  
> **主責**：Product Owner & AI Lead  
> **適用範圍**：翔耀實業核心團隊、五方合作夥伴  
> **文件狀態**：定稿執行版

---

## 1. 核心戰略 (Core Strategy)

| 項目 | 定義 |
| :--- | :--- |
| **Vision (願景)** | `Organic growth organization with local autonomous systems.` |
| **Mission (使命)** | `Forge autonomy, drive growth.` |
| **Core Values (價值觀)** | • **Innovation (進化)**: AI 越用越懂產業，系統自動迭代 Prompt 與知識庫。<br>• **Quality (可靠)**: 軍規級穩定，高併發下不崩潰，斷網亦能運行。<br>• **Protection (主權)**: 數據不出境，權限嚴控，把控制權 100% 交還企業。 |
| **產品定位** | 台灣首個「地端主權、自主進化」的中小企業 (SMB) Agent OS。 |

---

## 2. 產品定義 (Product Definition)

### 2.1 階段規劃 (Phased Roadmap)

* **Phase 1: MVP 基礎 Agent 交付 (M1-M3)**
  * **核心交付**：Agent Onboarding 引導 Agent + 會議情報官 (STT/TTS) + 視覺鑑識官 (Qwen-VL)。
  * **技術邊界**：封裝 Ai.Durbun 底層能力為 Application 層，建立基礎 MCP 工具路由與短期對話緩存。
  * **目標**：開箱即用，Time-to-Value < 4 小時，建立 20 家標竿客戶。
* **Phase 2: Agent Store & Digital Twins (M4-M8)**
  * **核心交付**：Agent Store (應用商店)、Digital Twins (數位分身運行環境)、OTA 無感更新。
  * **目標**：跑通 Private/Public Store 雙軌上架與審核邏輯、驗證 Digital Twins 狀態隔離與持久化、建立開發者 SDK 與 MCP 工具生態，實現平台從「單機工具」升級為「AI 應用生態系統」。
* **Phase 3: Agent OS 自治與模型訓練 (M9-M12)**
  * **核心交付**：Agent OS Kernel (完整四層架構)、專屬 LLM/SLM 訓練服務、A2A 跨平台協議。
  * **目標**：系統自主調度多 Agent 競爭，支援高可用叢集 (HA) 部署。

### 2.2 Agent Store 定義 (應用商店機制)
* **Private Store (私有倉庫)**：企業 IT 或合作夥伴開發的內部 Agent。免審核或僅做快速掃描，僅限企業內部可見，保障數據絕對隔離。
* **Public Store (公開市場)**：面向所有用戶的第三方應用市場。**嚴格審核 (Apple Style)**：通過翔耀 AI 團隊的功能與資安審查 (防注入、防越權) 後方可上架，建立信任堡壘。

### 2.3 初始化引導機制 (Agent Onboarding)
* **定義**：客戶開箱 DGX 後，系統自動進入的「對話式引導流程」，確保零技術門檻完成部署。
* **核心功能**：
    1. **硬體自檢 (Health Check)**：自動確認 GPU/VRAM/記憶體/儲存空間狀態。
    2. **基礎配置 (Config)**：一鍵寫入系統變數、設定 MCP 端點、建立管理員帳號。
    3. **產業問卷 (Knowledge Init)**：引導輸入企業產業別與基礎資料，自動生成第一個初始化知識庫與 Prompt。

---

## 3. 技術框架定義 (Agent OS Architecture)

Agent OS 分為四層，對應不同團隊職責：

| 架構層 | 核心職責 | 關鍵技術/組件 | 負責團隊 |
| :--- | :--- | :--- | :--- |
| **A. 內核層 (Kernel)** | **資源調度與推理路由**<br>• Agent Scheduler (FIFO/Priority)<br>• 推理引擎適配器 (自動切換模型)<br>• Context Manager (上下文 Swapping) | Go Scheduler<br>Model Router<br>Context Compression | AI Lead<br>Architect |
| **B. 存儲層 (Memory)** | **多層次記憶與狀態**<br>• 工作記憶 (對話緩存)<br>• 程序記憶 (SOP/操作流程)<br>• 狀態持久化 (Checkpoint) | Milvus Lite<br>Procedural Store<br>Redis / SQLite | AI Lead<br>ML Engineer |
| **C. 接口層 (Abstractions)** | **統一抽象與交互**<br>• HAL (硬體抽象層)<br>• MCP 工具箱標準<br>• UI/UX 抽象層 (Generative UI) | MCP Protocol<br>GenUI Renderer<br>Enlight SDK | Architect<br>Engineer |
| **D. 治理層 (Security)** | **權限與預算閘口**<br>• 權限環 (Privilege Rings)<br>• 沙盒環境 (Docker/WASM)<br>• 預算與審核閘口 (Governance Gate) | Docker / WASM<br>Human-in-loop Gate<br>Audit Log | Architect<br>ML Engineer |
| **系統組件** | **非同步通訊與排程**<br>• 事件總線 (Event Bus)<br>• 全局時鐘 (Global Clock/Cron) | NATS / Redis Stream<br>Distributed Cron | Architect |

---

## 4. 硬體規格與 SLA (Hardware & SLA)

### 4.1 核心載體與效能
* **設備**：NVIDIA DGX Spark (邊緣 AI 節點)
* **規格基準**：24GB VRAM (統一記憶體) | 64GB RAM | NVMe SSD。
* **效能上限 (基於 Qwen-14B-Int4 優化)**：
  * **純文字**：穩定 8~12 路併發
  * **語音進線**：2~3 路併發
  * **視覺鑑識**：1~2 路併發
  > **⚠️ 營運紅線**：超過併發上限自動觸發排隊或降級機制，確保系統「不死機」。

### 4.2 叢集架構 (高可用性)
當客戶部署 2-3 台時，產品定義升級為 **「高可用基礎設施 (HA Infrastructure)」**：
* **架構變動**：引入全局負載均衡 (Global Load Balancer)、共享/複製存儲 (Shared Storage)、統一 API Gateway。
* **商業價值**：SLA 提升至 99.99%。單一節點故障時，另一台自動接管 (Failover)，確保業務不中斷。

---

## 5. 商業模式 (Business Models)

| 模式 | 核心載體 | 營運邏輯 |
| :--- | :--- | :--- |
| **📦 軟體授權 (Software)** | Enlight One OS + Agent Store 資格 | 平台使用授權費、Public Store 上架審核費/分潤。 |
| **💻 銷售整合 (Sales)** | DGX 硬體 + 解決方案包 + Onboarding 服務 | 硬體設備銷售、預載軟體映像檔、產業初始化知識包。Onboarding 取代人工設定。 |
| **🔧 維運服務 (Operations)** | OTA 系統 + Partner Dispatch | OTA 無感更新訂閱費、SLA 保固、故障備機替換、遠端診斷。 |
| **🧠 專屬 LLM/SLM 訓練** | 代客訓練管線 (Managed Training) | **翔耀代客訓練**。企業提供私有資料，翔耀負責數據清洗、微調與驗證，最終將「專屬模型」交付部署至客戶端。 |

---

## 6. 人員編制 (Organization & Staffing)

*5 人核心團隊，全員背負「產品交付」與「營運指標」。*

| 排名 | 角色 | 核心職責 | 關鍵產出 (KPIs) |
| :---: | :--- | :--- | :--- |
| **1** | **AI 部門主管 (AI Lead)** | **智能與程序記憶核心**：Agent 編排、Store 審核、SOP 設計、推理路由策略。 | Agent 解決率、SOP 準確率、Store 審核標準。 |
| **2** | **Senior PM / PO (您)** | **商業與治理規則**：定價框架、Partner SLA、預算閘口規則 (Human-in-loop)、Store 商業機制。 | 續約率、NPS、TTV (Time-to-Value)。 |
| **3** | **系統架構師 (Architect)** | **底層基建與 SecOps**：Agent OS 四層架構、Event Bus、安全運維、OTA 管線。 | OTA 成功率、系統可用性、資安掃描通過率。 |
| **4** | **ML / 模型工程師** | **模型效能與 Data Eng**：量化部署、向量數據清洗與索引、RAG 優化、專屬模型訓練。 | TTFT 優化、VRAM 利用率、微調模型品質。 |
| **5** | **Senior Marketing** | **GTM 策略**：品牌定位、開發者生態招募、銷售工具包、渠道賦能。 | Leads 數、Demo 轉化率、Partner 活躍度。 |

---

## 7. 一年營運計畫 (One-Year Operational Roadmap)

### Q1 (M1-M3): MVP 攻堅與種子驗證 (Phase 1)
* **產品**：完成 Agent Onboarding 流程、Tactical Console v1、會議/視覺 Agent 開發。
* **營運**：簽署四方合作 LOI 與 SLA。完成 10 家種子客戶導入。啟動軟體授權與銷售整合模式。
* **里程碑**：**Time-to-Value < 4 小時**，Onboarding 自動化引導成功率 > 95%。

### Q2 (M4-M6): Agent Store Beta 與開發者生態 (Phase 2 啟動)
* **產品**：Agent Store Beta 上線 (Private 為主)，實作 `.enlight` 封裝標準與基礎權限環。發布 Agent SDK。
* **營運**：啟動維運服務訂閱制。建立首批 ISV/SI 導入測試。
* **里程碑**：**跑通 Private Store 上架與企業內部部署流程**。

### Q3 (M7-M9): Digital Twins 擴容與 MCP 生態 (Phase 2 深化)
* **產品**：Digital Twins 運行環境上線，支援狀態持久化與程序記憶。OTA 無感更新啟用。
* **營運**：群環 Partner Dispatch 正式上線，實現硬體報修自動化。中衛中心批量輔導轉化。
* **里程碑**：**Digital Twins 狀態恢復成功率 > 99%**，MRR 達標，維運續約率 > 90%。

### Q4 (M10-M12): Agent OS 自治與模型訓練服務 (Phase 3)
* **產品**：Agent OS Kernel 完整上線 (含推理路由)。開放「專屬 LLM/SLM 訓練服務」管線。
* **營運**：全面啟動模型訓練商業模式 (代客訓練)。進軍中大型企業，推廣高可用叢集 (HA) 方案。
* **里程碑**：**交付首個企業專屬 SLM/LLM 模型**，ARR 突破預期，實現損益兩平。

---

## 8. 風險管理 (Risk Management)

| 風險類別 | 應對策略 | 負責人 |
| :--- | :--- | :--- |
| **技術瓶頸** | 嚴禁預設運行 30B+ 模型；優先使用 7B/14B 路由分流；超發自動排隊。 | AI Lead / Architect |
| **營運過載** | 嚴格執行 SLA，超線工單自動轉付費升級；不接客製化外包。 | PO / Mkt |
| **硬體故障** | 群環科技 48 小時內備機替換；翔耀提供數據遷移腳本。 | PO / Eng |
| **資安合規** | 資料 100% 地端儲存；Agent 權限沙盒隔離；Strict Public Store 審核。 | Architect / AI Lead |

---
*本文檔是翔耀一號的憲法。所有產品迭代、營運決策、合作簽約均須以此為準繩。*
