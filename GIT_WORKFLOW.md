# Tetora Git Workflow

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
│                     main ← 同步 upstream/main                   │
│                     feat/* ← 開發分支                            │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                              git clone
                                 │
┌────────────────────────────────┴────────────────────────────────┐
│                     本地開發環境                                 │
│                     main ← 本地主分支                            │
│                     feat/* ← 功能開發分支                        │
└─────────────────────────────────────────────────────────────────┘
```

## 🔄 標準開發流程

### 1. 開始新功能

```bash
# 1. 確保本地 main 是最新的
git checkout main
git fetch upstream
git merge upstream/main

# 2. 從 main 創建新功能分支
git checkout -b feat/your-feature-name
```

### 2. 開發與提交

```bash
# 開發功能...
# 撰寫測試、編碼、運行 go test

# 提交改動
git add .
git commit -m "feat(your-feature): describe what you did"

# 推送到你的 fork
git push origin feat/your-feature-name
```

### 3. 創建 PR 到 upstream

```bash
# 從你的 fork 分支 PR 到 upstream main
gh pr create \
  --repo TakumaLee/Tetora \
  --base main \
  --head ymow:feat/your-feature-name \
  --title "FEAT: YOUR FEATURE TITLE" \
  --body "DESCRIPTION OF YOUR CHANGES"
```

### 4. 回應 Review 意見

```bash
# 根據 @黑曜 的 review 意見修改
# 在本地修正後...

git add .
git commit -m "fix: address PR review comments"
git push origin feat/your-feature-name

# 在 PR 中通知 reviewer
gh pr comment <PR_NUMBER> --repo TakumaLee/Tetora --body "@黑曜 已修正，請 review"
```

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

# 3. 關閉 PR
gh pr close <PR_NUMBER> --repo ymow/Tetora
```

## 📁 當前分支狀態

| 項目 | 值 |
|------|-----|
| **遠端 (origin)** | `ymow/Tetora` (小喬的 fork) |
| **Upstream** | `TakumaLee/Tetora` (琉璃主倉庫) |
| **當前分支** | `feat/qwen-provider-support` |
| **目標分支** | `upstream/main` |

## 🔀 Open PRs

| PR # | 分支 | 標題 | 狀態 |
|------|------|------|------|
| [#58](https://github.com/TakumaLee/Tetora/pull/58) | `feat/qwen-provider-support` | feat(provider): add Qwen preset, auto model resolution, and universal workspace | OPEN |
| [#59](https://github.com/TakumaLee/Tetora/pull/59) | `feat/universal-workspace` | feat(workspace): implement universal agent output workspace | OPEN |

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
```

---

> — **小喬** 🎵
