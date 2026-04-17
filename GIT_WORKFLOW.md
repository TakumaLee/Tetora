# Tetora Git Workflow（本地參考）

## 📊 架構圖

```
┌─────────────────────────────────────────────────────────────────┐
│                     TakumaLee/Tetora (upstream)                 │
│                        (琉璃團隊主倉庫)                          │
│                         main ← 所有 PR 的目標                    │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                              fork 分叉
                                 │
┌────────────────────────────────┴────────────────────────────────┐
│                     ymow/Tetora (origin)                        │
│                        (小喬 的 fork)                            │
│                     main ← 與 upstream/main 同步               │
│                     develop ← 本地開發整合分支                   │
│                     feat/* ← 功能開發分支                        │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                              git clone
                                 │
┌────────────────────────────────┴────────────────────────────────┐
│                     本地開發環境                                 │
│                     main ← 與 upstream/main 同步               │
│                     develop ← 本地開發整合分支                   │
│                     feat/* ← 功能開發分支                        │
│                     config.local.json ← 本地配置（不進入 git）    │
└─────────────────────────────────────────────────────────────────┘
```

## 🔄 分支策略

### main 分支
- **目的**: 與 upstream/main 保持完全同步
- **內容**: 只包含 upstream 已合併的提交
- **操作**: 定期 `git fetch upstream && git merge upstream/main`
- **推送**: 同步到 origin/main 保持遠端一致

### develop 分支
- **目的**: 本地開發整合，包含所有本地改動
- **內容**: 本地配置、文檔、測試功能
- **操作**: 所有 feature 分支合併到 develop 測試
- **推送**: 推送到 origin/develop 作為本地開發記錄

### feature 分支
- **目的**: 單一功能或修復開發
- **來源**: 從 develop 或 main 建立
- **合併**: 完成後合併到 develop 測試，然後 PR 到 upstream/main

## 🔄 標準開發流程

### 1. 同步 main（定期執行）

```bash
# 確保 main 與 upstream 同步
git checkout main
git fetch upstream
git merge upstream/main
git push origin main
```

### 2. 開始新功能

```bash
# 1. 從 develop 創建新功能分支（或從 main 如果是純 upstream 功能）
git checkout develop
git checkout -b feat/your-feature-name

# 或者如果是純 upstream 相關功能
git checkout main
git checkout -b feat/your-feature-name
```

### 3. 開發與提交

```bash
# 開發功能...
# 撰寫測試、編碼、運行 go test

# 提交改動
git add .
git commit -m "feat(your-feature): describe what you did"

# 推送到你的 fork
git push origin feat/your-feature-name
```

### 4. 整合到 develop 測試

```bash
# 功能完成後，合併到 develop 進行整合測試
git checkout develop
git merge feat/your-feature-name
git push origin develop

# 在 develop 上跑一陣子確認沒問題
```

### 5. 創建 PR 到 upstream

```bash
# 從 develop（或 feature 分支）PR 到 upstream main
gh pr create \
  --repo TakumaLee/Tetora \
  --base main \
  --head ymow:feat/your-feature-name \
  --title "FEAT: YOUR FEATURE TITLE" \
  --body "DESCRIPTION OF YOUR CHANGES"
```

### 4. 回應 Review 意見

**⚠️ 重要：在修改 PR 前，務必先確認並同步 upstream/main 的最新狀態！**

```bash
# 第一步：先同步 upstream（必須！）
git fetch upstream
git checkout main
git merge upstream/main
git push origin main

# 第二步：將最新 main 合併到你的 PR 分支
git checkout feat/your-feature-name
git merge main
# 如果有衝突，解決衝突後...
git add .
git commit -m "merge: resolve conflicts with upstream/main"

# 第三步：根據 @黑曜 的 review 意見修改
# 在本地修正後...
git add .
git commit -m "fix: address PR review comments"

# 第四步：推送修正
git push origin feat/your-feature-name

# 第五步：在 PR 中通知 reviewer
gh pr comment <PR_NUMBER> --repo TakumaLee/Tetora --body "@黑曜 已修正，請 review"
```

**為什麼要先同步 upstream？**
- upstream/main 可能已有其他 PR 的合併
- 不同步可能導致衝突或重複修改
- 確保你的修正是基於最新的程式碼基礎
- 減少 PR 合併時的衝突風險

### 5. PR 合併後清理

```bash
# PR 被合併到 upstream/main 後

# 1. 更新本地 main
git checkout main
git fetch upstream
git merge upstream/main

# 2. 刪除本地和遠端分支
git branch -d feat/your-feature-name
git push origin --delete feat/your-feature-name

# 3. 關閉 PR（如果需要）
gh pr close <PR_NUMBER> --repo ymow/Tetora
```

## 📁 本地化配置架構

### 三層配置系統

```
config.json              ← 共用配置（進入 git，團隊共享）
config.local.json        ← 本地配置（gitignore，個人/客戶特定）
config.<client>.json     ← 多租戶配置（可選，per-client）
```

### 合併機制

- **深層合併**：`config.local.json` 遞迴合併到 `config.json`
- **local 優先**：local 的值覆蓋 base config
- **Hot-reload**：SIGHUP 信號重新載入 local config
- **Git 安全**：`config.local.json` 在 `.gitignore` 中

### 新 Client Onboarding

參考 `LOCAL_CONFIG_GUIDE.md` 完成以下步驟：

1. 建立客戶配置目錄：`mkdir -p ~/.tetora/clients/<client-id>`
2. 複製配置範本：`cp config.local.example.json config.local.json`
3. 配置必要欄位：apiToken, providers, agents, etc.
4. 設定檔案權限：`chmod 600 config.local.json`
5. 驗證配置：`tetora health`

## 📁 當前分支狀態

| 項目 | 值 |
|------|-----|
| **遠端 (origin)** | `ymow/Tetora` (小喬的 fork) |
| **Upstream** | `TakumaLee/Tetora` (琉璃主倉庫) |
| **main** | ✅ 與 upstream/main 同步 |
| **develop** | ✅ 本地開發整合分支 |
| **當前分支** | `develop` |
| **目標分支** | `upstream/main` |

## 🔀 Open PRs

| PR # | 分支 | 標題 | 狀態 |
|------|------|------|------|
| [#58](https://github.com/TakumaLee/Tetora/pull/58) | `feat/qwen-provider-support` | feat(provider): add Qwen preset, auto model resolution, and universal workspace | OPEN |
| [#59](https://github.com/TakumaLee/Tetora/pull/59) | `feat/universal-workspace` | feat(workspace): implement universal agent output workspace | OPEN |

## 🌿 分支使用指南

| 分支 | 用途 | 來源 | 合併到 | PR 到 |
|------|------|------|--------|-------|
| `main` | 與 upstream 同步 | upstream/main | origin/main | - |
| `develop` | 本地開發整合 | develop 或 main | origin/develop | - |
| `feat/*` | 新功能開發 | develop 或 main | develop | upstream/main |
| `fix/*` | Bug 修復 | develop 或 main | develop | upstream/main |
| `docs/*` | 文檔更新 | develop | develop | upstream/main |

## ⚠️ 注意事項

### 1. 絕對不要直接推送 main

```bash
# ❌ 錯誤
git push origin main

# ✅ 正確 - 先同步 upstream
git fetch upstream
git merge upstream/main
git push origin main
```

### 2. PR 合併後的分支清理

當 upstream 合併了你的 PR，記得：
- 刪除本地分支
- 刪除遠端分支
- 切換回 main
- 更新 local main

### 3. 解決衝突

如果 upstream/main 有新提交導致衝突：

```bash
git checkout feat/your-feature-name
git fetch upstream
git merge upstream/main
# 解決衝突...
git add .
git commit -m "merge: resolve conflicts with upstream/main"
git push origin feat/your-feature-name
```

### 4. 提交訊息格式

遵循以下格式：
```
<type>(<scope>): <description>

[optional body]
```

| type | 用途 | 範例 |
|------|------|------|
| `feat` | 新功能 | `feat(provider): add Qwen preset` |
| `fix` | 修復 bug | `fix(workspace): address PR review comments` |
| `docs` | 文檔 | `docs: add workflow guide` |
| `chore` | 雜務 | `chore: update dependencies` |
| `refactor` | 重構 | `refactor: simplify provider resolution` |

## 🎯 快速命令參考

```bash
# 查看所有分支
git branch -a

# 查看提交歷史
git log --oneline -10

# 查看狀態
git status

# 推送變更
git push origin <branch-name>

# 創建 PR
gh pr create --repo TakumaLee/Tetora --base main --head ymow:<branch-name>

# 列出你的 PRs
gh pr list --repo TakumaLee/Tetora

# 查看 PR 狀態
gh pr view <NUMBER> --repo TakumaLee/Tetora

# 添加 PR comment
gh pr comment <NUMBER> --repo TakumaLee/Tetora --body "message"

# 熱重載配置
kill -HUP $(pgrep -f "tetora serve")

# 驗證配置
tetora health
```

## 📚 本地文檔

- `GIT_WORKFLOW.md` - 本文件（本地參考）
- `LOCAL_CONFIG_GUIDE.md` - 本地化配置指南
- `MULTI_DEVICE_STRATEGY.md` - 多機協同架構指南（同一帳號跨電腦同步）
- `config.local.example.json` - 配置範本

---

> — **小喬** 🎵
