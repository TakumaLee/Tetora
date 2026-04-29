# Tetora 本地化配置指南

## 📊 配置架構

Tetora 使用**三層配置架構**：

```
config.json              ← 共用配置（進入 git，團隊共享）
config.local.json        ← 本地配置（gitignore，個人/客戶特定）
config.<client>.json     ← 多租戶配置（可選，per-client）
```

### 合併機制

- **深層合併**：`config.local.json` 會遞迴合併到 `config.json`
- **local 優先**：local 的值覆蓋 base config
- **Hot-reload**：SIGHUP 信號會重新載入 local config
- **Git 安全**：`config.local.json` 已在 `.gitignore` 中

## 📁 配置分類

### 應該放在 `config.json`（進入 git）

| 配置類型 | 範例 |
|---------|------|
| Agent 定義 | `agents.*.soulFile`, `agents.*.description` |
| Agent 行為 | `agents.*.permissionMode`, `agents.*.toolPolicy` |
| MCP 配置 | `mcpConfigs.*` |
| Smart Dispatch | `smartDispatch.bindings`, `smartDispatch.rules` |
| Cron Jobs | `jobs` 定義 |
| 系統參數 | `maxConcurrent`, `defaultTimeout` |

### 必須放在 `config.local.json`（不進入 git）

| 配置類型 | 範例 | 原因 |
|---------|------|------|
| **認證金鑰** | `apiToken`, `encryptionKey`, `providers.*.apiKey` | 安全風險 |
| **Bot Tokens** | `telegram.botToken`, `discord.botToken` | 安全風險 |
| **OAuth 配置** | `oauth.services.*.clientSecret` | 安全風險 |
| **本地路徑** | `claudePath`, `historyDB`, `allowedDirs` | 環境依賴 |
| **監聽位址** | `listenAddr` | 部署環境不同 |
| **客戶特定** | `defaultProvider`, `defaultModel`, `agents.*.provider` | 客戶需求不同 |

## 🚀 新 Client Onboarding 流程

### 步驟 1：建立客戶配置目錄

```bash
# 建立客戶專屬目錄
mkdir -p ~/.tetora/clients/<client-id>
```

### 步驟 2：生成基礎配置

```bash
# 複製範本
cp config.local.example.json config.local.json

# 或使用互動式引導（開發中）
tetora onboard --client-id <client-id>
```

### 步驟 3：配置必要欄位

#### 3.1 基礎設施（必須）

```json
{
  "apiToken": "生成隨機 token（openssl rand -hex 32）",
  "encryptionKey": "生成隨機 key（openssl rand -hex 32）",
  "claudePath": "claude 執行檔路徑（which claude）",
  "listenAddr": "localhost:8080",
  "historyDB": "~/.tetora/history.db"
}
```

#### 3.2 AI Provider（必須）

```json
{
  "defaultProvider": "qwen",
  "defaultModel": "auto",
  "providers": {
    "qwen": {
      "apiKey": "your-qwen-api-key"
    },
    "google": {
      "apiKey": "your-google-api-key"
    }
  }
}
```

#### 3.3 通訊平台（依需求）

```json
{
  "telegram": {
    "botToken": "from @BotFather",
    "chatID": 123456789
  },
  "discord": {
    "botToken": "from Discord Developer Portal",
    "publicKey": "from Discord Developer Portal"
  }
}
```

#### 3.4 Agent 配置（依需求）

```json
{
  "agents": {
    "coder": {
      "provider": "qwen",
      "model": "auto",
      "allowedDirs": ["~/projects"]
    }
  }
}
```

### 步驟 4：設定檔案權限

```bash
# 確保敏感檔案權限正確
chmod 600 config.local.json
chmod 700 ~/.tetora/
```

### 步驟 5：驗證配置

```bash
# 驗證配置是否正確
tetora health

# 檢查 provider 連接
tetora provider status
```

## 🌍 環境變數支援（可選）

Tetora 支援從環境變數讀取敏感配置：

```bash
# 在 ~/.bashrc 或 ~/.zshrc 中設置
export TETORA_API_TOKEN="your-token"
export TETORA_TELEGRAM_BOT_TOKEN="your-bot-token"
export TETORA_PROVIDER="qwen"
export TETORA_MODEL="auto"
```

然後在 `config.local.json` 中使用：

```json
{
  "apiToken": "$TETORA_API_TOKEN",
  "telegram": {
    "botToken": "$TETORA_TELEGRAM_BOT_TOKEN"
  }
}
```

## 📋 常見配置場景

### 場景 1：本地開發

```json
{
  "listenAddr": "localhost:8080",
  "claudePath": "/opt/homebrew/bin/claude",
  "historyDB": "~/.tetora/history-dev.db",
  "defaultProvider": "qwen",
  "providers": {
    "qwen": {
      "apiKey": "dev-api-key"
    }
  }
}
```

### 場景 2：生產環境

```json
{
  "listenAddr": "0.0.0.0:8080",
  "claudePath": "/usr/local/bin/claude",
  "historyDB": "/var/lib/tetora/history.db",
  "apiToken": "production-token",
  "encryptionKey": "production-key",
  "defaultProvider": "claude-code",
  "providers": {
    "claude-code": {
      "apiKey": "production-api-key"
    }
  },
  "tls": {
    "certFile": "/etc/ssl/certs/tetora.crt",
    "keyFile": "/etc/ssl/private/tetora.key"
  }
}
```

### 場景 3：多租戶環境

```json
{
  "defaultClientID": "client-a",
  "clientsDir": "~/.tetora/clients",
  "clients": {
    "client-a": {
      "defaultProvider": "qwen",
      "providers": {
        "qwen": {
          "apiKey": "client-a-qwen-key"
        }
      }
    },
    "client-b": {
      "defaultProvider": "google",
      "providers": {
        "google": {
          "apiKey": "client-b-google-key"
        }
      }
    }
  }
}
```

## 🔧 故障排除

### Q: config.local.json 沒有生效？

A: 檢查以下幾點：
1. 檔案是否存在：`ls -la config.local.json`
2. JSON 格式是否正確：`cat config.local.json | jq .`
3. 查看日誌確認載入：`tetora logs | grep "loaded local config"`

### Q: 如何熱重載配置？

A: 發送 SIGHUP 信號：
```bash
kill -HUP $(pgrep -f "tetora serve")
```

### Q: 如何驗證配置合併結果？

A: 運行：
```bash
tetora config show
```

## 📚 參考資源

- 配置範本：`config.local.example.json`
- 本地工作流：`GIT_WORKFLOW.md`
- 提供商切換：`docs/PROVIDER_SWITCH_GUIDE.md`

---

> — **小喬** 🎵
