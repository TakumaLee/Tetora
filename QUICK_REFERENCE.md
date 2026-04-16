# 動態提供商切換 - 快速參考

## 🎯 核心命令

```bash
# 切換提供商
tetora provider set qwen                    # 使用預設模型
tetora provider set google gemini-2.5-pro   # 指定模型
tetora provider set qwen auto               # 自動選擇

# 查看狀態
tetora provider status                      # 當前活躍提供商
tetora provider list                        # 所有配置的提供商

# 清除覆蓋
tetora provider clear                       # 返回 Agent 配置
```

## 📊 優先級鏈

```
Task Provider → Active Provider → Agent Provider → Global Default → Fallback
```

## 🔧 Agent 配置範例

```json
{
  "agents": {
    "coder": {
      "provider": "auto",
      "model": "auto"
    }
  },
  "defaultProvider": "qwen",
  "fallbackProviders": ["google", "claude"]
}
```

## 📋 預設參數

| 提供商 | 預設模型 | MaxTokens | Temperature |
|--------|----------|-----------|-------------|
| qwen | qwen3.6-plus | 8192 | 0.7 |
| google | gemini-2.5-pro | 65536 | 0.6 |
| claude | claude-sonnet-4 | 8192 | 0.7 |
| groq | llama-3.3-70b | 8192 | 0.7 |

## 💡 使用場景

### 場景 1: 快速切換
```bash
tetora provider set google  # Qwen 報錯時
```

### 場景 2: 測試比較
```bash
tetora provider set qwen && tetora dispatch "測試任務"
tetora provider set google && tetora dispatch "測試任務"
tetora provider set claude && tetora dispatch "測試任務"
```

### 場景 3: 生產環境
```bash
# 設置穩定提供商 + 降級列表
tetora provider set claude
# config.json: "fallbackProviders": ["qwen", "google"]
```

## 📁 檔案位置

- 狀態檔：`~/.tetora/runtime/active-provider.json`
- 配置檔：`~/.tetora/config.json`
- 預設程式碼：`internal/provider/provider_profiles.go`

## ✅ 測試

```bash
# 運行所有測試
go test ./internal/config ./internal/provider -v

# 運行整合測試
go test -run TestProvider . -v
```

## 🔍 疑難排解

**Q: 設置後不生效？**
```bash
tetora provider status                    # 檢查是否設置
tetora logs | grep provider               # 查看日誌
```

**Q: 如何知道使用的提供商？**
```bash
tetora provider status                    # 查看 Active Provider
tetora dispatch "test" --verbose          # 查看實際使用的提供商
```

**Q: 清除覆蓋？**
```bash
tetora provider clear
```
