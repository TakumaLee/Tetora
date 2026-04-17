# Dynamic Provider Switching - User Guide

## Problem Statement

When switching AI providers (e.g., Qwen → Gemini → Claude), users previously had to:
1. Modify each agent's provider configuration individually
2. Tune provider-specific parameters (temperature, maxTokens, etc.)
3. Spend significant time adapting and testing different providers

## Solution

Implemented **session-level dynamic provider switching**, enabling users to:
- ✅ Switch all agents' providers with a single command
- ✅ Automatically apply optimized parameter configurations
- ✅ Test different providers with zero configuration overhead
- ✅ Quickly switch when a provider encounters errors

---

## Quick Start

### 1. Auto-Load via Environment Variables (Recommended)

Set these in your shell profile (`~/.bashrc`, `~/.zshrc`) or Docker environment:

```bash
# Automatically switch provider on startup
export TETORA_PROVIDER=qwen
export TETORA_MODEL=auto          # Optional: specific model or "auto"

# Other examples
export TETORA_PROVIDER=google
export TETORA_MODEL=gemini-2.5-pro

export TETORA_PROVIDER=claude
export TETORA_MODEL=claude-sonnet-4-20250514
```

**Priority**: CLI command > Environment variable > Config file

### 2. Manual Switch via CLI Commands

```bash
# Use Qwen default model
tetora provider set qwen

# Use specific model
tetora provider set qwen qwen3.6-plus

# Auto-select model
tetora provider set qwen auto
```

### 2. Switch to Gemini

```bash
tetora provider set google gemini-2.5-pro
```

### 3. Switch to Claude

```bash
tetora provider set claude claude-sonnet-4-20250514
```

### 4. Check Current Provider

```bash
tetora provider status
```

Example output:
```
Active Provider Override:
  Provider: qwen
  Model:    auto (use provider default)
  Set at:   2026-04-16 10:30:00
  Set by:   CLI

This override affects ALL agent executions.
Use 'tetora provider clear' to remove this override.
```

### 5. Clear Override, Return to Agent-Level Config

```bash
tetora provider clear
```

### 6. List All Configured Providers

```bash
tetora provider list
```

---

## How It Works

### Priority Chain

Provider resolution now follows this priority:

```
0. Active Provider Override (CLI/API setting) ← NEW
1. Task-level Provider (task-level override)
2. Agent-level Provider (agent config)
   - Supports "auto" mode, follows global setting
3. Global Default Provider
4. Legacy Fallback
```

### Automatic Parameter Optimization

Each provider has pre-configured optimal parameters:

| Provider | Default Model | MaxTokens | Temperature | Context Window |
|----------|---------------|-----------|-------------|----------------|
| Qwen | qwen3.6-plus | 8192 | 0.7 | 131K |
| Gemini | gemini-2.5-pro | 65536 | 0.6 | 1M |
| Claude | claude-sonnet-4 | 8192 | 0.7 | 200K |
| Groq | llama-3.3-70b | 8192 | 0.7 | 131K |

These parameters are automatically applied when switching providers, no manual tuning required.

---

## Agent Configuration Example

### Using "auto" Mode

In `config.json`, configure agents to use automatic provider:

```json
{
  "agents": {
    "code-reviewer": {
      "provider": "auto",
      "model": "auto",
      "description": "Code Review Agent"
    },
    "writer": {
      "provider": "auto",
      "model": "auto",
      "description": "Content Creation Agent"
    }
  }
}
```

Now switch all agents' providers with a single command:

```bash
tetora provider set qwen
# All agents will use Qwen, no need to modify each agent config
```

---

## Advanced Features

### 1. Fallback Support

Even with Active Provider set, the global `fallbackProviders` configuration is still respected:

```json
{
  "defaultProvider": "qwen",
  "fallbackProviders": ["google", "claude"]
}
```

When Qwen fails, automatically try Google → Claude.

### 2. Circuit Breaker Integration

Built-in circuit breaker mechanism automatically detects provider failures:
- Opens after 5 consecutive failures
- Attempts recovery after 30 seconds
- Closes after 2 successful requests

Check circuit breaker status:
```bash
tetora health
```

### 3. Preset Parameter Templates

System predefines optimal parameters for each provider in:
```
internal/provider/provider_profiles.go
```

Includes:
- Default model
- MaxTokens
- Temperature / TopP
- FirstTokenTimeout
- ContextWindow
- Capabilities (tools, streaming, vision)

---

## Real-World Scenarios

### Scenario 1: Qwen Returns 400 Error, Quick Switch to Gemini

```bash
# Problem detected: Qwen error
# Quick switch
tetora provider set google gemini-2.5-pro

# Continue working, no need to modify any agent config
tetora dispatch "analyze codebase architecture"
```

### Scenario 2: Test Different Provider Performance

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

# Compare results, choose best provider
```

### Scenario 3: Production Environment with Stable Provider

```bash
# Set production provider
tetora provider set claude claude-sonnet-4-20250514

# Configure fallback list
tetora provider set qwen auto
# In config.json set fallbackProviders: [google, claude]

# Now even if primary provider fails, system auto-fallbacks
```

---

## File Location

Active Provider state is stored at:
```
~/.tetora/runtime/active-provider.json
```

Format:
```json
{
  "providerName": "qwen",
  "model": "auto",
  "setAt": "2026-04-16T10:30:00Z",
  "setBy": "CLI"
}
```

---

## Troubleshooting

### Q: Active Provider not taking effect after setting?

A: Check the following:
1. Is the provider configured in `config.json`?
2. Does the task have a higher-level provider override?
3. Check logs to verify provider resolution: `tetora logs | grep provider`

### Q: How do I know which provider is currently in use?

A: Run `tetora provider status` to check Active Provider.
   Run `tetora dispatch <prompt> --verbose` to see the actual provider being used.

### Q: Parameters incorrect after switching provider?

A: System automatically applies preset parameters. To customize, edit the corresponding provider configuration in `config.json`.

---

## Technical Implementation

### Core Files

- `internal/config/active_provider.go` - Active Provider state management
- `internal/cli/provider.go` - CLI command implementation
- `internal/provider/provider_profiles.go` - Preset parameter templates
- `wire.go` - Provider resolution logic (modified)

### Key Changes

1. **Config Structure Extension**
   ```go
   ActiveProviderStore *ActiveProviderStore `json:"-"`
   ```

2. **Provider Resolution Priority**
   ```go
   func resolveProviderName(cfg *Config, task Task, agentName string) string {
       // 0. Active Provider Override
       // 1. Task-level
       // 2. Agent-level (supports "auto")
       // 3. Global default
   }
   ```

3. **Model Resolution Enhancement**
   ```go
   func buildProviderRequest(...) {
       // Active Provider model override
       // "auto" model resolution
       // Provider default model
   }
   ```

---

## Future Improvements

- [ ] Web UI provider switching
- [ ] Automatic health check and smart selection
- [ ] Provider performance monitoring and reporting
- [ ] Cost optimization recommendations
- [ ] Task-type-based automatic provider selection
