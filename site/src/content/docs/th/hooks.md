---
title: "การ Integration กับ Claude Code Hooks"
lang: "th"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# การ Integration กับ Claude Code Hooks

## ภาพรวม

Claude Code Hooks คือระบบ event ที่ built-in อยู่ใน Claude Code ซึ่งยิง shell command ในจุดสำคัญระหว่าง session Tetora ลงทะเบียนตัวเองเป็น hook receiver เพื่อให้สามารถสังเกต agent session ที่รันอยู่ทุกตัวแบบ real-time โดยไม่ต้อง polling ไม่ต้องใช้ tmux และไม่ต้อง inject wrapper script

**สิ่งที่ hook เปิดใช้งานได้:**

- การติดตาม progress แบบ real-time ใน dashboard (tool call, session state, รายการ worker แบบ live)
- การ monitor ค่าใช้จ่ายและ token ผ่าน statusline bridge
- การ audit การใช้ tool (tool ไหนรัน ใน session ไหน ใน directory ไหน)
- การตรวจจับการสิ้นสุด session และการอัปเดต task status อัตโนมัติ
- Plan mode gate: ระงับ `ExitPlanMode` จนกว่ามนุษย์จะอนุมัติแผนใน dashboard
- Question routing แบบ interactive: `AskUserQuestion` จะถูก redirect ไปยัง MCP bridge เพื่อให้คำถามปรากฏบน chat platform แทนที่จะ block terminal

Hooks คือทางการ integration ที่แนะนำตั้งแต่ Tetora v2.0 แนวทาง tmux เดิม (v1.x) ยังใช้งานได้แต่ไม่รองรับฟีเจอร์ที่ต้องใช้ hook อย่าง plan gate และ question routing

---

## สถาปัตยกรรม

```
Claude Code session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan gate: long-poll จนกว่าจะได้รับการอนุมัติ
  │   (AskUserQuestion)               └─► Deny: redirect ไปยัง MCP bridge
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► อัปเดต worker state
  │                                   └─► ตรวจจับการเขียนไฟล์ plan
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► ทำเครื่องหมาย worker ว่าเสร็จแล้ว
  │                                   └─► Trigger การสิ้นสุด task
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► ส่งต่อไปยัง Discord/Telegram
```

Hook command คือการเรียก curl ขนาดเล็กที่ inject เข้าไปใน `~/.claude/settings.json` ของ Claude Code ทุก event จะถูก post ไปยัง `POST /api/hooks/event` บน Tetora daemon ที่รันอยู่

---

## การตั้งค่า

### ติดตั้ง hooks

เมื่อ Tetora daemon รันอยู่:

```bash
tetora hooks install
```

คำสั่งนี้เขียน entry เข้า `~/.claude/settings.json` และสร้าง MCP bridge config ที่ `~/.tetora/mcp/bridge.json`

ตัวอย่างสิ่งที่จะถูกเขียนเข้า `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST http://localhost:8991/api/hooks/event -H 'Content-Type: application/json' -d @-"
          }
        ]
      }
    ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "Notification": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [ { "type": "command", "command": "...", "timeout": 600 } ]
      },
      {
        "matcher": "AskUserQuestion",
        "hooks": [ { "type": "command", "command": "..." } ]
      }
    ]
  }
}
```

### ตรวจสอบสถานะ

```bash
tetora hooks status
```

Output แสดง hook ไหนถูกติดตั้ง จำนวน Tetora rule ที่ลงทะเบียน และจำนวน event รวมที่ได้รับตั้งแต่ daemon เริ่มต้น

ตรวจสอบจาก dashboard ได้เช่นกัน: **Engineering Details → Hooks** แสดงสถานะเดียวกันพร้อม live event feed

### ลบ hooks

```bash
tetora hooks remove
```

ลบ entry ของ Tetora ทั้งหมดจาก `~/.claude/settings.json` hook ที่ไม่ใช่ของ Tetora จะถูกเก็บไว้

---

## Hook Events

### PostToolUse

ยิงหลัง tool call ทุกครั้งเสร็จสิ้น Tetora ใช้เพื่อ:

- ติดตาม tool ที่ agent ใช้งาน (`Bash`, `Write`, `Edit`, `Read` เป็นต้น)
- อัปเดต `lastTool` และ `toolCount` ของ worker ใน live workers list
- ตรวจจับเมื่อ agent เขียนไฟล์ plan (trigger การอัปเดต plan cache)

### Stop

ยิงเมื่อ Claude Code session สิ้นสุด (การสิ้นสุดตามปกติหรือการยกเลิก) Tetora ใช้เพื่อ:

- ทำเครื่องหมาย worker ว่า `done` ใน live workers list
- เผยแพร่ completion SSE event ไปยัง dashboard
- Trigger การอัปเดต task status ปลายทางสำหรับ task ของ taskboard

### Notification

ยิงเมื่อ Claude Code ส่งการแจ้งเตือน (เช่น ต้องขอสิทธิ์ หยุดนาน) Tetora ส่งต่อสิ่งเหล่านี้ไปยัง Discord/Telegram และเผยแพร่ไปยัง dashboard SSE stream

### PreToolUse: ExitPlanMode (plan gate)

เมื่อ agent กำลังจะออกจาก plan mode Tetora จะ intercept event ด้วย long-poll (timeout: 600 วินาที) เนื้อหาแผนจะถูก cache และแสดงใน dashboard ภายใต้มุมมองรายละเอียดของ session

มนุษย์สามารถอนุมัติหรือปฏิเสธแผนจาก dashboard ได้ หากอนุมัติ hook จะ return และ Claude Code จะดำเนินต่อ หากปฏิเสธ (หรือ timeout หมด) การออกจะถูกบล็อกและ Claude Code จะอยู่ใน plan mode ต่อไป

### PreToolUse: AskUserQuestion (question routing)

เมื่อ Claude Code พยายามถามคำถามผู้ใช้แบบ interactive Tetora จะ intercept และปฏิเสธพฤติกรรมเริ่มต้น คำถามจะถูก route ผ่าน MCP bridge ไปปรากฏบน chat platform ที่กำหนดค่าไว้ (Discord, Telegram เป็นต้น) เพื่อให้คุณตอบได้โดยไม่ต้องนั่งอยู่หน้า terminal

---

## การติดตาม Progress แบบ Real-Time

เมื่อ hook ถูกติดตั้งแล้ว panel **Workers** ของ dashboard จะแสดง session แบบ live:

| Field | แหล่งที่มา |
|---|---|
| Session ID | `session_id` ใน hook event |
| State | `working` / `idle` / `done` |
| Last tool | ชื่อ tool ล่าสุดจาก `PostToolUse` |
| Working directory | `cwd` จาก hook event |
| Tool count | จำนวน `PostToolUse` รวม |
| Cost / tokens | Statusline bridge (`POST /api/hooks/usage`) |
| Origin | Task หรือ cron job ที่เชื่อมโยงหาก dispatch โดย Tetora |

ข้อมูลค่าใช้จ่ายและ token มาจาก Claude Code statusline script ซึ่ง post ไปยัง `/api/hooks/usage` ตามช่วงเวลาที่กำหนด statusline script แยกต่างหากจาก hooks — มันอ่าน output status bar ของ Claude Code และส่งต่อไปยัง Tetora

---

## การ Monitor ค่าใช้จ่าย

Usage endpoint (`POST /api/hooks/usage`) รับ:

```json
{
  "sessionId": "abc123",
  "costUsd": 0.0042,
  "inputTokens": 8200,
  "outputTokens": 340,
  "contextPct": 12,
  "model": "claude-sonnet-4-5"
}
```

ข้อมูลนี้ปรากฏใน dashboard Workers panel และถูกรวบรวมเป็นกราฟค่าใช้จ่ายรายวัน Budget alert จะยิงเมื่อค่าใช้จ่ายของ session เกินงบประมาณต่อ role หรือ global ที่กำหนด

---

## การแก้ปัญหา

### Hooks ไม่ยิง

**ตรวจสอบว่า daemon รันอยู่:**
```bash
tetora status
```

**ตรวจสอบว่า hooks ถูกติดตั้ง:**
```bash
tetora hooks status
```

**ตรวจสอบ settings.json โดยตรง:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

หาก hooks key หายไป ให้รัน `tetora hooks install` อีกครั้ง

**ตรวจสอบว่า daemon รับ hook event ได้:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# คาดหวัง: {"ok":true}
```

หาก daemon ไม่ได้ฟังอยู่บน port ที่คาดหวัง ตรวจสอบ `listenAddr` ใน `config.json`

### Permission error บน settings.json

`settings.json` ของ Claude Code อยู่ที่ `~/.claude/settings.json` หากไฟล์เป็นของผู้ใช้อื่นหรือมีสิทธิ์ที่จำกัด:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### Dashboard workers panel ว่างเปล่า

1. ยืนยันว่า hooks ถูกติดตั้งและ daemon รันอยู่
2. เริ่ม Claude Code session ด้วยตนเองและรัน tool หนึ่งตัว (เช่น `ls`)
3. ตรวจสอบ dashboard Workers panel — session ควรปรากฏภายในไม่กี่วินาที
4. ถ้าไม่ปรากฏ ตรวจสอบ daemon log: `tetora logs -f | grep hooks`

### Plan gate ไม่ปรากฏ

Plan gate จะ activate เฉพาะเมื่อ Claude Code พยายามเรียก `ExitPlanMode` ซึ่งเกิดขึ้นเฉพาะใน plan mode session (เริ่มด้วย `--plan` หรือตั้งค่าผ่าน `permissionMode: "plan"` ใน role config) Session แบบ `acceptEdits` interactive ไม่ใช้ plan mode

### คำถามไม่ถูก route ไปยัง chat

Hook deny ของ `AskUserQuestion` ต้องการให้ MCP bridge ถูกกำหนดค่า รัน `tetora hooks install` อีกครั้ง — มันจะสร้าง bridge config ใหม่ จากนั้นเพิ่ม bridge ในการตั้งค่า MCP ของ Claude Code:

```bash
cat ~/.tetora/mcp/bridge.json
```

เพิ่มไฟล์นั้นเป็น MCP server ใน `~/.claude/settings.json` ภายใต้ `mcpServers`

---

## การ Migrate จาก tmux (v1.x)

ใน Tetora v1.x agent รันภายใน tmux pane และ Tetora monitor โดยการอ่าน pane output ใน v2.0 agent รันเป็น Claude Code process ธรรมดาและ Tetora สังเกตผ่าน hooks

**หากคุณกำลัง upgrade จาก v1.x:**

1. รัน `tetora hooks install` ครั้งเดียวหลัง upgrade
2. ลบ tmux session management configuration จาก `config.json` (key `tmux.*` ถูกละเว้นแล้ว)
3. ประวัติ session เดิมถูกเก็บไว้ใน `history.db` — ไม่ต้อง migrate
4. คำสั่ง `tetora session list` และแท็บ Sessions ใน dashboard ยังคงทำงานเหมือนเดิม

tmux terminal bridge (`discord_terminal.go`) ยังคงใช้ได้สำหรับการเข้าถึง terminal แบบ interactive ผ่าน Discord ซึ่งแยกจากการรัน agent — ให้คุณส่ง keystroke ไปยัง terminal session ที่รันอยู่ Hooks และ terminal bridge ทำงานเสริมกัน ไม่ได้ขัดแย้งกัน
