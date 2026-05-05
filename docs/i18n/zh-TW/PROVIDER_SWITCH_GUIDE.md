# 動態提供商切換 - 使用指南

## 問題背景

在切換 AI 提供商（如 Qwen → Gemini → Claude）時，使用者需要：
1. 逐個修改每個 Agent 的配置
2. 調整不同提供商的參數（temperature、maxTokens 等）
3. 花費大量時間適配和測試

## 解決方案

實現了 **Session 級別的提供商動態切換**，讓使用者可以：
- ✅ 一條命令切換所有 Agent 的提供商
- ✅ 自動應用最優參數配置
- ✅ 零配置成本測試不同提供商
- ✅ 提供商報錯時自動降級

---

## 快速開始

### 1. 切換到 Qwen

```bash
# 使用 Qwen 預設模型
tetora provider set qwen

# 使用指定模型
tetora provider set qwen qwen-plus

# 自動選擇模型
tetora provider set qwen auto
```

### 2. 切換到 Gemini

```bash
tetora provider set gemini
```

### 3. 切換到 Claude

```bash
tetora provider set claude
```

### 4. 切換到 Codex

```bash
tetora provider set codex
```

### 5. 查看當前提供商

```bash
tetora provider status
```

輸出範例：
```
Active Provider Override:
  Provider: claude
  Model:    auto (use provider default)
  Set at:   2026-04-16 10:30:00
  Set by:   CLI

This override affects ALL agent executions.
Use 'tetora provider clear' to remove this override.
```

### 6. 清除覆蓋，返回 Agent 級別配置

```bash
tetora provider clear
```

### 7. 查看所有配置的提供商

```bash
tetora provider list
```

---

## 工作原理

### 優先級鏈

提供商解析遵循以下優先級：

```
1. Task-level Provider（任務級別覆蓋）          ← 最高優先級
2. Agent-level Provider（明確 pin，非 "auto"） ← 不受全局覆蓋影響
3. Active Provider Override（CLI/API 設置）
4. Global Default Provider（全局預設）
5. Legacy Fallback（向後相容）
```

**Agent 釘選（Pin）：** 若 Agent 在 config 中設置了明確的 `"provider"`（非 `"auto"`），則該 Agent 會被釘選到指定提供商，無法被 `tetora provider set` 覆蓋。適合需要永遠使用特定 CLI 的 Agent（例如只使用 Claude Code 的 Agent）。

### 自動參數優化

每個提供商都有預定義的最優參數配置：

| 提供商 | 預設模型 | MaxTokens | Temperature | Context Window |
|--------|----------|-----------|-------------|----------------|
| Qwen | qwen-plus | 8192 | 0.7 | 131K |
| Gemini | gemini-2.5-pro | 65536 | 0.6 | 1M |
| Claude | claude-sonnet-4 | 8192 | 0.7 | 200K |
| Groq | llama-3.3-70b | 8192 | 0.7 | 131K |

切換提供商時，這些參數會自動應用，無需手動調整。

---

## Agent 配置範例

### 使用 "auto" 模式（跟隨全局覆蓋）

在 `config.json` 中設置 Agent 使用自動提供商：

```json
{
  "agents": {
    "code-reviewer": {
      "provider": "auto",
      "model": "auto",
      "description": "程式碼審查 Agent"
    },
    "writer": {
      "provider": "auto",
      "model": "auto",
      "description": "內容創作 Agent"
    }
  }
}
```

現在只需一條命令即可切換所有 Agent 的提供商：

```bash
tetora provider set claude
# 所有 Agent 都會使用 Claude，無需逐個修改配置
```

### 釘選 Agent 到特定提供商

設置明確的 `"provider"`（非 `"auto"`）可將 Agent 鎖定到指定提供商，全局覆蓋不影響它：

```json
{
  "agents": {
    "takuma": {
      "provider": "claude",
      "model": "auto",
      "description": "永遠使用 Claude Code"
    },
    "analyst": {
      "provider": "auto",
      "description": "跟隨全局提供商設置"
    }
  }
}
```

```bash
tetora provider set gemini
# analyst → gemini（跟隨覆蓋）
# takuma  → claude（已釘選，不受影響）
```

---

## 進階功能

### 1. 故障轉移（Fallback）

即使設置了 Active Provider，仍然會尊重全局的 `fallbackProviders` 配置：

```json
{
  "defaultProvider": "claude",
  "fallbackProviders": ["gemini", "qwen"]
}
```

當 Claude 失敗時，自動嘗試 Gemini → Qwen。

### 2. 斷路器整合

內建斷路器機制，自動檢測提供商故障：
- 連續失敗 5 次後開啟斷路器
- 30 秒後嘗試恢復
- 成功 2 次後關閉斷路器

查看斷路器狀態：
```bash
tetora health
```

### 3. 預設參數模板

系統為每個提供商預定義了最優參數，位於：
```
internal/provider/provider_profiles.go
```

包含：
- 預設模型
- MaxTokens
- Temperature / TopP
- FirstTokenTimeout
- ContextWindow
- 能力特性（工具、串流、視覺）

---

## 實際場景

### 場景 1: Qwen 返回 400 錯誤，快速切換到 Gemini

```bash
# 發現問題：Qwen 報錯
# 快速切換
tetora provider set gemini

# 繼續工作，無需修改任何 Agent 配置
tetora dispatch "分析程式碼庫架構"
```

### 場景 2: 測試不同提供商的效果

```bash
# 測試 Qwen
tetora provider set qwen
tetora dispatch "寫一個排序算法"

# 測試 Gemini
tetora provider set gemini
tetora dispatch "寫一個排序算法"

# 測試 Claude
tetora provider set claude
tetora dispatch "寫一個排序算法"

# 測試 Codex
tetora provider set codex
tetora dispatch "寫一個排序算法"

# 比較結果，選擇最佳提供商
```

### 場景 3: 生產環境使用穩定提供商

```bash
# 設置生產提供商（預設使用 Claude）
tetora provider set claude

# config.json 中設置 fallbackProviders
# "defaultProvider": "claude"
# "fallbackProviders": ["gemini", "qwen"]

# 現在即使主要提供商失敗，系統也會自動降級
```

---

## 檔案位置

Active Provider 狀態儲存在：
```
~/.tetora/runtime/active-provider.json
```

格式：
```json
{
  "providerName": "claude",
  "model": "auto",
  "setAt": "2026-04-16T10:30:00Z",
  "setBy": "CLI"
}
```

---

## 疑難排解

### Q: `provider set` 拒絕我的提供商名稱？

A: 名稱必須與 `config.json` 的 `providers` 區塊中的 key 一致。執行 `tetora provider list` 查看有效名稱。也可以使用提供商的 `type` 名稱作為別名（例如 `gemini-cli` 會自動解析為 `gemini`）。

### Q: 設置 Active Provider 後不生效？

A: 檢查以下幾點：
1. 提供商 key 是否在 `config.json`？（`tetora provider list`）
2. Agent 是否已釘選？設置了明確 `"provider"`（非 `"auto"`）的 Agent 不受全局覆蓋影響。
3. 任務是否有更高級別的 provider 覆蓋？
4. 查看日誌確認提供商解析：`tetora logs | grep provider`

### Q: 如何知道當前使用的是哪個提供商？

A: 運行 `tetora provider status` 查看 Active Provider。
   運行 `tetora dispatch <prompt> --verbose` 查看實際使用的提供商。

### Q: 切換提供商後參數不對？

A: 系統會自動應用預設參數。如需自定義，編輯 `config.json` 中對應提供商的配置。

---

## 技術實現

### 核心檔案

- `internal/config/active_provider.go` - Active Provider 狀態管理
- `internal/cli/provider.go` - CLI 命令實現
- `internal/provider/provider_profiles.go` - 預設參數模板
- `wire.go` - 提供商解析邏輯（已修改）

### 關鍵改動

1. **Config 結構擴展**
   ```go
   ActiveProviderStore *ActiveProviderStore `json:"-"`
   ```

2. **提供商解析優先級**
   ```go
   func resolveProviderName(cfg *Config, task Task, agentName string) string {
       // 1. Task-level (最高)
       // 2. Agent-level pin（明確設置，非 "auto"）
       // 3. Active Provider Override
       // 4. Global default
   }
   ```

3. **模型解析增強**
   ```go
   func buildProviderRequest(...) {
       // Active Provider 模型覆蓋
       // "auto" 模型解析
       // 提供商預設模型
   }
   ```

---

## 未來改進方向

- [ ] Web UI 提供商切換
- [ ] 自動健康檢查和智能選擇
- [ ] 提供商效能監控和報告
- [ ] 成本優化建議
- [ ] 基於任務類型自動選擇最佳提供商
