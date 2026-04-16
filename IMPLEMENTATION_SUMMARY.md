# 動態提供商切換 - 實現總結

## 📋 問題描述

在 PR #58 的討論中，使用者反饋：**切換 AI 提供商（Qwen → Gemini → Claude）仍然需要大量手動配置工作**，包括：
1. 逐個修改每個 Agent 的提供商配置
2. 調整不同提供商的參數（temperature、maxTokens、timeout 等）
3. 提供商報錯時無法快速切換
4. 缺乏執行時動態切換機制

## ✅ 實現方案

實現了 **Session 級別的提供商動態切換系統**，包含以下核心功能：

### 1. Active Provider 狀態管理
**檔案**: `internal/config/active_provider.go`

- 執行緒安全的檔案化狀態儲存
- 支援持久化（重啟後保持）
- 提供 Set/Get/Clear 操作
- 儲存位置：`~/.tetora/runtime/active-provider.json`

**核心 API**:
```go
store := config.NewActiveProviderStore(path)
store.Set("qwen", "auto", "CLI")     // 設置
store.Get()                           // 獲取
store.Clear()                         // 清除
store.HasActiveOverride()             // 檢查是否設置
```

### 2. CLI 命令
**檔案**: `internal/cli/provider.go`

新增 6 個子命令：
- `tetora provider set <name> [model]` - 設置活躍提供商
- `tetora provider use <name> [model]` - set 的別名
- `tetora provider status` - 查看當前狀態
- `tetora provider show` - status 的別名
- `tetora provider clear` - 清除覆蓋
- `tetora provider list` - 列出所有配置的提供商

**使用範例**:
```bash
tetora provider set qwen                          # 使用 Qwen 預設模型
tetora provider set google gemini-2.5-pro         # 使用指定模型
tetora provider set qwen auto                     # 自動選擇模型
tetora provider status                            # 查看當前狀態
tetora provider clear                             # 返回 Agent 配置
```

### 3. 提供商解析優先級增強
**檔案**: `wire.go` - `resolveProviderName()`

新的優先級鏈：
```
1. Task-level Provider (任務級別覆蓋) ← 最高優先級
2. Active Provider Override (CLI/API 設置) ← 新增
3. Agent-level Provider (Agent 配置)
   - 支援 "auto" 模式，跟隨全局設置 ← 新增
4. Global Default Provider (全局預設)
5. Legacy Fallback (向後相容)
```

**關鍵改動**:
```go
func resolveProviderName(cfg *Config, task Task, agentName string) string {
    // 1. Task-level
    if task.Provider != "" {
        return task.Provider
    }
    
    // 2. Active Provider Override ← 新增
    if cfg.ActiveProviderStore != nil && cfg.ActiveProviderStore.HasActiveOverride() {
        return cfg.ActiveProviderStore.Get().ProviderName
    }
    
    // 3. Agent-level (支援 "auto")
    if rc.Provider == "auto" {
        // 繼續到全局預設
    }
    
    // 4. Global default
    // 5. Fallback
}
```

### 4. 模型解析增強
**檔案**: `wire.go` - `buildProviderRequest()`

支援 Active Provider 的模型覆蓋：
```go
func buildProviderRequest(...) {
    model := task.Model
    
    // Active Provider 模型覆蓋
    if cfg.ActiveProviderStore.HasActiveOverride() {
        activeState := cfg.ActiveProviderStore.Get()
        if activeState.Model != "" && activeState.Model != "auto" {
            model = activeState.Model
        }
    }
    
    // "auto" 模型解析
    // 提供商預設模型
    // 全局預設模型
}
```

### 5. 故障轉移機制增強
**檔案**: `wire.go` - `buildProviderCandidates()`

Active Provider 模式下仍然尊重全局 FallbackProviders：
```go
func buildProviderCandidates(...) []string {
    primary := resolveProviderName(...)
    candidates := []string{primary}
    
    // Active Provider 模式下使用全局 fallbacks
    if cfg.ActiveProviderStore.HasActiveOverride() {
        for _, fb := range cfg.FallbackProviders {
            candidates = append(candidates, fb)
        }
        return candidates
    }
    
    // 正常流程：Agent + Global fallbacks
}
```

### 6. 提供商預設參數模板
**檔案**: `internal/provider/provider_profiles.go`

為每個提供商預定義最優參數：

| 提供商 | 預設模型 | MaxTokens | Temperature | Context | 特點 |
|--------|----------|-----------|-------------|---------|------|
| Qwen | qwen3.6-plus | 8192 | 0.7 | 131K | 中文、程式碼、性價比 |
| Gemini | gemini-2.5-pro | 65536 | 0.6 | 1M | 超大上下文、多模態 |
| Claude | claude-sonnet-4 | 8192 | 0.7 | 200K | 程式碼理解、複雜推理 |
| Groq | llama-3.3-70b | 8192 | 0.7 | 131K | 極速、低延遲 |

**API**:
```go
profile := provider.GetProviderProfile("qwen")
provider.ApplyProfileToConfig(profile, &cfg)
settings := profile.GetOptimizedSettings()
```

### 7. Config 結構擴展
**檔案**: `internal/config/config.go`

添加 ActiveProviderStore 欄位：
```go
type Config struct {
    // ... 現有欄位
    
    // Active provider override store
    ActiveProviderStore *ActiveProviderStore `json:"-"`
}
```

## 📊 測試覆蓋

### 單元測試
**檔案**: `internal/config/active_provider_test.go`

- ✅ SaveAndLoad - 儲存和載入
- ✅ Clear - 清除功能
- ✅ HasActiveOverride - 狀態檢查
- ✅ ConcurrentAccess - 併發安全
- ✅ Persistence - 持久化
- ✅ NonExistentFile - 容錯處理
- ✅ StateTime - 時間戳驗證
- ✅ GetReturnsCopy - 引用安全

### 整合測試
**檔案**: `provider_integration_test.go`

- ✅ ActiveProviderOverride - 優先級測試
- ✅ AutoMode - 自動模式測試
- ✅ ModelResolution - 模型解析測試
- ✅ FallbacksWithActiveProvider - 故障轉移測試
- ✅ ProviderProfiles_Availability - 預設可用性
- ✅ ProviderProfiles_ApplyToConfig - 配置應用

### 效能測試
```
BenchmarkActiveProviderStore_Set    15613 ops/s    84885 ns/op    984 B/op
BenchmarkActiveProviderStore_Get    11947618 ops/s 113 ns/op      80 B/op
BenchmarkActiveProviderStore_Load   60694 ops/s    20430 ns/op    1272 B/op
```

**所有測試通過** ✅

## 🎯 使用場景

### 場景 1: 快速切換報錯的提供商
```bash
# Qwen 返回 400 錯誤
tetora provider set google gemini-2.5-pro
# 立即繼續工作，無需修改 Agent 配置
```

### 場景 2: 測試不同提供商的效果
```bash
# 測試 Qwen
tetora provider set qwen
tetora dispatch "寫一個排序算法"

# 測試 Gemini
tetora provider set google
tetora dispatch "寫一個排序算法"

# 測試 Claude
tetora provider set claude
tetora dispatch "寫一個排序算法"
```

### 場景 3: Agent 配置使用 auto 模式
```json
{
  "agents": {
    "code-reviewer": {
      "provider": "auto",
      "model": "auto"
    }
  },
  "defaultProvider": "qwen",
  "fallbackProviders": ["google", "claude"]
}
```

現在一條命令切換所有 Agent：
```bash
tetora provider set gemini
```

## 📁 檔案清單

### 新增檔案
1. `internal/config/active_provider.go` - Active Provider 狀態管理
2. `internal/config/active_provider_test.go` - 單元測試
3. `internal/cli/provider.go` - CLI 命令實現
4. `internal/provider/provider_profiles.go` - 預設參數模板
5. `provider_integration_test.go` - 整合測試
6. `PROVIDER_SWITCH_GUIDE.md` - 使用指南
7. `IMPLEMENTATION_SUMMARY.md` - 本文檔

### 修改檔案
1. `internal/config/config.go` - 添加 ActiveProviderStore 欄位
2. `wire.go` - 修改提供商解析邏輯
3. `main.go` - 註冊 provider CLI 命令

## 🔧 技術亮點

### 1. 非破壞性設計
- 完全向後相容
- 現有配置繼續工作
- Active Provider 是可选的覆蓋層

### 2. 執行緒安全
- 使用 `sync.RWMutex` 保護併發訪問
- 所有操作返回副本而非引用

### 3. 持久化
- 基於檔案的儲存，重啟後保持
- 自動創建目錄
- 優雅處理不存在檔案

### 4. 優先級清晰
- Task > Active > Agent > Global > Fallback
- 每個級別都有明確註釋

### 5. 容錯機制
- 檔案不存在時返回空狀態
- JSON 解析錯誤處理
- 目錄自動創建

## 🚀 未來改進方向

- [ ] Web UI 提供商切換
- [ ] API 端點支援 (`/api/provider/active`)
- [ ] 自動健康檢查和智能選擇
- [ ] 提供商效能監控和報告
- [ ] 基於任務類型自動選擇最佳提供商
- [ ] 成本優化建議
- [ ] 提供商預設的自定義擴展

## 📝 與 PR #58 的關係

PR #58 實現了：
- ✅ Qwen 預設配置
- ✅ `model: "auto"` 自動解析
- ✅ 通用 Agent 輸出工作區
- ✅ 初始化流程去硬編碼

本實現在此基礎上增加了：
- ✨ **Session 級別的動態覆蓋**（無需修改 Agent 配置）
- ✨ **CLI 命令管理**（一條命令切換）
- ✨ **預設參數模板**（自動應用最優參數）
- ✨ **完整的測試覆蓋**（單元 + 整合 + 效能）

兩者結合實現了**真正的無顧慮提供商切換**。

## 💡 總結

通過實現 Session 級別的提供商動態切換系統，使用者現在可以：

✅ **一條命令切換所有 Agent 的提供商**
✅ **自動應用最優參數配置**
✅ **零配置成本測試不同提供商**
✅ **提供商報錯時快速切換**
✅ **保持完整的故障轉移能力**
✅ **完全向後相容**

這解決了 PR #58 討論中提出的核心問題：**切換提供商不再需要大量手動配置工作**。
