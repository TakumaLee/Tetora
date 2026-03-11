# Dashboard Visibility Fix — Templates, Skills, Tools

> Status: Draft
> Priority: High (UX 基本功能)
> Estimated: 1-2 sessions

## Problem

三個核心功能藏太深或只有半成品 UI：

| 功能 | 現狀 | 問題 |
|---|---|---|
| Template Gallery | Workflows tab 裡的摺疊區塊 | 要手動點開才看到，大多數人不會注意 |
| Skill List | 只在 workflow editor step 下拉選單出現 | 沒有獨立頁面，看不到全貌、無法管理 |
| Tool List | Settings → Tools & Security 只顯示「Total: N」 | 只有一個數字，完全沒有列表 |

用戶打開 dashboard 後，應該能一眼看到系統有什麼能力（tools）、什麼技能（skills）、什麼模板（templates）。

---

## Solution: Operations 新增 Capabilities sub-tab

### 方案：在 Operations 下新增 sub-tab「Capabilities」

```
Operations
├── Agents          (現有)
├── Workflows       (現有)
├── Tasks           (現有)
├── Capabilities    (新增) ← Templates + Skills + Tools 整合頁
└── Files           (現有)
```

**Why one tab instead of three?** 三個各自量不大（tools ~20、skills ~10、templates ~36），分三個 tab 太碎。合一頁三區塊，一眼掌握系統能力全貌。

---

## Design

### Layout: 三個可摺疊 Section

```
┌─────────────────────────────────────────┐
│ 🔧 Tools (23)                      [▼] │
│ ┌──────┬──────┬──────┬──────┬──────┐    │
│ │ web  │ bash │ file │ sql  │ ...  │    │
│ │search│ exec │ read │query │      │    │
│ └──────┴──────┴──────┴──────┴──────┘    │
│                                         │
│ 🎯 Skills (8)                      [▼] │
│ ┌──────────┬──────────┬──────────┐      │
│ │ deploy   │ review   │ triage   │      │
│ │ ✅ approved        │ ⏳ pending│      │
│ └──────────┴──────────┴──────────┘      │
│                                         │
│ 📘 Templates (36)          [Filter: __] │
│ ┌──────────┬──────────┬──────────┐      │
│ │ standard │ content  │ order    │      │
│ │ -dev     │ -publish │ -dispute │      │
│ │ 5 steps  │ 4 steps  │ 6 steps  │      │
│ │ [Preview][Install]  │          │      │
│ └──────────┴──────────┴──────────┘      │
└─────────────────────────────────────────┘
```

### Section 1: Tools

**Data source:** `GET /api/tools`

**Card content:**
- Name (bold)
- Description (truncated, 1 line)
- Badge: `builtin` or `custom`
- Badge: `auth` if requireAuth

**Actions:** Read-only (tools are code-registered, not user-editable)

### Section 2: Skills

**Data source:** `GET /api/skills/store` (includes pending + approved)

**Card content:**
- Name (bold)
- Description
- Status badge: ✅ Approved / ⏳ Pending / 🔒 Sandbox
- Usage count + last used
- Created by (agent name)

**Actions:**
- Approve / Reject (for pending skills)
- Delete
- View usage history

### Section 3: Templates

**Data source:** `GET /api/templates`

Move existing template gallery from workflow-editor.js here. Same grid, same filter, same preview/install — just relocated.

**Card content:** (same as current)
- Name, description, step count, variable count, category badge
- Preview / Install buttons

**Filter:** Category dropdown + text search (same as current `filterTemplates()`)

---

## Implementation Plan

### Phase 1: HTML Structure

**File: `dashboard/body.html`**

1. Add sidebar nav item for Capabilities (between Tasks and Files):
   ```html
   <button id="tab-operations-capabilities" data-tab="operations" data-sub="capabilities"
     onclick="switchTab('operations');switchSubTab('operations','capabilities')">
     <span class="nav-icon">&#x1F9E9;</span>Capabilities
   </button>
   ```

2. Add sub-tab button in operations sub-nav (after tasks, before files)

3. Add content section `<div id="operations-sub-capabilities">` with three collapsible sections

### Phase 2: JavaScript — New file `dashboard/capabilities.js`

**Functions:**
- `refreshCapabilities()` — called by `refreshOperationsSubTab('capabilities')`
- `loadToolsList()` — fetch `/api/tools`, render card grid
- `loadSkillsList()` — fetch `/api/skills/store`, render card grid with actions
- `loadTemplatesList()` — fetch `/api/templates`, render grid (reuse existing `renderTemplateGrid` logic)
- `approveSkill(name)` / `rejectSkill(name)` / `deleteSkill(name)` — skill management
- `filterCapabilities()` — text search across all three sections
- `toggleCapSection(section)` — collapse/expand individual sections

### Phase 3: CSS

**File: `dashboard/style.css`**

- `.cap-section` — collapsible section container
- `.cap-grid` — responsive card grid (`repeat(auto-fill, minmax(200px, 1fr))`)
- `.cap-card` — card styling (reuse kanban-card-like aesthetic)
- `.cap-badge` — status badges

### Phase 4: Wiring

**File: `dashboard/dispatch.js`**

- Add `'capabilities'` to `refreshOperationsSubTab()` switch case
- Call `refreshCapabilities()`

**File: `Makefile` (or dashboard build)**

- Include `capabilities.js` in the built dashboard.html

### Phase 5: Cleanup

- Remove template gallery from `workflow-editor.js` section (or keep as secondary access point)
- Remove tool count from Settings → Tools & Security (or keep as summary, link to Capabilities)

---

## Files Changed

| File | Changes |
|---|---|
| `dashboard/body.html` | Sidebar nav item, sub-tab button, capabilities content section |
| `dashboard/capabilities.js` | **New file** — all capabilities rendering logic |
| `dashboard/style.css` | Card grid + section styling |
| `dashboard/dispatch.js` | Wire refreshCapabilities to sub-tab switching |
| `Makefile` | Include capabilities.js in build |

## What's NOT Changing

- Backend APIs (all endpoints already exist)
- Tool registration logic
- Skill creation/approval flow
- Template embed system
- Workflow editor (template gallery stays as secondary access)

## Verification

1. Click Capabilities in sidebar → see three sections with real data
2. Tools section shows all registered tools with descriptions
3. Skills section shows approved + pending skills with action buttons
4. Templates section shows all 36 templates with filter + preview + install
5. Approve/reject a skill → status updates live
6. Install a template → navigates to workflow editor
7. All three sections collapse/expand independently
