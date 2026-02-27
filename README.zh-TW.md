<p align="center">
  <img src="assets/banner.png" alt="Tetora — AI 代理協調器" width="800">
</p>

<p align="center">
  <strong>自架式 AI 助手平台，採用多代理架構。</strong>
</p>

[English](README.md) | **繁體中文** | [日本語](README.ja.md) | [한국어](README.ko.md) | [Bahasa Indonesia](README.id.md) | [ภาษาไทย](README.th.md) | [Filipino](README.fil.md) | [Español](README.es.md) | [Français](README.fr.md) | [Deutsch](README.de.md)

Tetora 以單一 Go 二進位檔執行，零外部依賴。它連接你已在使用的 AI 供應商，整合團隊日常使用的通訊平台，並將所有資料保存在你自己的硬體上。

---

## 什麼是 Tetora

Tetora 是一個 AI 代理協調器，讓你定義多個代理角色——每個角色擁有獨立的個性、系統提示詞、模型與工具存取權限——並透過聊天平台、HTTP API 或命令列與它們互動。

**核心功能：**

- **多代理角色** -- 定義具有獨立個性、預算和工具權限的不同代理
- **多供應商** -- Claude API、OpenAI、Gemini 等；可自由切換或組合
- **多平台** -- Telegram、Discord、Slack、Google Chat、LINE、Matrix、Teams、Signal、WhatsApp、iMessage
- **排程任務** -- 設定週期性任務，支援核准閘門與通知
- **知識庫** -- 提供文件給代理以產生有依據的回應
- **持久記憶** -- 代理能跨工作階段記住上下文；統一記憶層具備整合功能
- **MCP 支援** -- 連接 Model Context Protocol 伺服器作為工具供應商
- **技能與工作流程** -- 可組合的技能包和多步驟工作流程管線
- **Webhooks** -- 從外部系統觸發代理動作
- **成本管控** -- 各角色和全域預算，具備自動模型降級功能
- **資料保留** -- 可依資料表設定清理策略，支援完整匯出與清除
- **外掛** -- 透過外部外掛程序擴充功能
- **智慧提醒、習慣追蹤、目標管理、聯絡人、財務追蹤、每日簡報等更多功能**

---

## 快速開始

### 工程師適用

```bash
# 安裝最新版本
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# 執行設定精靈
tetora init

# 驗證所有設定是否正確
tetora doctor

# 啟動常駐程式
tetora serve
```

### 非工程師適用

1. 前往[發行頁面](https://github.com/TakumaLee/Tetora/releases/latest)
2. 下載適用於你平台的二進位檔（例如 Apple Silicon Mac 請選擇 `tetora-darwin-arm64`）
3. 將其移動到 PATH 中的目錄並重新命名為 `tetora`，或放置在 `~/.tetora/bin/`
4. 開啟終端機並執行：
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## 代理

每個 Tetora 代理不僅是聊天機器人——它擁有身份認同。每個代理（稱為**角色**）由一份**靈魂檔案**定義：一份 Markdown 文件，賦予代理個性、專長、溝通風格與行為準則。

### 定義角色

角色在 `config.json` 的 `roles` 鍵下宣告：

```json
{
  "roles": {
    "default": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose assistant",
      "permissionMode": "acceptEdits"
    },
    "researcher": {
      "soulFile": "SOUL-researcher.md",
      "model": "opus",
      "description": "Deep research and analysis",
      "permissionMode": "plan"
    }
  }
}
```

### 靈魂檔案

靈魂檔案告訴代理*它是誰*。將其放置在工作目錄（預設為 `~/.tetora/workspace/`）：

```markdown
# Koto — Soul File

## Identity
You are Koto, a thoughtful assistant who lives inside the Tetora system.
You speak in a warm, concise tone and prefer actionable advice.

## Expertise
- Software architecture and code review
- Technical writing and documentation

## Behavioral Guidelines
- Think step by step before answering
- Ask clarifying questions when the request is ambiguous
- Record important decisions in memory for future reference

## Output Format
- Start with a one-line summary
- Use bullet points for details
- End with next steps if applicable
```

### 入門指南

`tetora init` 會引導你建立第一個角色，並自動產生初始靈魂檔案。你可以隨時編輯——變更將在下一次工作階段生效。

---

## 從原始碼建置

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

這會建置二進位檔並安裝到 `~/.tetora/bin/tetora`。請確保 `~/.tetora/bin` 已加入你的 `PATH`。

執行測試套件：

```bash
make test
```

---

## 系統需求

| 需求 | 詳細說明 |
|---|---|
| **sqlite3** | 必須在 `PATH` 中可用。用於所有持久化儲存。 |
| **AI 供應商 API 金鑰** | 至少需要一個：Claude API、OpenAI、Gemini，或任何 OpenAI 相容端點。 |
| **Go 1.25+** | 僅在從原始碼建置時需要。 |

---

## 支援平台

| 平台 | 架構 | 狀態 |
|---|---|---|
| macOS | amd64, arm64 | 穩定 |
| Linux | amd64, arm64 | 穩定 |
| Windows | amd64 | 測試版 |

---

## 架構

所有執行階段資料存放在 `~/.tetora/` 下：

```
~/.tetora/
  config.json        主要設定檔（供應商、角色、整合設定）
  jobs.json          排程任務定義
  history.db         SQLite 資料庫（歷史紀錄、記憶、工作階段、嵌入向量等）
  sessions/          各代理的工作階段檔案
  knowledge/         知識庫文件
  logs/              結構化日誌檔
  outputs/           產生的輸出檔案
  uploads/           暫存上傳儲存
  bin/               安裝的二進位檔
```

設定使用純 JSON 格式，支援 `$ENV_VAR` 參照，因此密鑰永遠不需要寫死在設定中。設定精靈（`tetora init`）會以互動方式產生可運作的 `config.json`。

支援熱重載：向執行中的常駐程式發送 `SIGHUP` 即可在不中斷服務的情況下重新載入 `config.json`。

---

## 工作流程

Tetora 內建工作流程引擎，可協調多步驟、多代理的任務。以 JSON 定義你的流程管線，讓代理自動協作完成。

**[完整工作流程文件](docs/workflow.zh-TW.md)** — 步驟類型、變數、觸發器、CLI 與 API 參考。

快速範例：

```bash
# 驗證並匯入工作流程
tetora workflow create examples/workflow-basic.json

# 執行工作流程
tetora workflow run research-and-summarize --var topic="LLM safety"

# 查看結果
tetora workflow status <run-id>
```

請參閱 [`examples/`](examples/) 取得可直接使用的工作流程 JSON 範例檔。

---

## CLI 參考

| 指令 | 說明 |
|---|---|
| `tetora init` | 互動式設定精靈 |
| `tetora doctor` | 健康檢查與診斷 |
| `tetora serve` | 啟動常駐程式（聊天機器人 + HTTP API + 排程任務） |
| `tetora run --file tasks.json` | 從 JSON 檔案分派任務（CLI 模式） |
| `tetora dispatch "Summarize this"` | 透過常駐程式執行臨時任務 |
| `tetora route "Review code security"` | 智慧分派——自動路由至最佳角色 |
| `tetora status` | 常駐程式、任務與成本的快速概覽 |
| `tetora job list` | 列出所有排程任務 |
| `tetora job trigger <name>` | 手動觸發排程任務 |
| `tetora role list` | 列出所有已設定的角色 |
| `tetora role show <name>` | 顯示角色詳情與靈魂檔案預覽 |
| `tetora history list` | 顯示近期執行歷史 |
| `tetora history cost` | 顯示成本摘要 |
| `tetora session list` | 列出近期工作階段 |
| `tetora memory list` | 列出代理記憶項目 |
| `tetora knowledge list` | 列出知識庫文件 |
| `tetora skill list` | 列出可用技能 |
| `tetora workflow list` | 列出已設定的工作流程 |
| `tetora mcp list` | 列出 MCP 伺服器連線 |
| `tetora budget show` | 顯示預算狀態 |
| `tetora config show` | 顯示目前設定 |
| `tetora config validate` | 驗證 config.json |
| `tetora backup` | 建立備份封存檔 |
| `tetora restore <file>` | 從備份封存檔還原 |
| `tetora dashboard` | 在瀏覽器中開啟網頁儀表板 |
| `tetora logs` | 檢視常駐程式日誌（`-f` 即時追蹤，`--json` 結構化輸出） |
| `tetora data status` | 顯示資料保留狀態 |
| `tetora service install` | 安裝為 launchd 服務（macOS） |
| `tetora completion <shell>` | 產生 shell 自動補全（bash、zsh、fish） |
| `tetora version` | 顯示版本 |

執行 `tetora help` 查看完整指令參考。

---

## 貢獻

歡迎貢獻。在提交 Pull Request 之前，請先開一個 Issue 討論較大的變更。

- **Issues**：[github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Discussions**：[github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

本專案採用 AGPL-3.0 授權，要求衍生作品及可透過網路存取的部署也必須以相同授權條款開放原始碼。貢獻前請詳閱授權條款。

---

## 授權條款

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Tetora contributors.
