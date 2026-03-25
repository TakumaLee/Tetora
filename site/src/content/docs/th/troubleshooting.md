---
title: "คู่มือการแก้ปัญหา"
lang: "th"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# คู่มือการแก้ปัญหา

คู่มือนี้ครอบคลุมปัญหาที่พบบ่อยที่สุดเมื่อรัน Tetora สำหรับแต่ละปัญหาสาเหตุที่น่าจะเป็นไปได้มากที่สุดจะถูกแสดงก่อน

---

## tetora doctor

เริ่มต้นที่นี่เสมอ รัน `tetora doctor` หลังติดตั้งหรือเมื่อมีบางอย่างหยุดทำงาน:

```
=== Tetora Doctor ===

  ✓ Config          /Users/you/.tetora/config.json
  ✓ Claude CLI      claude 1.2.3
  ✓ Provider        claude-cli
  ✓ Port            localhost:8991 in use (daemon running)
  ✓ Telegram        enabled (chatID=123456)
  ✓ Jobs            jobs.json (4 jobs, 3 enabled)
  ✓ History DB      12 tasks
  ✓ Workdir         /Users/you/dev
  ✓ Agent/ruri      Commander
  ✓ Binary          /Users/you/.tetora/bin/tetora
  ✓ Encryption      key configured
  ✓ ffmpeg          available
  ✓ sqlite3         available
  ✓ Agents Dir      /Users/you/.tetora/agents (3 agents)
  ✓ Workspace       /Users/you/.tetora/workspace

All checks passed.
```

แต่ละบรรทัดคือการตรวจสอบหนึ่งรายการ `✗` สีแดงหมายถึงความล้มเหลวหนัก (daemon จะไม่ทำงานหากไม่แก้ไข) `~` สีเหลืองหมายถึงคำแนะนำ (เป็นตัวเลือกแต่แนะนำ)

การแก้ไขทั่วไปสำหรับการตรวจสอบที่ล้มเหลว:

| การตรวจสอบที่ล้มเหลว | การแก้ไข |
|---|---|
| `Config: not found` | รัน `tetora init` |
| `Claude CLI: not found` | ตั้งค่า `claudePath` ใน `config.json` หรือติดตั้ง Claude Code |
| `sqlite3: not found` | `brew install sqlite3` (macOS) หรือ `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | สร้าง `~/.tetora/agents/{name}/SOUL.md` หรือรัน `tetora init` |
| `Workspace: not found` | รัน `tetora init` เพื่อสร้างโครงสร้าง directory |

---

## "session produced no output"

Task เสร็จสิ้นแต่ output ว่างเปล่า Task ถูกทำเครื่องหมายว่า `failed` โดยอัตโนมัติ

**สาเหตุที่ 1: Context window ใหญ่เกินไป** Prompt ที่ inject เข้า session เกินขีดจำกัด context ของ model Claude Code จะออกทันทีเมื่อไม่สามารถใส่ context ได้

แก้ไข: เปิดใช้งาน session compaction ใน `config.json`:

```json
{
  "sessionCompaction": {
    "enabled": true,
    "tokenThreshold": 150000,
    "messageThreshold": 100,
    "strategy": "auto"
  }
}
```

หรือลด context ที่ inject เข้า task (คำอธิบายสั้นลง, spec comment น้อยลง, chain `dependsOn` ขนาดเล็กลง)

**สาเหตุที่ 2: Claude Code CLI เริ่มต้นล้มเหลว** Binary ที่ `claudePath` crash เมื่อเริ่มต้น — โดยทั่วไปเนื่องจาก API key ผิด, network issue หรือ version ไม่ตรงกัน

แก้ไข: รัน Claude Code binary ด้วยตนเองเพื่อดู error:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

แก้ไข error ที่รายงาน จากนั้น retry task:

```bash
tetora task move task-abc123 --status=todo
```

**สาเหตุที่ 3: Prompt ว่างเปล่า** Task มี title แต่ไม่มีคำอธิบาย และ title เพียงอย่างเดียวคลุมเครือเกินไปสำหรับ agent ที่จะดำเนินการได้ Session รัน สร้าง output ที่ไม่ผ่านการตรวจสอบ empty และถูกทำเครื่องหมาย

แก้ไข: เพิ่มคำอธิบายที่เจาะจง:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## Error "unauthorized" บน dashboard

Dashboard ส่งคืน 401 หรือแสดงหน้าว่างหลัง reload

**สาเหตุที่ 1: Service Worker cache token auth เก่า** PWA Service Worker cache response รวมถึง auth header หลัง daemon restart ด้วย token ใหม่ เวอร์ชันที่ cache ไว้จะ stale

แก้ไข: Hard refresh หน้า ใน Chrome/Safari:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

หรือเปิด DevTools → Application → Service Workers → คลิก "Unregister" แล้ว reload

**สาเหตุที่ 2: Referer header ไม่ตรงกัน** auth middleware ของ dashboard ตรวจสอบ `Referer` header Request จาก browser extension, proxy หรือ curl ที่ไม่มี `Referer` header จะถูกปฏิเสธ

แก้ไข: เข้าถึง dashboard โดยตรงที่ `http://localhost:8991/dashboard` ไม่ใช่ผ่าน proxy หากต้องการเข้าถึง API จาก external tool ใช้ API token แทน browser session auth

---

## Dashboard ไม่อัปเดต

Dashboard โหลดแต่ activity feed, worker list หรือ task board ยังคง stale

**สาเหตุ: Service Worker version ไม่ตรงกัน** PWA Service Worker ให้บริการเวอร์ชัน cache ของ dashboard JS/HTML แม้หลัง `make bump` อัปเดต

แก้ไข:

1. Hard refresh (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. ถ้าไม่ได้ผล เปิด DevTools → Application → Service Workers → คลิก "Update" หรือ "Unregister"
3. Reload หน้า

**สาเหตุ: SSE connection หลุด** Dashboard รับการอัปเดตแบบ live ผ่าน Server-Sent Events ถ้า connection หลุด (network hiccup, laptop sleep) feed จะหยุดอัปเดต

แก้ไข: Reload หน้า SSE connection จะ re-establish อัตโนมัติเมื่อโหลดหน้า

---

## คำเตือน "排程接近滿載"

คุณเห็นข้อความนี้ใน Discord/Telegram หรือ dashboard notification feed

นี่คือคำเตือน slot pressure ยิงเมื่อ slot concurrency ที่ว่างลดลงถึงหรือต่ำกว่า `warnThreshold` (ค่าเริ่มต้น: 3) หมายความว่า Tetora รันใกล้ความจุ

**สิ่งที่ต้องทำ:**

- ถ้านี่คือที่คาดหวัง (task จำนวนมากรันอยู่): ไม่จำเป็นต้องดำเนินการ คำเตือนเป็นข้อมูลเท่านั้น
- ถ้าคุณไม่ได้รัน task จำนวนมาก: ตรวจสอบ task ที่ค้างในสถานะ `doing`:

```bash
tetora task list --status=doing
```

- ถ้าต้องการเพิ่มความจุ: เพิ่ม `maxConcurrent` ใน `config.json` และปรับ `slotPressure.warnThreshold` ตามนั้น
- ถ้า interactive session ถูกดีเลย์: เพิ่ม `slotPressure.reservedSlots` เพื่อเก็บ slot ไว้มากขึ้นสำหรับการใช้งาน interactive

---

## Task ค้างใน "doing"

Task แสดง `status=doing` แต่ไม่มี agent กำลังทำงานจริงๆ

**สาเหตุที่ 1: Daemon restart ระหว่าง task** Task กำลังรันเมื่อ daemon ถูก kill เมื่อ startup ถัดไป Tetora ตรวจสอบ task `doing` ที่กำพร้าและ restore เป็น `done` (หากมีหลักฐานค่าใช้จ่าย/ระยะเวลา) หรือ reset เป็น `todo`

นี่เป็นอัตโนมัติ — รอ daemon startup ถัดไป ถ้า daemon รันอยู่แล้วและ task ยังค้าง heartbeat หรือการ reset task ที่ค้างจะจัดการภายใน `stuckThreshold` (ค่าเริ่มต้น: 2h)

หากต้องการ force reset ทันที:

```bash
tetora task move task-abc123 --status=todo
```

**สาเหตุที่ 2: Heartbeat/stall detection** heartbeat monitor (`heartbeat.go`) ตรวจสอบ session ที่รันอยู่ ถ้า session ไม่มี output ในช่วง stall threshold จะถูกยกเลิกอัตโนมัติและ task ย้ายไปยัง `failed`

ตรวจสอบ task comment สำหรับ system comment `[auto-reset]` หรือ `[stall-detected]`:

```bash
tetora task show task-abc123 --full
```

**ยกเลิกด้วยตนเองผ่าน API:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Worktree merge ล้มเหลว

Task เสร็จสิ้นและย้ายไปยัง `partial-done` พร้อม comment เช่น `[worktree] merge failed`

หมายความว่าการเปลี่ยนแปลงของ agent บน task branch ขัดแย้งกับ `main`

**ขั้นตอนการกู้คืน:**

```bash
# ดูรายละเอียด task และ branch ที่ถูกสร้าง
tetora task show task-abc123 --full

# ไปยัง project repo
cd /path/to/your/repo

# Merge branch ด้วยตนเอง
git merge feat/kokuyou-task-abc123

# แก้ conflict ใน editor แล้ว commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# ทำเครื่องหมาย task ว่าเสร็จ
tetora task move task-abc123 --status=done
```

worktree directory ถูกเก็บไว้ที่ `~/.tetora/runtime/worktrees/task-abc123/` จนกว่าคุณจะล้างด้วยตนเองหรือย้าย task เป็น `done`

---

## Token มีค่าใช้จ่ายสูง

Session ใช้ token มากกว่าที่คาดไว้

**สาเหตุที่ 1: Context ไม่ถูก compact** หากไม่มี session compaction แต่ละรอบจะสะสมประวัติการสนทนาเต็ม task ที่รันนาน (tool call จำนวนมาก) จะทำให้ context โตแบบเส้นตรง

แก้ไข: เปิดใช้งาน `sessionCompaction` (ดูส่วน "session produced no output" ด้านบน)

**สาเหตุที่ 2: ไฟล์ knowledge base หรือ rule ขนาดใหญ่** ไฟล์ใน `workspace/rules/` และ `workspace/knowledge/` จะถูก inject เข้า agent prompt ทุกตัว ถ้าไฟล์เหล่านี้ใหญ่ มันจะบริโภค token ในทุกการเรียก

แก้ไข:
- ตรวจสอบ `workspace/knowledge/` — เก็บไฟล์แต่ละไฟล์ให้ต่ำกว่า 50 KB
- ย้ายเนื้อหาอ้างอิงที่คุณไม่ค่อยต้องการออกจาก auto-inject path
- รัน `tetora knowledge list` เพื่อดูสิ่งที่ถูก inject และขนาดของมัน

**สาเหตุที่ 3: Model routing ผิด** model ที่มีราคาแพง (Opus) กำลังถูกใช้สำหรับ task ทั่วไป

แก้ไข: ตรวจสอบ `defaultModel` ใน agent config และตั้งค่า default ที่ถูกกว่าสำหรับ bulk task:

```json
{
  "taskBoard": {
    "autoDispatch": {
      "defaultModel": "sonnet"
    }
  }
}
```

---

## Error timeout ของ Provider

Task ล้มเหลวด้วย timeout error เช่น `context deadline exceeded` หรือ `provider request timed out`

**สาเหตุที่ 1: Task timeout สั้นเกินไป** timeout เริ่มต้นอาจสั้นเกินไปสำหรับ task ที่ซับซ้อน

แก้ไข: ตั้งค่า timeout ที่ยาวขึ้นใน agent config ของ task หรือต่อ task:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

หรือเพิ่มการประมาณ LLM timeout โดยเพิ่มรายละเอียดในคำอธิบาย task (Tetora ใช้คำอธิบายในการประมาณ timeout ผ่านการเรียก model เร็ว)

**สาเหตุที่ 2: API rate limiting หรือ contention** Request พร้อมกันมากเกินไปที่ส่งไปยัง provider เดียวกัน

แก้ไข: ลด `maxConcurrentTasks` หรือเพิ่ม `maxBudget` เพื่อ throttle task ที่มีค่าใช้จ่ายสูง:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` รบกวน workflow

คุณรัน `make bump` ขณะที่ workflow หรือ task กำลังรัน daemon restart ระหว่าง task

การ restart จะ trigger logic orphan recovery ของ Tetora:

- Task ที่มีหลักฐานการสิ้นสุด (บันทึกค่าใช้จ่าย, บันทึกระยะเวลา) จะถูก restore เป็น `done`
- Task ที่ไม่มีหลักฐานการสิ้นสุดแต่เกิน grace period (2 นาที) จะถูก reset เป็น `todo` สำหรับ re-dispatch
- Task ที่อัปเดตภายใน 2 นาทีที่ผ่านมาจะถูกปล่อยไว้จนถึง stuck-task scan ถัดไป

**หากต้องการตรวจสอบสิ่งที่เกิดขึ้น:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

ตรวจสอบ task comment สำหรับ entry `[auto-restore]` หรือ `[auto-reset]`

**ถ้าต้องการป้องกันการ bump ระหว่าง task ที่ active** (ยังไม่มีเป็น flag) ตรวจสอบว่าไม่มี task รันก่อน bump:

```bash
tetora task list --status=doing
# ถ้าว่าง ปลอดภัยที่จะ bump
make bump
```

---

## SQLite error

คุณเห็น error เช่น `database is locked`, `SQLITE_BUSY` หรือ `index.lock` ใน log

**สาเหตุที่ 1: ขาด WAL mode pragma** หากไม่มี WAL mode SQLite ใช้ exclusive file locking ซึ่งทำให้เกิด `database is locked` ภายใต้การอ่าน/เขียนพร้อมกัน

การเรียก DB ของ Tetora ทั้งหมดผ่าน `queryDB()` และ `execDB()` ซึ่งนำหน้าด้วย `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;` ถ้าคุณเรียก sqlite3 โดยตรงใน script ให้เพิ่ม pragma เหล่านี้:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**สาเหตุที่ 2: ไฟล์ `index.lock` stale** git operation ทิ้ง `index.lock` ไว้เมื่อถูกขัดจังหวะ worktree manager ตรวจสอบ lock stale ก่อนเริ่ม git work แต่ crash สามารถทิ้งไว้ได้

แก้ไข:

```bash
# ค้นหาไฟล์ lock stale
find ~/.tetora/runtime/worktrees -name "index.lock"

# ลบออก (เฉพาะเมื่อไม่มี git operation กำลังรันอยู่)
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram ไม่ตอบสนอง

ข้อความไปยัง bot ไม่มีการตอบกลับ

**สาเหตุที่ 1: Channel configuration ผิด** Discord มี channel list สองรายการ: `channelIDs` (ตอบกลับข้อความทั้งหมดโดยตรง) และ `mentionChannelIDs` (ตอบกลับเฉพาะเมื่อถูก @กล่าวถึง) ถ้า channel ไม่อยู่ในรายการใดรายการหนึ่ง ข้อความจะถูกละเว้น

แก้ไข: ตรวจสอบ `config.json`:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**สาเหตุที่ 2: Bot token หมดอายุหรือผิด** Telegram bot token ไม่หมดอายุ แต่ Discord token สามารถถูก invalidate ได้หาก bot ถูก kick ออกจาก server หรือ token ถูกสร้างใหม่

แก้ไข: สร้าง bot token ใหม่ใน Discord developer portal และอัปเดต `config.json`

**สาเหตุที่ 3: Daemon ไม่รัน** Bot gateway ทำงานเฉพาะเมื่อ `tetora serve` รันอยู่

แก้ไข:

```bash
tetora status
tetora serve   # ถ้าไม่รัน
```

---

## Error จาก glab / gh CLI

Git integration ล้มเหลวด้วย error จาก `glab` หรือ `gh`

**Error ทั่วไป: `gh: command not found`**

แก้ไข:
```bash
brew install gh      # macOS
gh auth login        # authenticate
```

**Error ทั่วไป: `glab: You are not logged in`**

แก้ไข:
```bash
brew install glab    # macOS
glab auth login      # authenticate กับ GitLab instance ของคุณ
```

**Error ทั่วไป: `remote: HTTP Basic: Access denied`**

แก้ไข: ตรวจสอบว่า SSH key หรือ HTTPS credential ถูกกำหนดค่าสำหรับ repository host สำหรับ GitLab:

```bash
glab auth status
ssh -T git@gitlab.com   # ทดสอบ SSH connectivity
```

สำหรับ GitHub:

```bash
gh auth status
ssh -T git@github.com
```

**การสร้าง PR/MR สำเร็จแต่ชี้ไปยัง base branch ผิด**

ค่าเริ่มต้น PR จะ target default branch ของ repository (`main` หรือ `master`) ถ้า workflow ของคุณใช้ base ต่างกัน ตั้งค่าอย่างชัดเจนใน post-task git configuration หรือตรวจสอบว่า default branch ของ repository ถูกกำหนดค่าอย่างถูกต้องบน hosting platform
