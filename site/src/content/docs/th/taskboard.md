---
title: "คู่มือ Taskboard และ Auto-Dispatch"
lang: "th"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# คู่มือ Taskboard และ Auto-Dispatch

## ภาพรวม

Taskboard คือระบบ kanban ในตัวของ Tetora สำหรับติดตามและรัน task โดยอัตโนมัติ ประกอบด้วย task store แบบถาวร (ใช้ SQLite) และ auto-dispatch engine ที่คอยดู task ที่พร้อมและส่งต่อให้ agent โดยไม่ต้องแทรกแซงด้วยตนเอง

กรณีใช้งานทั่วไป:

- จัดคิว backlog ของ engineering task และให้ agent ทำงานผ่านในตอนกลางคืน
- Route task ไปยัง agent ตามความเชี่ยวชาญ (เช่น `kokuyou` สำหรับ backend, `kohaku` สำหรับ content)
- เชื่อม task ด้วยความสัมพันธ์แบบ dependency เพื่อให้ agent รับงานต่อจากที่คนอื่นทิ้งไว้
- รวม task execution กับ git: การสร้าง branch อัตโนมัติ, commit, push และ PR/MR

**ข้อกำหนด:** `taskBoard.enabled: true` ใน `config.json` และ Tetora daemon ต้องรันอยู่

---

## วงจรชีวิตของ Task

Task ไหลผ่านสถานะตามลำดับนี้:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| สถานะ | ความหมาย |
|---|---|
| `idea` | แนวคิดคร่าวๆ ยังไม่ได้ปรับแต่ง |
| `needs-thought` | ต้องการการวิเคราะห์หรือออกแบบก่อนลงมือทำ |
| `backlog` | กำหนดและจัดลำดับแล้ว แต่ยังไม่ได้กำหนดเวลา |
| `todo` | พร้อมรัน — auto-dispatch จะหยิบขึ้นมาหากมี assignee |
| `doing` | กำลังรันอยู่ |
| `review` | การรันเสร็จสิ้น รอ quality review |
| `done` | เสร็จสมบูรณ์และผ่าน review แล้ว |
| `partial-done` | การรันสำเร็จแต่การประมวลผลหลังล้มเหลว (เช่น git merge conflict) สามารถกู้คืนได้ |
| `failed` | การรันล้มเหลวหรือ output ว่างเปล่า จะถูก retry สูงสุด `maxRetries` ครั้ง |

Auto-dispatch หยิบ task ที่มี `status=todo` หาก task ไม่มี assignee จะถูกกำหนดให้ `defaultAgent` โดยอัตโนมัติ (ค่าเริ่มต้น: `ruri`) Task ใน `backlog` จะถูก triage เป็นระยะโดย `backlogAgent` ที่กำหนดค่า (ค่าเริ่มต้น: `ruri`) ซึ่งจะ promote task ที่มีแนวโน้มไปยัง `todo`

---

## การสร้าง Task

### CLI

```bash
# Task ขั้นต่ำ (ลงที่ backlog ไม่มี assignee)
tetora task create --title="Add rate limiting to API"

# พร้อมทุก option
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# แสดงรายการ task
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# แสดง task เฉพาะ
tetora task show task-abc123
tetora task show task-abc123 --full   # รวม comments/thread

# ย้าย task ด้วยตนเอง
tetora task move task-abc123 --status=todo

# กำหนด agent
tetora task assign task-abc123 --assignee=kokuyou

# เพิ่ม comment (ประเภท spec, context, log หรือ system)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

Task ID ถูกสร้างอัตโนมัติในรูปแบบ `task-<uuid>` คุณอ้างอิง task ด้วย ID เต็มหรือ prefix สั้นๆ — CLI จะแนะนำ match ที่ตรงกัน

### HTTP API

```bash
# สร้าง
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# แสดงรายการ (กรองตามสถานะ)
curl "http://localhost:8991/api/tasks?status=todo"

# ย้ายไปสถานะใหม่
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

เปิดแท็บ **Taskboard** ใน dashboard (`http://localhost:8991/dashboard`) Task แสดงในคอลัมน์ kanban ลาก card ระหว่างคอลัมน์เพื่อเปลี่ยนสถานะ คลิก card เพื่อเปิด detail panel พร้อม comments และ diff view

---

## Auto-Dispatch

Auto-dispatch คือ background loop ที่หยิบ task `todo` และรันผ่าน agent

### วิธีการทำงาน

1. Ticker ยิงทุก `interval` (ค่าเริ่มต้น: `5m`)
2. Scanner ตรวจสอบว่ามีกี่ task กำลังรัน ถ้า `activeCount >= maxConcurrentTasks` การ scan จะถูกข้าม
3. สำหรับ task `todo` ทุกตัวที่มี assignee task จะถูก dispatch ไปยัง agent นั้น Task ที่ไม่มี assignee จะถูกกำหนดให้ `defaultAgent` โดยอัตโนมัติ
4. เมื่อ task เสร็จสิ้น re-scan ทันทีจะยิงเพื่อให้ batch ถัดไปเริ่มโดยไม่ต้องรอ interval เต็ม
5. เมื่อ daemon เริ่มต้น task `doing` ที่กำพร้าจาก crash ก่อนหน้าจะถูก restore เป็น `done` (หากมีหลักฐานการสิ้นสุด) หรือ reset เป็น `todo` (หากกำพร้าจริงๆ)

### Dispatch Flow

```
                          ┌─────────┐
                          │  idea   │  (การป้อนแนวคิดด้วยตนเอง)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (ต้องการการวิเคราะห์)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Triage (backlogAgent, ค่าเริ่มต้น: ruri) รันเป็นระยะ:   │
  │   • "ready"     → กำหนด agent → ย้ายไป todo              │
  │   • "decompose" → สร้าง subtask → parent ไปยัง doing     │
  │   • "clarify"   → เพิ่ม comment คำถาม → อยู่ใน backlog   │
  │                                                           │
  │  Fast-path: มี assignee + ไม่มี dep ที่บล็อก              │
  │   → ข้าม LLM triage, promote โดยตรงไปยัง todo            │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Auto-dispatch หยิบ task ทุกรอบ scan:                    │
  │   • มี assignee       → dispatch ไปยัง agent นั้น         │
  │   • ไม่มี assignee    → กำหนด defaultAgent แล้วรัน        │
  │   • มี workflow       → รันผ่าน workflow pipeline          │
  │   • มี dependsOn      → รอจน dep เสร็จ                   │
  │   • มี prev run ที่ resume ได้ → resume จาก checkpoint    │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Agent รัน task (single prompt หรือ workflow DAG)         │
  │                                                           │
  │  Guard: stuckThreshold (ค่าเริ่มต้น 2h)                  │
  │   • หาก workflow ยังรัน → refresh timestamp               │
  │   • หาก stuck จริงๆ    → reset เป็น todo                 │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     success    partial failure  failure
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     ปุ่ม Resume       │  Retry (สูงสุด maxRetries)
           │     ใน dashboard      │  หรือ escalate
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ escalate │
       └────────┘            │ to human │
                             └──────────┘
```

### รายละเอียด Triage

Triage รันทุก `backlogTriageInterval` (ค่าเริ่มต้น: `1h`) และดำเนินการโดย `backlogAgent` (ค่าเริ่มต้น: `ruri`) Agent ได้รับ backlog task ทุกตัวพร้อม comment และรายชื่อ agent ที่มี จากนั้นตัดสินใจ:

| Action | ผล |
|---|---|
| `ready` | กำหนด agent เฉพาะและ promote ไปยัง `todo` |
| `decompose` | สร้าง subtask (พร้อม assignee) parent ย้ายไปยัง `doing` |
| `clarify` | เพิ่มคำถามเป็น comment task อยู่ใน `backlog` ต่อ |

**Fast-path**: Task ที่มี assignee แล้วและไม่มี dependency ที่บล็อกจะข้าม LLM triage และถูก promote ไปยัง `todo` ทันที

### Auto-Assignment

เมื่อ task `todo` ไม่มี assignee dispatcher จะกำหนดให้ `defaultAgent` โดยอัตโนมัติ (กำหนดค่าได้ ค่าเริ่มต้น: `ruri`) เพื่อป้องกัน task ค้างโดยไม่เงียบ flow ทั่วไป:

1. Task ถูกสร้างโดยไม่มี assignee → เข้าสู่ `backlog`
2. Triage promote ไปยัง `todo` (มีหรือไม่มีการกำหนด agent ก็ได้)
3. ถ้า triage ไม่ได้กำหนด → dispatcher กำหนด `defaultAgent`
4. Task รันตามปกติ

### การกำหนดค่า

เพิ่มใน `config.json`:

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| Field | Default | คำอธิบาย |
|---|---|---|
| `enabled` | `false` | เปิดใช้งาน auto-dispatch loop |
| `interval` | `5m` | ความถี่ในการ scan หา task ที่พร้อม |
| `maxConcurrentTasks` | `3` | Task สูงสุดที่รันพร้อมกัน |
| `defaultAgent` | `ruri` | กำหนดให้ task `todo` ที่ไม่มี assignee ก่อน dispatch |
| `backlogAgent` | `ruri` | Agent ที่ review และ promote backlog task |
| `reviewAgent` | `ruri` | Agent ที่ review output ของ task ที่เสร็จสิ้น |
| `escalateAssignee` | `takuma` | ผู้ที่ได้รับมอบหมายเมื่อ auto-review ต้องการการตัดสินของมนุษย์ |
| `stuckThreshold` | `2h` | เวลาสูงสุดที่ task อยู่ใน `doing` ก่อน reset |
| `backlogTriageInterval` | `1h` | ช่วงเวลาขั้นต่ำระหว่างการรัน backlog triage |
| `reviewLoop` | `false` | เปิดใช้งาน Dev↔QA loop (รัน → review → แก้ไข สูงสุด `maxRetries`) |
| `maxBudget` | ไม่จำกัด | ค่าใช้จ่ายสูงสุดต่อ task เป็น USD |
| `defaultModel` | — | Override model สำหรับ task ที่ auto-dispatch ทั้งหมด |

---

## Slot Pressure

Slot pressure ป้องกัน auto-dispatch จากการใช้ slot concurrency ทั้งหมดและทำให้ interactive session ขาดแคลน (ข้อความ chat ของมนุษย์, on-demand dispatch)

เปิดใช้งานใน `config.json`:

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| Field | Default | คำอธิบาย |
|---|---|---|
| `reservedSlots` | `2` | Slot ที่เก็บไว้สำหรับใช้งาน interactive Task ที่ไม่ interactive ต้องรอหาก slot ว่างลดลงถึงระดับนี้ |
| `warnThreshold` | `3` | การเตือนยิงเมื่อ slot ว่างลดลงถึงระดับนี้ ข้อความ "排程接近滿載" ปรากฏใน dashboard และ notification channel |
| `nonInteractiveTimeout` | `5m` | นานเท่าใดที่ non-interactive task รอ slot ก่อนถูกยกเลิก |

แหล่ง interactive (human chat, `tetora dispatch`, `tetora route`) จะได้ slot ทันทีเสมอ แหล่งพื้นหลัง (taskboard, cron) จะรอหาก pressure สูง

---

## Git Integration

เมื่อ `gitCommit`, `gitPush` และ `gitPR` เปิดใช้งาน dispatcher จะรัน git operation หลัง task เสร็จสมบูรณ์

**การตั้งชื่อ branch** ถูกควบคุมโดย `gitWorkflow.branchConvention`:

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

template เริ่มต้น `{type}/{agent}-{description}` สร้าง branch เช่น `feat/kokuyou-add-rate-limiting` ส่วน `{description}` ได้มาจาก task title (แปลงเป็นตัวพิมพ์เล็ก แทนที่ช่องว่างด้วย hyphen ตัดให้สูงสุด 40 ตัวอักษร)

field `type` ของ task กำหนด branch prefix หาก task ไม่มี type จะใช้ `defaultType`

**Auto PR/MR** รองรับทั้ง GitHub (`gh`) และ GitLab (`glab`) binary ที่มีอยู่บน `PATH` จะถูกใช้โดยอัตโนมัติ

---

## Worktree Mode

เมื่อ `gitWorktree: true` แต่ละ task จะรันใน git worktree ที่แยกออกมาแทนที่จะเป็น working directory ร่วมกัน ซึ่งขจัด file conflict เมื่อหลาย task รันพร้อมกันบน repository เดียวกัน

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← สำเนาที่แยกออกมาสำหรับ task นี้
  task-def456/   ← สำเนาที่แยกออกมาสำหรับ task นี้
```

เมื่อ task เสร็จสิ้น:

- ถ้า `autoMerge: true` (ค่าเริ่มต้น) worktree branch จะถูก merge กลับไปยัง `main` และ worktree จะถูกลบ
- ถ้า merge ล้มเหลว task จะย้ายไปยังสถานะ `partial-done` worktree จะถูกเก็บไว้สำหรับการแก้ไขด้วยตนเอง

การกู้คืนจาก `partial-done`:

```bash
# ดูรายละเอียดว่าเกิดอะไรขึ้น
tetora task show task-abc123 --full

# Merge branch ด้วยตนเอง
git merge feat/kokuyou-add-rate-limiting

# แก้ conflict ใน editor แล้ว commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# ทำเครื่องหมาย task ว่าเสร็จ
tetora task move task-abc123 --status=done
```

---

## Workflow Integration

Task สามารถรันผ่าน workflow pipeline แทนที่จะเป็น single agent prompt ซึ่งมีประโยชน์เมื่อ task ต้องการหลายขั้นตอนที่ประสานกัน (เช่น research → implement → test → document)

กำหนด workflow ให้ task:

```bash
# ตั้งค่าตอนสร้าง task
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# หรืออัปเดต task ที่มีอยู่
tetora task update task-abc123 --workflow=engineering-pipeline
```

หากต้องการปิด default workflow ระดับ board สำหรับ task เฉพาะ:

```json
{ "workflow": "none" }
```

Default workflow ระดับ board ใช้กับ task ที่ auto-dispatch ทั้งหมดยกเว้นจะมีการ override:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

field `workflowRunId` บน task เชื่อมโยงไปยังการรัน workflow เฉพาะ ซึ่งมองเห็นได้ในแท็บ Workflows ของ dashboard

---

## มุมมอง Dashboard

เปิด dashboard ที่ `http://localhost:8991/dashboard` และไปที่แท็บ **Taskboard**

**Kanban board** — คอลัมน์สำหรับแต่ละสถานะ Card แสดง title, assignee, priority badge และค่าใช้จ่าย ลากเพื่อเปลี่ยนสถานะ

**Task detail panel** — คลิก card ใดก็ได้เพื่อเปิด แสดง:
- คำอธิบายเต็มและ comment ทั้งหมด (spec, context, log entry)
- Session link (กระโดดไปยัง live worker terminal หากยังรันอยู่)
- ค่าใช้จ่าย, ระยะเวลา, จำนวน retry
- Workflow run link หากใช้

**Diff review panel** — เมื่อ `requireReview: true` task ที่เสร็จสิ้นจะปรากฏในคิว review ผู้ review เห็น diff ของการเปลี่ยนแปลงและสามารถอนุมัติหรือขอแก้ไขได้

---

## เคล็ดลับ

**การขนาด Task** เก็บ task ไว้ในขอบเขต 30–90 นาที Task ที่ใหญ่เกินไป (refactor หลายวัน) มักจะ timeout หรือสร้าง output ว่างเปล่าและถูกทำเครื่องหมายว่าล้มเหลว แบ่งเป็น subtask โดยใช้ field `parentId`

**ขีดจำกัด concurrent dispatch** `maxConcurrentTasks: 3` คือค่าเริ่มต้นที่ปลอดภัย การเพิ่มเกินจำนวน API connection ที่ provider ของคุณรองรับทำให้เกิด contention และ timeout เริ่มที่ 3 เพิ่มเป็น 5 เฉพาะหลังจากยืนยันพฤติกรรมที่เสถียรแล้ว

**การกู้คืน partial-done** ถ้า task เข้า `partial-done` หมายความว่า agent ทำงานเสร็จสมบูรณ์แล้ว — แค่ขั้นตอน git merge ล้มเหลว แก้ conflict ด้วยตนเองแล้วย้าย task เป็น `done` ข้อมูลค่าใช้จ่ายและ session ถูกเก็บไว้

**การใช้ `dependsOn`** Task ที่มี dependency ที่ยังไม่ครบจะถูก dispatcher ข้ามไปจนกว่า task ID ทั้งหมดในรายการถึงสถานะ `done` ผลลัพธ์ของ upstream task จะถูก inject เข้า prompt ของ task ที่ขึ้นอยู่โดยอัตโนมัติภายใต้ "Previous Task Results"

**Backlog triage** `backlogAgent` อ่าน task ทุกตัวใน `backlog` ประเมินความเป็นไปได้และลำดับความสำคัญ และ promote task ที่ชัดเจนไปยัง `todo` เขียนคำอธิบายและ acceptance criteria อย่างละเอียดใน backlog task ของคุณ — triage agent ใช้สิ่งเหล่านี้ตัดสินใจว่าจะ promote หรือปล่อยให้มนุษย์ review

**Retry และ review loop** เมื่อ `reviewLoop: false` (ค่าเริ่มต้น) task ที่ล้มเหลวจะถูก retry สูงสุด `maxRetries` ครั้งพร้อม log comment ก่อนหน้าที่ inject เข้าไป เมื่อ `reviewLoop: true` การรันแต่ละครั้งจะถูก review โดย `reviewAgent` ก่อนถือว่าเสร็จ — agent จะได้รับ feedback และลองอีกครั้งหากพบปัญหา
