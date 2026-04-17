# Dynamic Provider Switching - Quick Reference

## 🎯 Core Commands

```bash
# Switch provider
tetora provider set qwen                    # Use default model
tetora provider set google gemini-2.5-pro   # Specify model
tetora provider set qwen auto               # Auto-select

# Check status
tetora provider status                      # Current active provider
tetora provider list                        # All configured providers

# Clear override
tetora provider clear                       # Return to agent config
```

## 🌍 Environment Variables (Auto-Load)

Set these in your shell profile (`~/.bashrc`, `~/.zshrc`) or Docker environment:

```bash
# Automatically switch provider on startup
export TETORA_PROVIDER=qwen
export TETORA_MODEL=auto          # Optional: specific model or "auto"

# Examples
export TETORA_PROVIDER=google
export TETORA_MODEL=gemini-2.5-pro

export TETORA_PROVIDER=claude
export TETORA_MODEL=claude-sonnet-4-20250514
```

**Priority**: CLI command > Environment variable > Config file

## 📊 Priority Chain

```
Task Provider → Active Provider → Agent Provider → Global Default → Fallback
```

## 🔧 Agent Configuration Example

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

## 📋 Preset Parameters

| Provider | Default Model | MaxTokens | Temperature |
|----------|---------------|-----------|-------------|
| qwen | qwen3.6-plus | 8192 | 0.7 |
| google | gemini-2.5-pro | 65536 | 0.6 |
| claude | claude-sonnet-4 | 8192 | 0.7 |
| groq | llama-3.3-70b | 8192 | 0.7 |

## 💡 Use Cases

### Use Case 1: Quick Switch
```bash
tetora provider set google  # When Qwen errors
```

### Use Case 2: Test & Compare
```bash
tetora provider set qwen && tetora dispatch "test task"
tetora provider set google && tetora dispatch "test task"
tetora provider set claude && tetora dispatch "test task"
```

### Use Case 3: Production Environment
```bash
# Set stable provider + fallback list
tetora provider set claude
# config.json: "fallbackProviders": ["qwen", "google"]
```

## 📁 File Locations

- State file: `~/.tetora/runtime/active-provider.json`
- Config file: `~/.tetora/config.json`
- Preset code: `internal/provider/provider_profiles.go`

## ✅ Tests

```bash
# Run all tests
go test ./internal/config ./internal/provider -v

# Run integration tests
go test -run TestProvider . -v
```

## 🔍 Troubleshooting

**Q: Not taking effect after setting?**
```bash
tetora provider status                    # Check if set
tetora logs | grep provider               # Check logs
```

**Q: How to know which provider is in use?**
```bash
tetora provider status                    # Check Active Provider
tetora dispatch "test" --verbose          # Check actual provider
```

**Q: Clear override?**
```bash
tetora provider clear
```
