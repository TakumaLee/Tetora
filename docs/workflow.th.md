# เวิร์กโฟลว์

## ภาพรวม

เวิร์กโฟลว์คือระบบออร์เคสเตรชันงานแบบหลายขั้นตอนของ Tetora กำหนดลำดับขั้นตอนในรูปแบบ JSON ให้ agent ต่าง ๆ ทำงานร่วมกัน และทำให้งานที่ซับซ้อนเป็นแบบอัตโนมัติ

**กรณีใช้งาน:**

- งานที่ต้องการ agent หลายตัวทำงานตามลำดับหรือพร้อมกัน
- กระบวนการที่มีการแตกสาขาตามเงื่อนไขและลอจิกการลองใหม่เมื่อเกิดข้อผิดพลาด
- งานอัตโนมัติที่ทริกเกอร์ด้วยตารางเวลา cron, event หรือ webhook
- กระบวนการที่เป็นทางการซึ่งต้องการประวัติการทำงานและการติดตามต้นทุน

## เริ่มต้นอย่างรวดเร็ว

### 1. เขียน workflow JSON

สร้างไฟล์ `my-workflow.json`:

```json
{
  "name": "research-and-summarize",
  "description": "Gather information and write a summary",
  "variables": {
    "topic": "AI agents"
  },
  "timeout": "30m",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "Search and organize the latest developments in {{topic}}, listing 5 key points"
    },
    {
      "id": "summarize",
      "agent": "kohaku",
      "prompt": "Write a 300-word summary based on the following:\n{{steps.research.output}}",
      "dependsOn": ["research"]
    }
  ]
}
```

### 2. นำเข้าและตรวจสอบ

```bash
# ตรวจสอบโครงสร้าง JSON
tetora workflow validate my-workflow.json

# นำเข้าไปยัง ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. รัน

```bash
# รันเวิร์กโฟลว์
tetora workflow run research-and-summarize

# กำหนดค่าตัวแปรใหม่
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run (ไม่มีการเรียก LLM เฉพาะประมาณการต้นทุน)
tetora workflow run research-and-summarize --dry-run
```

### 4. ตรวจสอบผลลัพธ์

```bash
# แสดงประวัติการรัน
tetora workflow runs research-and-summarize

# ดูสถานะโดยละเอียดของการรันที่เจาะจง
tetora workflow status <run-id>
```

## โครงสร้าง Workflow JSON

### ฟิลด์ระดับบนสุด

| ฟิลด์ | ประเภท | จำเป็น | คำอธิบาย |
|-------|------|:--------:|-------------|
| `name` | string | ใช่ | ชื่อเวิร์กโฟลว์ ใช้ได้เฉพาะตัวอักษรและตัวเลข, `-`, และ `_` (เช่น `my-workflow`) |
| `description` | string | | คำอธิบาย |
| `steps` | WorkflowStep[] | ใช่ | ต้องมีอย่างน้อยหนึ่งขั้นตอน |
| `variables` | map[string]string | | ตัวแปรอินพุตพร้อมค่าเริ่มต้น (ค่าว่าง `""` = จำเป็นต้องระบุ) |
| `timeout` | string | | เวลาหมดอายุโดยรวมในรูปแบบ Go duration (เช่น `"30m"`, `"1h"`) |
| `onSuccess` | string | | เทมเพลตการแจ้งเตือนเมื่อสำเร็จ |
| `onFailure` | string | | เทมเพลตการแจ้งเตือนเมื่อล้มเหลว |

### ฟิลด์ WorkflowStep

| ฟิลด์ | ประเภท | คำอธิบาย |
|-------|------|-------------|
| `id` | string | **จำเป็น** — ตัวระบุขั้นตอนที่ไม่ซ้ำกัน |
| `type` | string | ประเภทขั้นตอน ค่าเริ่มต้นคือ `"dispatch"` ดูประเภทด้านล่าง |
| `agent` | string | บทบาท agent ที่จะดำเนินการขั้นตอนนี้ |
| `prompt` | string | คำสั่งสำหรับ agent (รองรับเทมเพลต `{{}}`) |
| `skill` | string | ชื่อ skill (สำหรับ type=skill) |
| `skillArgs` | string[] | อาร์กิวเมนต์ของ skill (รองรับเทมเพลต) |
| `dependsOn` | string[] | ID ของขั้นตอนที่ต้องทำก่อน (การพึ่งพา DAG) |
| `model` | string | การกำหนด LLM model เฉพาะ |
| `provider` | string | การกำหนด provider เฉพาะ |
| `timeout` | string | เวลาหมดอายุต่อขั้นตอน |
| `budget` | number | วงเงินต้นทุน (USD) |
| `permissionMode` | string | โหมดสิทธิ์ |
| `if` | string | นิพจน์เงื่อนไข (type=condition) |
| `then` | string | ID ขั้นตอนที่จะข้ามไปเมื่อเงื่อนไขเป็นจริง |
| `else` | string | ID ขั้นตอนที่จะข้ามไปเมื่อเงื่อนไขเป็นเท็จ |
| `handoffFrom` | string | ID ของขั้นตอนต้นทาง (type=handoff) |
| `parallel` | WorkflowStep[] | ขั้นตอนย่อยที่รันพร้อมกัน (type=parallel) |
| `retryMax` | int | จำนวนการลองใหม่สูงสุด (ต้องใช้ร่วมกับ `onError: "retry"`) |
| `retryDelay` | string | ระยะเวลาระหว่างการลองใหม่ เช่น `"10s"` |
| `onError` | string | การจัดการข้อผิดพลาด: `"stop"` (ค่าเริ่มต้น), `"skip"`, `"retry"` |
| `toolName` | string | ชื่อเครื่องมือ (type=tool_call) |
| `toolInput` | map[string]string | พารามิเตอร์อินพุตของเครื่องมือ (รองรับการขยาย `{{var}}`) |
| `delay` | string | ระยะเวลารอ (type=delay) เช่น `"30s"`, `"5m"` |
| `notifyMsg` | string | ข้อความแจ้งเตือน (type=notify, รองรับเทมเพลต) |
| `notifyTo` | string | คำใบ้ช่องทางแจ้งเตือน (เช่น `"telegram"`) |

## ประเภทขั้นตอน

### dispatch (ค่าเริ่มต้น)

ส่ง prompt ไปยัง agent ที่ระบุเพื่อดำเนินการ นี่คือประเภทขั้นตอนที่พบบ่อยที่สุด และใช้เมื่อละเว้น `type`

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**จำเป็น:** `prompt`
**ไม่จำเป็น:** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

รัน skill ที่ลงทะเบียนไว้

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**จำเป็น:** `skill`
**ไม่จำเป็น:** `skillArgs`

### condition

ประเมินนิพจน์เงื่อนไขเพื่อกำหนดเส้นทาง เมื่อเป็นจริงจะไปที่ `then`; เมื่อเป็นเท็จจะไปที่ `else` เส้นทางที่ไม่ถูกเลือกจะถูกทำเครื่องหมายเป็น skipped

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**จำเป็น:** `if`, `then`
**ไม่จำเป็น:** `else`

ตัวดำเนินการที่รองรับ:
- `==` — เท่ากัน (เช่น `{{type}} == 'technical'`)
- `!=` — ไม่เท่ากัน
- การตรวจสอบความถูกต้อง — ค่าที่ไม่ว่างและไม่ใช่ `"false"`/`"0"` ถือว่าเป็นจริง

### parallel

รันหลายขั้นตอนย่อยพร้อมกัน รอให้ทุกขั้นตอนเสร็จสิ้น ผลลัพธ์ของขั้นตอนย่อยจะถูกรวมด้วย `\n---\n`

```json
{
  "id": "gather",
  "type": "parallel",
  "parallel": [
    {"id": "search-papers", "agent": "hisui", "prompt": "Search for papers"},
    {"id": "search-code", "agent": "kokuyou", "prompt": "Search open-source projects"}
  ]
}
```

**จำเป็น:** `parallel` (ต้องมีอย่างน้อยหนึ่งขั้นตอนย่อย)

ผลลัพธ์ของขั้นตอนย่อยแต่ละตัวสามารถอ้างอิงได้ผ่าน `{{steps.search-papers.output}}`

### handoff

ส่งผลลัพธ์ของขั้นตอนหนึ่งไปยัง agent อื่นเพื่อประมวลผลต่อ ผลลัพธ์เต็มของขั้นตอนต้นทางจะกลายเป็นบริบทของ agent ที่รับ

```json
{
  "id": "review",
  "type": "handoff",
  "agent": "ruri",
  "handoffFrom": "draft",
  "prompt": "Review and revise the article",
  "dependsOn": ["draft"]
}
```

**จำเป็น:** `handoffFrom`, `agent`
**ไม่จำเป็น:** `prompt` (คำสั่งสำหรับ agent ที่รับ)

### tool_call

เรียกใช้เครื่องมือที่ลงทะเบียนไว้ใน tool registry

```json
{
  "id": "fetch-data",
  "type": "tool_call",
  "toolName": "http-get",
  "toolInput": {
    "url": "https://api.example.com/data?q={{topic}}"
  }
}
```

**จำเป็น:** `toolName`
**ไม่จำเป็น:** `toolInput` (รองรับการขยาย `{{var}}`)

### delay

รอตามระยะเวลาที่กำหนดก่อนดำเนินการต่อ

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**จำเป็น:** `delay` (รูปแบบ Go duration: `"30s"`, `"5m"`, `"1h"`)

### notify

ส่งข้อความแจ้งเตือน ข้อความจะถูกเผยแพร่เป็น SSE event (type=`workflow_notify`) เพื่อให้ consumer ภายนอกสามารถทริกเกอร์ Telegram, Slack และอื่น ๆ

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**จำเป็น:** `notifyMsg`
**ไม่จำเป็น:** `notifyTo` (คำใบ้ช่องทาง)

## ตัวแปรและเทมเพลต

เวิร์กโฟลว์รองรับไวยากรณ์เทมเพลต `{{}}` ซึ่งจะถูกขยายก่อนการดำเนินการขั้นตอน

### ตัวแปรอินพุต

```
{{varName}}
```

แก้ไขจากค่าเริ่มต้นใน `variables` หรือการกำหนดค่าใหม่ผ่าน `--var key=value`

### ผลลัพธ์ของขั้นตอน

```
{{steps.ID.output}}    — ข้อความผลลัพธ์ของขั้นตอน
{{steps.ID.status}}    — สถานะของขั้นตอน (success/error/skipped/timeout)
{{steps.ID.error}}     — ข้อความข้อผิดพลาดของขั้นตอน
```

### ตัวแปรสภาพแวดล้อม

```
{{env.KEY}}            — ตัวแปรสภาพแวดล้อมของระบบ
```

### ตัวอย่าง

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## การพึ่งพาและการควบคุมโฟลว์

### dependsOn — การพึ่งพา DAG

ใช้ `dependsOn` เพื่อกำหนดลำดับการดำเนินการ ระบบจะเรียงลำดับขั้นตอนโดยอัตโนมัติในรูปแบบ DAG (Directed Acyclic Graph)

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` รอให้ทั้ง `step-a` และ `step-b` เสร็จสมบูรณ์
- ขั้นตอนที่ไม่มี `dependsOn` จะเริ่มทันที (อาจทำงานพร้อมกัน)
- การพึ่งพาแบบวงกลมจะถูกตรวจพบและปฏิเสธ

### การแตกสาขาตามเงื่อนไข

`then`/`else` ของขั้นตอน `condition` จะกำหนดว่าขั้นตอนถัดไปใดจะถูกดำเนินการ:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

ขั้นตอนเส้นทางที่ไม่ถูกเลือกจะถูกทำเครื่องหมายเป็น `skipped` ขั้นตอนถัดไปยังคงประเมินตามปกติตาม `dependsOn` ของตัวเอง

## การจัดการข้อผิดพลาด

### กลยุทธ์ onError

แต่ละขั้นตอนสามารถตั้งค่า `onError`:

| ค่า | พฤติกรรม |
|-------|----------|
| `"stop"` | **ค่าเริ่มต้น** — ยกเลิกเวิร์กโฟลว์เมื่อล้มเหลว; ขั้นตอนที่เหลือจะถูกทำเครื่องหมายเป็น skipped |
| `"skip"` | ทำเครื่องหมายขั้นตอนที่ล้มเหลวเป็น skipped และดำเนินการต่อ |
| `"retry"` | ลองใหม่ตาม `retryMax` + `retryDelay`; หากลองใหม่ทั้งหมดล้มเหลว ถือว่าเป็นข้อผิดพลาด |

### การตั้งค่าการลองใหม่

```json
{
  "id": "flaky-step",
  "agent": "hisui",
  "prompt": "...",
  "onError": "retry",
  "retryMax": 3,
  "retryDelay": "10s"
}
```

- `retryMax`: จำนวนการลองใหม่สูงสุด (ไม่นับความพยายามครั้งแรก)
- `retryDelay`: ระยะเวลาระหว่างการลองใหม่ ค่าเริ่มต้นคือ 5 วินาที
- มีผลเฉพาะเมื่อ `onError: "retry"` เท่านั้น

## ทริกเกอร์

ทริกเกอร์เปิดใช้งานการดำเนินเวิร์กโฟลว์โดยอัตโนมัติ กำหนดค่าใน `config.json` ภายใต้อาร์เรย์ `workflowTriggers`

### โครงสร้าง WorkflowTriggerConfig

| ฟิลด์ | ประเภท | คำอธิบาย |
|-------|------|-------------|
| `name` | string | ชื่อทริกเกอร์ |
| `workflowName` | string | เวิร์กโฟลว์ที่จะดำเนินการ |
| `enabled` | bool | เปิดใช้งานหรือไม่ (ค่าเริ่มต้น: true) |
| `trigger` | TriggerSpec | เงื่อนไขทริกเกอร์ |
| `variables` | map[string]string | การกำหนดค่าตัวแปรใหม่สำหรับเวิร์กโฟลว์ |
| `cooldown` | string | ช่วงเวลา cooldown (เช่น `"5m"`, `"1h"`) |

### โครงสร้าง TriggerSpec

| ฟิลด์ | ประเภท | คำอธิบาย |
|-------|------|-------------|
| `type` | string | `"cron"`, `"event"`, หรือ `"webhook"` |
| `cron` | string | นิพจน์ cron (5 ฟิลด์: min hour day month weekday) |
| `tz` | string | เขตเวลา (เช่น `"Asia/Taipei"`) สำหรับ cron เท่านั้น |
| `event` | string | ประเภท SSE event รองรับ wildcard ต่อท้ายด้วย `*` (เช่น `"deploy_*"`) |
| `webhook` | string | ส่วนต่อท้ายพาธ webhook |

### Cron Triggers

ตรวจสอบทุก 30 วินาที ทริกเกอร์ได้สูงสุดครั้งละหนึ่งนาที (การกำจัดซ้ำ)

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Event Triggers

รับฟังบนช่อง SSE `_triggers` และจับคู่ประเภท event รองรับ wildcard ต่อท้ายด้วย `*`

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Event triggers จะฉีดตัวแปรพิเศษโดยอัตโนมัติ: `event_type`, `task_id`, `session_id` รวมถึงฟิลด์ข้อมูล event (มีคำนำหน้า `event_`)

### Webhook Triggers

ทริกเกอร์ผ่าน HTTP POST:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

การใช้งาน:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

คู่ key-value ใน JSON ของ POST body จะถูกฉีดเป็นตัวแปรเวิร์กโฟลว์พิเศษ

### Cooldown

ทริกเกอร์ทุกตัวรองรับ `cooldown` เพื่อป้องกันการทริกเกอร์ซ้ำภายในช่วงเวลาสั้น ทริกเกอร์ระหว่าง cooldown จะถูกละเว้นโดยไม่มีการแจ้งเตือน

### ตัวแปร Meta ของทริกเกอร์

ระบบจะฉีดตัวแปรเหล่านี้โดยอัตโนมัติในแต่ละทริกเกอร์:

- `_trigger_name` — ชื่อทริกเกอร์
- `_trigger_type` — ประเภทของทริกเกอร์ (cron/event/webhook)
- `_trigger_time` — เวลาทริกเกอร์ (RFC3339)

## โหมดการดำเนินการ

### live (ค่าเริ่มต้น)

การดำเนินการเต็มรูปแบบ: เรียก LLM, บันทึกประวัติ, เผยแพร่ SSE events

```bash
tetora workflow run my-workflow
```

### dry-run

ไม่มีการเรียก LLM; ประมาณต้นทุนสำหรับแต่ละขั้นตอน ขั้นตอน condition ประเมินตามปกติ; ขั้นตอน dispatch/skill/handoff จะคืนค่าการประมาณต้นทุน

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

ดำเนินการเรียก LLM ตามปกติแต่ไม่บันทึกลงในประวัติงานหรือบันทึก session เหมาะสำหรับการทดสอบ

```bash
tetora workflow run my-workflow --shadow
```

## อ้างอิง CLI

```
tetora workflow <command> [options]
```

| คำสั่ง | คำอธิบาย |
|---------|-------------|
| `list` | แสดงรายการเวิร์กโฟลว์ที่บันทึกไว้ทั้งหมด |
| `show <name>` | แสดงนิยามเวิร์กโฟลว์ (สรุป + JSON) |
| `validate <name\|file>` | ตรวจสอบเวิร์กโฟลว์ (รับชื่อหรือพาธไฟล์ JSON) |
| `create <file>` | นำเข้าเวิร์กโฟลว์จากไฟล์ JSON (ตรวจสอบก่อน) |
| `delete <name>` | ลบเวิร์กโฟลว์ |
| `run <name> [flags]` | ดำเนินเวิร์กโฟลว์ |
| `runs [name]` | แสดงประวัติการดำเนินการ (กรองตามชื่อได้) |
| `status <run-id>` | แสดงสถานะโดยละเอียดของการรัน (ผลลัพธ์ JSON) |
| `messages <run-id>` | แสดงข้อความ agent และบันทึก handoff ของการรัน |
| `history <name>` | แสดงประวัติเวอร์ชันของเวิร์กโฟลว์ |
| `rollback <name> <version-id>` | กู้คืนไปยังเวอร์ชันที่ระบุ |
| `diff <version1> <version2>` | เปรียบเทียบสองเวอร์ชัน |

### แฟล็กคำสั่ง run

| แฟล็ก | คำอธิบาย |
|------|-------------|
| `--var key=value` | กำหนดค่าตัวแปรเวิร์กโฟลว์ใหม่ (ใช้ได้หลายครั้ง) |
| `--dry-run` | โหมด dry-run (ไม่มีการเรียก LLM) |
| `--shadow` | โหมด shadow (ไม่บันทึกประวัติ) |

### ชื่อแทน

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## อ้างอิง HTTP API

### Workflow CRUD

| เมธอด | พาธ | คำอธิบาย |
|--------|------|-------------|
| GET | `/workflows` | แสดงรายการเวิร์กโฟลว์ทั้งหมด |
| POST | `/workflows` | สร้างเวิร์กโฟลว์ (body: Workflow JSON) |
| GET | `/workflows/{name}` | ดูนิยามเวิร์กโฟลว์เดียว |
| DELETE | `/workflows/{name}` | ลบเวิร์กโฟลว์ |
| POST | `/workflows/{name}/validate` | ตรวจสอบเวิร์กโฟลว์ |
| POST | `/workflows/{name}/run` | รันเวิร์กโฟลว์ (async, คืนค่า `202 Accepted`) |
| GET | `/workflows/{name}/runs` | ดูประวัติการรันของเวิร์กโฟลว์ |

#### POST /workflows/{name}/run Body

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### Workflow Runs

| เมธอด | พาธ | คำอธิบาย |
|--------|------|-------------|
| GET | `/workflow-runs` | แสดงรายการบันทึกการรันทั้งหมด (เพิ่ม `?workflow=name` เพื่อกรอง) |
| GET | `/workflow-runs/{id}` | ดูรายละเอียดการรัน (รวม handoffs + ข้อความ agent) |

### Triggers

| เมธอด | พาธ | คำอธิบาย |
|--------|------|-------------|
| GET | `/api/triggers` | แสดงสถานะทริกเกอร์ทั้งหมด |
| POST | `/api/triggers/{name}/fire` | ทริกเกอร์ด้วยตนเอง |
| GET | `/api/triggers/{name}/runs` | ดูประวัติการรันของทริกเกอร์ (เพิ่ม `?limit=N`) |
| POST | `/api/triggers/webhook/{id}` | webhook trigger (body: JSON key-value variables) |

## การจัดการเวอร์ชัน

ทุกการ `create` หรือการแก้ไขจะสร้าง snapshot เวอร์ชันโดยอัตโนมัติ

```bash
# ดูประวัติเวอร์ชัน
tetora workflow history my-workflow

# กู้คืนไปยังเวอร์ชันที่ระบุ
tetora workflow rollback my-workflow <version-id>

# เปรียบเทียบสองเวอร์ชัน
tetora workflow diff <version-id-1> <version-id-2>
```

## กฎการตรวจสอบ

ระบบตรวจสอบก่อนทั้ง `create` และ `run`:

- `name` จำเป็นต้องระบุ; ใช้ได้เฉพาะตัวอักษรและตัวเลข, `-`, และ `_`
- ต้องมีอย่างน้อยหนึ่งขั้นตอน
- ID ของขั้นตอนต้องไม่ซ้ำกัน
- การอ้างอิงใน `dependsOn` ต้องชี้ไปยัง ID ขั้นตอนที่มีอยู่จริง
- ขั้นตอนไม่สามารถพึ่งพาตัวเองได้
- การพึ่งพาแบบวงกลมจะถูกปฏิเสธ (การตรวจจับวงจร DAG)
- ฟิลด์ที่จำเป็นตามประเภทขั้นตอน (เช่น dispatch ต้องการ `prompt`, condition ต้องการ `if` + `then`)
- `timeout`, `retryDelay`, `delay` ต้องอยู่ในรูปแบบ Go duration ที่ถูกต้อง
- `onError` รับได้เฉพาะ `stop`, `skip`, `retry`
- `then`/`else` ของ condition ต้องอ้างอิง ID ขั้นตอนที่มีอยู่จริง
- `handoffFrom` ของ handoff ต้องอ้างอิง ID ขั้นตอนที่มีอยู่จริง
