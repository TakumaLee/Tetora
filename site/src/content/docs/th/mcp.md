---
title: "การ Integration กับ MCP (Model Context Protocol)"
lang: "th"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# การ Integration กับ MCP (Model Context Protocol)

Tetora มี MCP server ในตัวที่อนุญาตให้ AI agent (Claude Code เป็นต้น) โต้ตอบกับ API ของ Tetora ผ่าน MCP protocol มาตรฐาน

## สถาปัตยกรรม

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

MCP server คือ **stdio JSON-RPC 2.0 bridge** — อ่าน request จาก stdin ส่งต่อไปยัง HTTP API ของ Tetora และเขียน response ไปยัง stdout Claude Code เปิดใช้งานเป็น child process

## การตั้งค่า

### 1. เพิ่ม MCP server เข้าการตั้งค่า Claude Code

เพิ่มสิ่งต่อไปนี้ใน `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "tetora": {
      "command": "/Users/you/.tetora/bin/tetora",
      "args": ["mcp-server"]
    }
  }
}
```

แทนที่ path ด้วย path จริงของ binary `tetora` ของคุณ หาได้ด้วย:

```bash
which tetora
# หรือ
ls ~/.tetora/bin/tetora
```

### 2. ตรวจสอบว่า Tetora daemon รันอยู่

MCP bridge ส่งต่อไปยัง Tetora HTTP API ดังนั้น daemon ต้องรันอยู่:

```bash
tetora start
```

### 3. ยืนยัน

Restart Claude Code tool ของ MCP จะปรากฏเป็น tool ที่ใช้ได้โดยมี prefix `tetora_`

## Tool ที่ใช้ได้

### การจัดการ Task

| Tool | คำอธิบาย |
|------|-------------|
| `tetora_taskboard_list` | แสดงรายการ ticket ใน kanban board ตัวกรองเป็นตัวเลือก: `project`, `assignee`, `priority` |
| `tetora_taskboard_update` | อัปเดต task (status, assignee, priority, title) ต้องระบุ `id` |
| `tetora_taskboard_comment` | เพิ่ม comment ใน​task ต้องระบุ `id` และ `comment` |

### Memory

| Tool | คำอธิบาย |
|------|-------------|
| `tetora_memory_get` | อ่าน memory entry ต้องระบุ `agent` และ `key` |
| `tetora_memory_set` | เขียน memory entry ต้องระบุ `agent`, `key` และ `value` |
| `tetora_memory_search` | แสดงรายการ memory entry ทั้งหมด ตัวกรองเป็นตัวเลือก: `role` |

### Dispatch

| Tool | คำอธิบาย |
|------|-------------|
| `tetora_dispatch` | Dispatch task ไปยัง agent อื่น สร้าง Claude Code session ใหม่ ต้องระบุ `prompt` เป็นตัวเลือก: `agent`, `workdir`, `model` |

### Knowledge

| Tool | คำอธิบาย |
|------|-------------|
| `tetora_knowledge_search` | ค้นหา knowledge base ที่แชร์ ต้องระบุ `q` เป็นตัวเลือก: `limit` |

### Notifications

| Tool | คำอธิบาย |
|------|-------------|
| `tetora_notify` | ส่งการแจ้งเตือนไปยังผู้ใช้ผ่าน Discord/Telegram ต้องระบุ `message` เป็นตัวเลือก: `level` (info/warn/error) |
| `tetora_ask_user` | ถามคำถามผู้ใช้ผ่าน Discord และรอคำตอบ (สูงสุด 6 นาที) ต้องระบุ `question` เป็นตัวเลือก: `options` (ปุ่ม quick-reply สูงสุด 4 ปุ่ม) |

## รายละเอียด Tool

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

parameter ทั้งหมดเป็นตัวเลือก ส่งคืน JSON array ของ task

### tetora_taskboard_update

```json
{
  "id": "TASK-42",
  "status": "in_progress",
  "assignee": "kokuyou",
  "priority": "P1",
  "title": "New title"
}
```

เฉพาะ `id` ที่จำเป็น field อื่นอัปเดตเฉพาะเมื่อระบุ ค่าของ status: `todo`, `in_progress`, `review`, `done`

### tetora_taskboard_comment

```json
{
  "id": "TASK-42",
  "comment": "Started working on this",
  "author": "kokuyou"
}
```

### tetora_dispatch

```json
{
  "prompt": "Fix the broken CSS on the dashboard sidebar",
  "agent": "kokuyou",
  "workdir": "/path/to/project",
  "model": "sonnet"
}
```

เฉพาะ `prompt` ที่จำเป็น ถ้าละเว้น `agent` smart dispatch ของ Tetora จะ route ไปยัง agent ที่ดีที่สุด

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

นี่คือ **blocking call** — รอสูงสุด 6 นาทีสำหรับผู้ใช้ตอบผ่าน Discord ผู้ใช้เห็นคำถามพร้อมปุ่ม quick-reply เป็นตัวเลือกและยังสามารถพิมพ์คำตอบแบบกำหนดเองได้

## คำสั่ง CLI

### จัดการ MCP Server ภายนอก

Tetora สามารถทำหน้าที่เป็น MCP **host** ด้วย โดยเชื่อมต่อกับ MCP server ภายนอก:

```bash
# แสดงรายการ MCP server ที่กำหนดค่า
tetora mcp list

# แสดง config เต็มสำหรับ server
tetora mcp show <name>

# เพิ่ม MCP server ใหม่
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# ลบ server config
tetora mcp remove <name>

# ทดสอบการเชื่อมต่อ server
tetora mcp test <name>
```

### การรัน MCP Bridge

```bash
# เริ่ม MCP bridge server (โดยปกติ Claude Code เปิดใช้งาน ไม่ใช่ด้วยตนเอง)
tetora mcp-server
```

เมื่อรันครั้งแรก จะสร้าง `~/.tetora/mcp/bridge.json` พร้อม path binary ที่ถูกต้อง

## การกำหนดค่า

การตั้งค่าที่เกี่ยวกับ MCP ใน `config.json`:

| Field | Type | Default | คำอธิบาย |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | Map ของ MCP server config ภายนอก (ชื่อ → {command, args, env}) |

bridge server อ่าน `listenAddr` และ `apiToken` จาก config หลักเพื่อเชื่อมต่อกับ daemon

## Authentication

ถ้า `apiToken` ถูกตั้งค่าใน `config.json` MCP bridge จะรวม `Authorization: Bearer <token>` โดยอัตโนมัติใน HTTP request ทั้งหมดไปยัง daemon ไม่ต้องการ auth ระดับ MCP เพิ่มเติม

## การแก้ปัญหา

**Tool ไม่ปรากฏใน Claude Code:**
- ยืนยันว่า path ของ binary ใน `settings.json` ถูกต้อง
- ตรวจสอบว่า Tetora daemon รันอยู่ (`tetora start`)
- ตรวจสอบ log ของ Claude Code เพื่อหา MCP connection error

**Error "HTTP 401":**
- `apiToken` ใน `config.json` ต้องตรงกัน bridge อ่านโดยอัตโนมัติ

**Error "connection refused":**
- Daemon ไม่รันอยู่ หรือ `listenAddr` ไม่ตรงกัน ค่าเริ่มต้น: `127.0.0.1:8991`

**`tetora_ask_user` timeout:**
- ผู้ใช้มี 6 นาทีในการตอบผ่าน Discord ตรวจสอบว่า Discord bot เชื่อมต่ออยู่
