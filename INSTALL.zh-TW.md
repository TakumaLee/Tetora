# 安裝 Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <strong>繁體中文</strong> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.it.md">Italiano</a> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## 系統需求

| 需求 | 說明 |
|---|---|
| **作業系統** | macOS、Linux 或 Windows（WSL） |
| **終端機** | 任何終端機均可 |
| **sqlite3** | 需在 `PATH` 中可執行 |
| **AI 供應商** | 見下方路徑 1 或路徑 2 |

### 安裝 sqlite3

**macOS：**
```bash
brew install sqlite3
```

**Ubuntu / Debian：**
```bash
sudo apt install sqlite3
```

**Fedora / RHEL：**
```bash
sudo dnf install sqlite
```

**Windows（WSL）：** 在 WSL 發行版中使用上方 Linux 指令安裝。

---

## 下載 Tetora

前往 [Releases 頁面](https://github.com/TakumaLee/Tetora/releases/latest) 下載對應平台的執行檔：

| 平台 | 檔案名稱 |
|---|---|
| macOS（Apple Silicon） | `tetora-darwin-arm64` |
| macOS（Intel） | `tetora-darwin-amd64` |
| Linux（x86_64） | `tetora-linux-amd64` |
| Linux（ARM64） | `tetora-linux-arm64` |
| Windows（WSL） | 在 WSL 中使用 Linux 版本 |

**安裝執行檔：**
```bash
# 將檔案名稱替換為你下載的版本
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# 確認 ~/.tetora/bin 已加入 PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # 或 ~/.bashrc
source ~/.zshrc
```

**或使用一鍵安裝指令（macOS / Linux）：**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## 路徑 1：Claude Pro（月費 $20）— 新手推薦

此路徑使用 **Claude Code CLI** 作為 AI 後端，需要有效的 Claude Pro 訂閱（月費 $20，[claude.ai](https://claude.ai)）。

> **為何選擇此路徑？** 不需管理 API 金鑰，不用擔心用量計費。Pro 訂閱涵蓋透過 Claude Code 使用 Tetora 的所有費用。

> [!IMPORTANT]
> **前提條件：** 此路徑需要有效的 Claude Pro 訂閱（月費 $20）。尚未訂閱的話，請先前往 [claude.ai/upgrade](https://claude.ai/upgrade)。

### 步驟 1：安裝 Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

如果尚未安裝 Node.js：
- **macOS：** `brew install node`
- **Linux：** `sudo apt install nodejs npm`（Ubuntu/Debian）

確認安裝成功：
```bash
claude --version
```

使用 Claude Pro 帳號登入：
```bash
claude
# 依照瀏覽器登入流程操作
```

### 步驟 2：執行 tetora init

```bash
tetora init
```

設定精靈會引導你完成以下步驟：
1. **選擇語言** — 選擇你偏好的語言
2. **選擇訊息頻道** — Telegram、Discord、Slack 或無（僅 HTTP API）
3. **選擇 AI 供應商** — 選擇 **「Claude Code CLI」**
   - 精靈會自動偵測 `claude` 執行檔位置
   - 按 Enter 接受偵測到的路徑
4. **選擇目錄存取權限** — Tetora 可讀寫的資料夾
5. **建立第一個 Agent 角色** — 設定名稱與個性

### 步驟 3：驗證並啟動

```bash
# 確認設定正確
tetora doctor

# 啟動常駐程式
tetora serve
```

開啟網頁儀表板：
```bash
tetora dashboard
```

---

## 路徑 2：API 金鑰

此路徑直接使用 API 金鑰，支援以下供應商：

- **Claude API**（Anthropic）— [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **任何 OpenAI 相容端點** — Ollama、LM Studio、Azure OpenAI 等

> **費用提醒：** API 使用量按 token 計費，啟用前請確認供應商的定價方式。

### 步驟 1：取得 API 金鑰

**Claude API：**
1. 前往 [console.anthropic.com](https://console.anthropic.com)
2. 建立帳號或登入
3. 前往 **API Keys** → **Create Key**
4. 複製金鑰（格式：`sk-ant-...`）

**OpenAI：**
1. 前往 [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. 點選 **Create new secret key**
3. 複製金鑰（格式：`sk-...`）

**OpenAI 相容端點（如 Ollama）：**
```bash
# 啟動本機 Ollama 伺服器
ollama serve
# 預設端點：http://localhost:11434/v1
# 本機模型不需要 API 金鑰
```

### 步驟 2：執行 tetora init

```bash
tetora init
```

設定精靈會引導你：
1. **選擇語言**
2. **選擇訊息頻道**
3. **選擇 AI 供應商：**
   - 選擇 **「Claude API Key」** 使用 Anthropic Claude
   - 選擇 **「OpenAI 相容端點」** 使用 OpenAI 或本機模型
4. **輸入 API 金鑰**（或本機模型的端點 URL）
5. **選擇目錄存取權限**
6. **建立第一個 Agent 角色**

### 步驟 3：驗證並啟動

```bash
tetora doctor
tetora serve
```

---

## 網頁安裝精靈（非工程師）

如果偏好圖形化設定界面，可使用網頁精靈：

```bash
tetora setup --web
```

這會在 `http://localhost:7474` 開啟瀏覽器，顯示 4 步安裝精靈。

---

## 安裝後常用指令

| 指令 | 說明 |
|---|---|
| `tetora doctor` | 健康檢查 — 有問題時先執行這個 |
| `tetora serve` | 啟動常駐程式（Bot + HTTP API + 排程任務） |
| `tetora dashboard` | 開啟網頁儀表板 |
| `tetora status` | 快速查看狀態 |
| `tetora init` | 重新執行設定精靈 |

---

## 疑難排解

### `tetora: command not found`

確認 `~/.tetora/bin` 已加入 PATH：
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

依照上方「系統需求」安裝 sqlite3。

### `tetora doctor` 回報供應商錯誤

- **Claude Code CLI 路徑：** 執行 `which claude` 並更新 `~/.tetora/config.json` 中的 `claudePath`
- **API 金鑰無效：** 在供應商控制台確認金鑰是否正確
- **找不到模型：** 確認模型名稱與你的訂閱方案一致

### Claude Code 登入問題

```bash
claude logout
claude
```

### 執行檔權限被拒

```bash
chmod +x ~/.tetora/bin/tetora
```

---

## 從原始碼編譯

需要 Go 1.25+：

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## 後續步驟

- 閱讀 [README](README.zh-TW.md) 了解完整功能說明
- 社群討論：[github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- 回報問題：[github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
