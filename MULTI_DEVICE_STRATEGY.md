# Tetora 多機協同架構指南 (Multi-Device Strategy)

## 📊 架構圖

```
      ┌──────────────────────────────────────────────────────────────┐
      │                     GitHub (ymow/Tetora)                     │
      │                           origin                             │
      │                       develop (分支)                          │
      └──────────────┬───────────────────────────────────┬───────────┘
                     │ Pull / Push                       │ Pull / Push
    ┌────────────────▼────────────────┐      ┌───────────▼─────────────────┐
    │        💻 電腦 A (MacBook)      │      │      💻 電腦 B (Mac Pro)    │
    │                                 │      │                             │
    │  Branch: develop                │      │  Branch: develop            │
    │  Role: 開發 + 輕量任務          │      │  Role: 重度運算 / 特定Agents │
    │                                 │      │                             │
    │  config.local.json:             │      │  config.local.json:         │
    │  - agents: [writer, coder]      │      │  - agents: [analyst, dev]   │
    │  - claudePath: /opt/...         │      │  - claudePath: /usr/...     │
    │  - listenAddr: localhost:8081   │      │  - listenAddr: localhost:8082│
    └─────────────────────────────────┘      └─────────────────────────────┘
```

## 🔄 核心原則

### 1. 程式碼同步 (`develop` 分支)
- 所有 Tetora 的程式碼更新、Bug 修復、功能新增都推送到 `origin/develop`。
- 任何一台電腦開發完新功能，`git push` 後，其他電腦 `git pull` 即可獲得更新。

### 2. 配置隔離 (`config.local.json`)
- **絕不提交 `config.local.json`**（已在 `.gitignore` 中）。
- 每台電腦根據自己的硬體規格和職責，擁有獨立的配置：
    - **電腦 A**: 負責文書類 Agents，使用較省資源的 Provider。
    - **電腦 B**: 負責重度編譯/分析 Agents，使用高階 GPU 或高配 Provider。
    - **路徑差異**: 每台電腦的 `claudePath`、`historyDB` 路徑可能不同。

### 3. 狀態隔離 (`~/.tetora/`)
- 運行時狀態（Active Provider, DB, Logs）完全保存在本地，不與 Git 同步，避免多機衝突。

## 🚀 標準工作流

### 場景 1: 在一台電腦修復 Tetora Bug
1. 在 **電腦 A** 修改程式碼 (`fix/bug-fix`).
2. 合併到 `develop` 並推送: `git push origin develop`.
3. 在 **電腦 B** 拉取更新:
   ```bash
   git checkout develop
   git pull origin develop
   # 重啟 Daemon 或 HUP 熱重載
   kill -HUP $(pgrep -f "tetora serve")
   ```

### 場景 2: 新增專屬該電腦的 Agent
1. 在 **電腦 B** 編輯 `config.local.json`.
2. 加入新 Agent 設定 (例如: `agent: "video-processor"`).
3. **不需要 commit**，只要重啟服務，該電腦即可處理新任務。
4. 其他電腦因無此配置，不會受到影響（保持純淨）。

### 場景 3: 同步通用的 Agent 設定 (SoulFile / Prompts)
如果某個 Agent 的「靈魂 (SoulFile)」或「Prompt」是通用的，希望所有電腦都有一樣的設定：
1. 將 SoulFile 放入 Tetora Repo 的共用目錄 (例如 `soulfiles/common/`).
2. 在 `config.json` (非 local) 中定義 Agent 結構。
3. Commit 並 Push，所有電腦 Pull 後即擁有相同的 Agent 邏輯，但由各自的 `config.local.json` 決定是否啟用該 Agent。

## ⚙️ 配置建議範例

### 電腦 A (`config.local.json`)
```json
{
  "listenAddr": "localhost:8080",
  "agents": {
    "writer": { "provider": "gemini" },
    "researcher": { "provider": "claude-3-haiku" }
  }
}
```

### 電腦 B (`config.local.json`)
```json
{
  "listenAddr": "localhost:8081", 
  "claudePath": "/opt/homebrew/bin/claude",
  "agents": {
    "architect": { "provider": "claude-3-opus" },
    "compiler": { "provider": "claude-3-sonnet" }
  }
}
```

## ⚠️ 注意事項

- **避免同時修改程式碼**: 兩台電腦不要同時修改同一個 `.go` 檔案並 Push，以免產生 Git 衝突 (Merge Conflicts)。
- **統一 API Keys**: 如果多台電腦共用同一組 LLM API Key，請注意 **Rate Limit** 和 **總預算** 會被加總。
- **Host 綁定**: 如果有外部服務 (如 Discord/Telegram Webhook) 指向特定電腦，需確保 Network 設定 (如 ngrok) 正確轉發到該電腦的 Port。
