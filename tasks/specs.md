# Task Specs

> 已入票的詳細規格。以 task ID 或功能名稱索引。

---

## PostTaskGit + IdleAnalysis + Config

> 來源：2026-03-03 規劃
> 狀態：spec 完成，待開票

### 背景

黑曜等 agent 透過 taskboard dispatch 在 worktree 裡作業時，完成的改動散落在本機、沒有 commit 也沒有 push。主人需要手動撿 diff、手動開 branch、手動 push。這三個功能把這段流程自動化，同時加入「閒置時自動分析開票」讓 agent 不會空轉。

### 功能 1: `postTaskGit()` — Task 完成後自動 git 操作

**觸發時機**：`taskboard_dispatch.go` L498 區塊，`newStatus == "done" || newStatus == "review"` 時

**前提條件**（全部滿足才執行）：
1. `cfg.TaskBoard.GitCommit == true`（config opt-in）
2. Task 的 project 有對應的 `Project.Workdir`（從 DB 查）
3. `Workdir` 是 git repo（`git -C <workdir> rev-parse --git-dir` 成功）
4. Working tree 有變動（`git -C <workdir> status --porcelain` 非空）

**執行流程**：
```
1. branch 名 = "{assignee}/{project}"
   例：kokuyou/lookr-android-remake

2. git -C <workdir> checkout -B <branch>
   （-B：如果 branch 已存在就直接切過去，同專案多任務累積在同一 branch）

3. git -C <workdir> add -A

4. git -C <workdir> commit -m "[{task_id}] {task_title}"
   例：[TB-a1b2c3] Fix camera preview crash on Android 15

5. 如果 cfg.TaskBoard.GitPush == true：
   git -C <workdir> push -u origin <branch>
```

**錯誤處理**：
- 任何 git 指令失敗 → `logWarn` + `AddComment` 記錄失敗原因，**不影響** task status（task 本身已經 done/review）
- 不會因為 git 失敗把 task 改成 failed

**Branch 策略**：
- 同 assignee + 同 project → 同一條 branch，多次 commit 累積
- 主人一次 review → merge → 刪 branch
- 不碰 main，不自動 merge

**不觸發的情況**：
- `newStatus == "failed"` → 不做 git 操作
- project 沒有 workdir → skip
- workdir 不是 git repo → skip
- 沒有 diff → skip（但仍 log 一行 info）

### 功能 2: `idleAnalysis()` — 閒置時自動分析開票

**觸發時機**：dispatch scan 發現「todo 清空但仍有 project 活躍」時

**判斷條件**：
1. TaskBoard auto-dispatch 開啟
2. `cfg.TaskBoard.IdleAnalyze == true`（config opt-in）
3. 目前沒有 doing/review 的 task
4. 目前沒有 todo 的 task（全部 done/failed/backlog）
5. 同 project 24h 內沒做過 idle analysis（防無限循環）

**實作位置**：`taskboard_dispatch.go` 的 `scanAndDispatch()` 或獨立函數，在「沒有可 dispatch 的 task」時呼叫

**執行流程**：
```
1. 列出所有 active projects（有 non-backlog task 在過去 7 天完成的 project）
2. 每個 project：
   a. 收集最近完成的 tasks（title + status + comments 摘要）
   b. 收集 project workdir 的 git log --oneline -20（如果有 workdir）
   c. 組 prompt 送 LLM（haiku, budget $0.3）：
      "Based on the completed tasks and recent git activity for project {name},
       identify 1-3 logical next tasks. Output JSON array of {title, description, priority}."
   d. Parse 回傳 → CreateTask per item，status = "backlog"（不是 todo！）
   e. AddComment: "[idle-analysis] Auto-generated from project analysis"
3. 記錄分析時間（防 24h 內重複）
```

**防護機制**：
- 產出的票一律是 `backlog`，需要 triage 才能變 `todo` → 防止 agent 自己生票自己做的無限循環
- 同 project 24h cooldown
- 單次最多 3 個 project、每個 project 最多 3 張票
- LLM 呼叫失敗 → logWarn，不重試

### 功能 3: Config 新增欄位

**位置**：`TaskBoardConfig` struct（`taskboard.go` L57）

```go
type TaskBoardConfig struct {
    Enabled       bool                    `json:"enabled"`
    MaxRetries    int                     `json:"maxRetries,omitempty"`
    RequireReview bool                    `json:"requireReview,omitempty"`
    AutoDispatch  TaskBoardDispatchConfig `json:"autoDispatch,omitempty"`
    // --- 新增 ---
    GitCommit     bool `json:"gitCommit,omitempty"`     // task done 後自動 commit
    GitPush       bool `json:"gitPush,omitempty"`       // commit 後自動 push（需 gitCommit=true）
    IdleAnalyze   bool `json:"idleAnalyze,omitempty"`   // 閒置時自動分析開票
}
```

**預設值**：三個都是 `false`（opt-in），不改變現有行為。

**config.json 範例**：
```json
{
  "taskBoard": {
    "enabled": true,
    "gitCommit": true,
    "gitPush": true,
    "idleAnalyze": true,
    "autoDispatch": { "enabled": true }
  }
}
```

### 功能 4: Issue Auto-Capture — 開發中問題自動記錄

> 雙層防禦：prompt 層 + dispatch 層，確保問題不漏

**Layer 1: Prompt 層（即時捕捉）**

Agent 的 workspace rules 或 task prompt 注入指示：
```
遇到以下情況時，用 task_create 開一張 survey 票（status=backlog, priority=high）：
- 文件缺漏：需要但找不到的 spec、API doc、設計文件
- 需要確認：不確定的業務邏輯、模糊的需求
- 環境問題：缺少的 credentials、config、依賴
- 技術債：發現但目前繞過的問題
票 title 格式：[survey] {問題描述}
票 description 包含：發現的 context、影響範圍、建議的解法（如有）
開完票後繼續做能做的部分，不要停下來等。
```

**Layer 2: Dispatch 層（兜底掃描）**

`postTaskSurvey()` — task done/review 後掃描 output，補抓遺漏：

觸發時機：`dispatchTask()` 完成後，跟 `postTaskGit()` 同區塊
執行流程：
```
1. 掃描 task output 找未解決的問題信號：
   - "TODO", "FIXME", "HACK", "WORKAROUND"
   - "不確定", "需要確認", "找不到文件", "暫時跳過"
   - "missing", "unclear", "assumed", "skipped"
2. 如果有信號且 agent 沒有自己開 survey 票（查 task comments）：
   送 LLM（haiku, budget $0.1）摘要問題 → 自動開 survey 票
3. 如果 agent 已開過 survey 票 → skip（不重複）
```

**Config**：不需要額外 config 欄位。Layer 1 透過 prompt 控制，Layer 2 跟著 task completion 自動跑（只要 taskboard enabled 就生效）。

### 改動檔案清單

| 檔案 | 改動 |
|------|------|
| `taskboard.go` | `TaskBoardConfig` 加 3 欄位 |
| `taskboard_dispatch.go` | `postTaskGit()` + `postTaskSurvey()` 新函數 + 在 L498 區塊呼叫；`idleAnalysis()` 新函數 + 在 scan 無 task 時呼叫 |
| `projects.go` | 可能需要 `getProjectByName()` helper（查 workdir） |
| workspace rules | agent prompt 注入 survey 開票指示（Layer 1） |

**不需要新 Go 檔案**。全部在現有檔案內修改。

### 測試計畫

- `postTaskGit`：mock git commands + 驗證 branch 命名、commit message 格式、skip 邏輯
- `idleAnalysis`：mock LLM + 驗證 backlog 建立、24h cooldown、max 限制
- `postTaskSurvey`：驗證信號偵測、重複 skip、LLM 摘要 → 開票
- Config：驗證 JSON unmarshal 正確、預設 false

### 預估行數

- `postTaskGit()`: ~60 行
- `idleAnalysis()`: ~100 行
- `postTaskSurvey()`: ~70 行
- Config + helpers: ~20 行
- Tests: ~180 行
- **合計: ~430 行**
