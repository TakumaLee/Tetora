# Dynamic Provider Switching - Implementation Summary

## 📋 Problem Statement

In the PR #58 discussion, users reported: **Switching AI providers (Qwen → Gemini → Claude) still requires significant manual configuration work**, including:
1. Modifying each agent's provider configuration individually
2. Adjusting provider-specific parameters (temperature, maxTokens, timeout, etc.)
3. Unable to quickly switch when a provider encounters errors
4. Lack of runtime dynamic switching mechanism

## ✅ Solution

Implemented a **session-level dynamic provider switching system** with the following core features:

### 1. Active Provider State Management
**File**: `internal/config/active_provider.go`

- Thread-safe file-based state storage
- Supports persistence (persists across restarts)
- Provides Set/Get/Clear operations
- Storage location: `~/.tetora/runtime/active-provider.json`

**Core API**:
```go
store := config.NewActiveProviderStore(path)
store.Set("qwen", "auto", "CLI")     // Set
store.Get()                           // Get
store.Clear()                         // Clear
store.HasActiveOverride()             // Check if set
```

### 2. CLI Commands
**File**: `internal/cli/provider.go`

Added 6 subcommands:
- `tetora provider set <name> [model]` - Set active provider
- `tetora provider use <name> [model]` - Alias for set
- `tetora provider status` - Check current state
- `tetora provider show` - Alias for status
- `tetora provider clear` - Clear override
- `tetora provider list` - List all configured providers

**Usage Examples**:
```bash
tetora provider set qwen                          # Use Qwen default model
tetora provider set google gemini-2.5-pro         # Use specific model
tetora provider set qwen auto                     # Auto-select model
tetora provider status                            # Check current state
tetora provider clear                             # Return to agent config
```

### 3. Provider Resolution Priority Enhancement
**File**: `wire.go` - `resolveProviderName()`

New priority chain:
```
1. Task-level Provider (task-level override) ← Highest priority
2. Active Provider Override (CLI/API setting) ← NEW
3. Agent-level Provider (agent config)
   - Supports "auto" mode, follows global setting ← NEW
4. Global Default Provider
5. Legacy Fallback
```

**Key Changes**:
```go
func resolveProviderName(cfg *Config, task Task, agentName string) string {
    // 1. Task-level
    if task.Provider != "" {
        return task.Provider
    }
    
    // 2. Active Provider Override ← NEW
    if cfg.ActiveProviderStore != nil && cfg.ActiveProviderStore.HasActiveOverride() {
        return cfg.ActiveProviderStore.Get().ProviderName
    }
    
    // 3. Agent-level (supports "auto")
    if rc.Provider == "auto" {
        // Continue to global default
    }
    
    // 4. Global default
    // 5. Fallback
}
```

### 4. Model Resolution Enhancement
**File**: `wire.go` - `buildProviderRequest()`

Supports Active Provider model override:
```go
func buildProviderRequest(...) {
    model := task.Model
    
    // Active Provider model override
    if cfg.ActiveProviderStore.HasActiveOverride() {
        activeState := cfg.ActiveProviderStore.Get()
        if activeState.Model != "" && activeState.Model != "auto" {
            model = activeState.Model
        }
    }
    
    // "auto" model resolution
    // Provider default model
    // Global default model
}
```

### 5. Fallback Mechanism Enhancement
**File**: `wire.go` - `buildProviderCandidates()`

Active Provider mode still respects global FallbackProviders:
```go
func buildProviderCandidates(...) []string {
    primary := resolveProviderName(...)
    candidates := []string{primary}
    
    // Active Provider mode uses global fallbacks
    if cfg.ActiveProviderStore.HasActiveOverride() {
        for _, fb := range cfg.FallbackProviders {
            candidates = append(candidates, fb)
        }
        return candidates
    }
    
    // Normal flow: Agent + Global fallbacks
}
```

### 6. Provider Preset Parameter Templates
**File**: `internal/provider/provider_profiles.go`

Predefines optimal parameters for each provider:

| Provider | Default Model | MaxTokens | Temperature | Context | Strengths |
|----------|---------------|-----------|-------------|---------|-----------|
| Qwen | qwen3.6-plus | 8192 | 0.7 | 131K | Chinese, code, cost-effective |
| Gemini | gemini-2.5-pro | 65536 | 0.6 | 1M | Massive context, multimodal |
| Claude | claude-sonnet-4 | 8192 | 0.7 | 200K | Code understanding, complex reasoning |
| Groq | llama-3.3-70b | 8192 | 0.7 | 131K | Speed, low latency |

**API**:
```go
profile := provider.GetProviderProfile("qwen")
provider.ApplyProfileToConfig(profile, &cfg)
settings := profile.GetOptimizedSettings()
```

### 7. Config Structure Extension
**File**: `internal/config/config.go`

Added ActiveProviderStore field:
```go
type Config struct {
    // ... existing fields
    
    // Active provider override store
    ActiveProviderStore *ActiveProviderStore `json:"-"`
}
```

## 📊 Test Coverage

### Unit Tests
**File**: `internal/config/active_provider_test.go`

- ✅ SaveAndLoad - Save and load
- ✅ Clear - Clear functionality
- ✅ HasActiveOverride - State check
- ✅ ConcurrentAccess - Concurrency safety
- ✅ Persistence - Persistence
- ✅ NonExistentFile - Error tolerance
- ✅ StateTime - Timestamp validation
- ✅ GetReturnsCopy - Reference safety

### Integration Tests
**File**: `provider_integration_test.go`

- ✅ ActiveProviderOverride - Priority tests
- ✅ AutoMode - Auto mode tests
- ✅ ModelResolution - Model resolution tests
- ✅ FallbacksWithActiveProvider - Fallback tests
- ✅ ProviderProfiles_Availability - Preset availability
- ✅ ProviderProfiles_ApplyToConfig - Config application

### Performance Tests
```
BenchmarkActiveProviderStore_Set    15613 ops/s    84885 ns/op    984 B/op
BenchmarkActiveProviderStore_Get    11947618 ops/s 113 ns/op      80 B/op
BenchmarkActiveProviderStore_Load   60694 ops/s    20430 ns/op    1272 B/op
```

**All tests passing** ✅

## 🎯 Use Cases

### Use Case 1: Quick Switch Failing Provider
```bash
# Qwen returns 400 error
tetora provider set google gemini-2.5-pro
# Immediately continue working, no need to modify agent config
```

### Use Case 2: Test Different Provider Performance
```bash
# Test Qwen
tetora provider set qwen
tetora dispatch "write a sorting algorithm"

# Test Gemini
tetora provider set google
tetora dispatch "write a sorting algorithm"

# Test Claude
tetora provider set claude
tetora dispatch "write a sorting algorithm"
```

### Use Case 3: Agent Config Using Auto Mode
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

Now switch all agents with one command:
```bash
tetora provider set gemini
```

## 📁 File List

### New Files
1. `internal/config/active_provider.go` - Active Provider state management
2. `internal/config/active_provider_test.go` - Unit tests
3. `internal/cli/provider.go` - CLI command implementation
4. `internal/provider/provider_profiles.go` - Preset parameter templates
5. `provider_integration_test.go` - Integration tests
6. `docs/PROVIDER_SWITCH_GUIDE.md` - User guide
7. `docs/IMPLEMENTATION_SUMMARY.md` - This document
8. `docs/QUICK_REFERENCE.md` - Quick reference
9. `docs/i18n/zh-TW/` - Traditional Chinese translations

### Modified Files
1. `internal/config/config.go` - Added ActiveProviderStore field
2. `wire.go` - Modified provider resolution logic
3. `main.go` - Registered provider CLI command

## 🔧 Technical Highlights

### 1. Non-Destructive Design
- Fully backward compatible
- Existing configs continue to work
- Active Provider is an optional override layer

### 2. Thread-Safe
- Uses `sync.RWMutex` to protect concurrent access
- All operations return copies, not references

### 3. Persistence
- File-based storage, persists across restarts
- Auto-creates directories
- Gracefully handles non-existent files

### 4. Clear Priority
- Task > Active > Agent > Global > Fallback
- Each level has clear comments

### 5. Error Tolerance
- Returns empty state when file doesn't exist
- JSON parsing error handling
- Auto-creates directories

## 🚀 Future Improvements

- [ ] Web UI provider switching
- [ ] API endpoint support (`/api/provider/active`)
- [ ] Automatic health check and smart selection
- [ ] Provider performance monitoring and reporting
- [ ] Task-type-based automatic provider selection
- [ ] Cost optimization recommendations
- [ ] Custom provider preset extensions

## 📝 Relationship with PR #58

PR #58 implemented:
- ✅ Qwen preset configuration
- ✅ `model: "auto"` automatic resolution
- ✅ Universal agent output workspace
- ✅ Init flow de-hardcoding

This implementation adds:
- ✨ **Session-level dynamic override** (no need to modify agent configs)
- ✨ **CLI command management** (one-command switching)
- ✨ **Preset parameter templates** (auto-apply optimal parameters)
- ✨ **Complete test coverage** (unit + integration + performance)

Together they achieve **truly worry-free provider switching**.

## 💡 Summary

By implementing the session-level dynamic provider switching system, users can now:

✅ **Switch all agents' providers with one command**
✅ **Automatically apply optimal parameter configurations**
✅ **Test different providers with zero configuration overhead**
✅ **Quickly switch when a provider encounters errors**
✅ **Maintain full fallback capabilities**
✅ **Fully backward compatible**

This solves the core issue raised in the PR #58 discussion: **Switching providers no longer requires significant manual configuration work**.
