---
title: "Configuration Reference"
lang: "th"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# Configuration Reference

## ภาพรวม

Tetora ถูกกำหนดค่าผ่านไฟล์ JSON เดียวที่อยู่ที่ `~/.tetora/config.json`

**พฤติกรรมสำคัญ:**

- **การแทนที่ `$ENV_VAR`** — ค่าของ string ที่ขึ้นต้นด้วย `$` จะถูกแทนที่ด้วย environment variable ที่ตรงกันเมื่อเริ่มต้นระบบ ใช้วิธีนี้สำหรับข้อมูลลับ (API key, token) แทนการฝังค่าตรงๆ
- **Hot-reload** — การส่ง `SIGHUP` ไปยัง daemon จะโหลด config ใหม่ หาก config ผิดพลาดจะถูกปฏิเสธและคง config เดิมไว้ daemon จะไม่ crash
- **Relative path** — `jobsFile`, `historyDB`, `defaultWorkdir` และ directory field จะถูก resolve เทียบกับ directory ของไฟล์ config (`~/.tetora/`)
- **Backward compatibility** — key เดิม `"roles"` เป็น alias ของ `"agents"` และ key เดิม `"defaultRole"` ภายใน `smartDispatch` เป็น alias ของ `"defaultAgent"`

---

## Field ระดับบนสุด

### การตั้งค่าหลัก

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | ที่อยู่ HTTP สำหรับ API และ dashboard รูปแบบ: `host:port` |
| `apiToken` | string | `""` | Bearer token สำหรับ API request ทุกรายการ หากว่างเปล่าหมายถึงไม่มีการยืนยันตัวตน (ไม่แนะนำสำหรับ production) รองรับ `$ENV_VAR` |
| `maxConcurrent` | int | `8` | จำนวน agent task สูงสุดที่รันพร้อมกัน ค่าที่เกิน 20 จะมีคำเตือนเมื่อเริ่มต้น |
| `defaultModel` | string | `"sonnet"` | ชื่อ Claude model เริ่มต้น ส่งให้ provider ยกเว้นจะถูก override ต่อ agent |
| `defaultTimeout` | string | `"1h"` | timeout เริ่มต้นของ task รูปแบบ Go duration: `"15m"`, `"1h"`, `"30s"` |
| `defaultBudget` | float64 | `0` | งบประมาณค่าใช้จ่ายเริ่มต้นต่อ task เป็น USD `0` หมายถึงไม่จำกัด |
| `defaultPermissionMode` | string | `"acceptEdits"` | permission mode เริ่มต้นของ Claude ค่าทั่วไป: `"acceptEdits"`, `"default"` |
| `defaultAgent` | string | `""` | ชื่อ agent fallback ระดับระบบเมื่อไม่มี routing rule ที่ตรงกัน |
| `defaultWorkdir` | string | `""` | working directory เริ่มต้นสำหรับ agent task ต้องมีอยู่บน disk |
| `claudePath` | string | `"claude"` | path ไปยัง `claude` CLI binary ค่าเริ่มต้นคือค้นหา `claude` บน `$PATH` |
| `defaultProvider` | string | `"claude"` | ชื่อ provider ที่ใช้เมื่อไม่มีการ override ระดับ agent |
| `log` | bool | `false` | flag เดิมสำหรับเปิดการบันทึก log ไฟล์ แนะนำให้ใช้ `logging.level` แทน |
| `maxPromptLen` | int | `102400` | ความยาว prompt สูงสุดเป็น byte (100 KB) request ที่เกินกว่านี้จะถูกปฏิเสธ |
| `configVersion` | int | `0` | เวอร์ชัน schema ของ config ใช้สำหรับ auto-migration ไม่ควรตั้งค่าเอง |
| `encryptionKey` | string | `""` | AES key สำหรับการเข้ารหัสข้อมูลที่ sensitive รองรับ `$ENV_VAR` |
| `streamToChannels` | bool | `false` | ส่ง task status แบบ live ไปยัง messaging channel ที่เชื่อมต่ออยู่ (Discord, Telegram เป็นต้น) |
| `cronNotify` | bool\|null | `null` (true) | `false` ปิดการแจ้งเตือนการสิ้นสุด cron job ทั้งหมด `null` หรือ `true` เปิดใช้งาน |
| `cronReplayHours` | int | `2` | จำนวนชั่วโมงที่ย้อนหลังเพื่อค้นหา cron job ที่พลาดเมื่อ daemon เริ่มต้น |
| `diskBudgetGB` | float64 | `1.0` | พื้นที่ disk ว่างขั้นต่ำเป็น GB Cron job จะถูกปฏิเสธหากต่ำกว่าระดับนี้ |
| `diskWarnMB` | int | `500` | เกณฑ์เตือน disk ว่างเป็น MB บันทึก WARN แต่ job ยังดำเนินต่อ |
| `diskBlockMB` | int | `200` | เกณฑ์บล็อก disk ว่างเป็น MB Job จะถูกข้ามด้วยสถานะ `skipped_disk_full` |

### การ Override Directory

ค่าเริ่มต้น directory ทั้งหมดอยู่ภายใต้ `~/.tetora/` Override เฉพาะเมื่อต้องการโครงสร้างที่ไม่ใช่มาตรฐาน

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | Directory สำหรับไฟล์ knowledge ของ workspace |
| `agentsDir` | string | `~/.tetora/agents/` | Directory ที่มีไฟล์ SOUL.md ต่อ agent |
| `workspaceDir` | string | `~/.tetora/workspace/` | Directory สำหรับ rules, memory, skills, drafts เป็นต้น |
| `runtimeDir` | string | `~/.tetora/runtime/` | Directory สำหรับ sessions, outputs, logs, cache |
| `vaultDir` | string | `~/.tetora/vault/` | Directory สำหรับ vault ของ encrypted secrets |
| `historyDB` | string | `history.db` | path ฐานข้อมูล SQLite สำหรับประวัติงาน เทียบกับ config dir |
| `jobsFile` | string | `jobs.json` | path ไฟล์นิยาม cron job เทียบกับ config dir |

### Global Allowed Directories

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| Field | Type | คำอธิบาย |
|---|---|---|
| `allowedDirs` | string[] | Directory ที่ agent อ่านและเขียนได้ ใช้ global สามารถแคบลงต่อ agent ได้ |
| `defaultAddDirs` | string[] | Directory ที่ถูก inject เป็น `--add-dir` สำหรับทุก task (context อ่านอย่างเดียว) |
| `allowedIPs` | string[] | IP address หรือ CIDR range ที่อนุญาตให้เรียก API ว่างเปล่า = อนุญาตทั้งหมด ตัวอย่าง: `["192.168.1.0/24", "10.0.0.1"]` |

---

## Providers

Provider กำหนดวิธีที่ Tetora รัน agent task สามารถกำหนดค่า provider ได้หลายตัวและเลือกต่อ agent

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `type` | string | required | ประเภท provider ค่าหนึ่งใน: `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"` |
| `path` | string | `""` | path ของ binary ใช้โดย type `claude-cli` และ `claude-code` ถ้าว่างเปล่าจะ fallback ไปยัง `claudePath` |
| `baseUrl` | string | `""` | API base URL จำเป็นสำหรับ `openai-compatible` |
| `apiKey` | string | `""` | API key รองรับ `$ENV_VAR` จำเป็นสำหรับ `claude-api` เป็นตัวเลือกสำหรับ `openai-compatible` |
| `model` | string | `""` | model เริ่มต้นสำหรับ provider นี้ Override `defaultModel` สำหรับ task ที่ใช้ provider นี้ |
| `maxTokens` | int | `8192` | output token สูงสุด (ใช้โดย `claude-api`) |
| `firstTokenTimeout` | string | `"60s"` | เวลารอ token แรกก่อน timeout (SSE stream) |

**ประเภท Provider:**
- `claude-cli` — รัน `claude` binary เป็น subprocess (ค่าเริ่มต้น รองรับมากที่สุด)
- `claude-api` — เรียก Anthropic API โดยตรงผ่าน HTTP (ต้องใช้ `ANTHROPIC_API_KEY`)
- `openai-compatible` — REST API ที่เข้ากันได้กับ OpenAI (OpenAI, Ollama, Groq เป็นต้น)
- `claude-code` — ใช้ Claude Code CLI mode

---

## Agents

Agent กำหนด persona ที่มีชื่อพร้อม model, soul file และสิทธิ์เข้าถึง tool ของตัวเอง

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `soulFile` | string | required | path ไปยังไฟล์ SOUL.md บุคลิกภาพของ agent เทียบกับ `agentsDir` |
| `model` | string | `defaultModel` | model ที่ใช้สำหรับ agent นี้ |
| `description` | string | `""` | คำอธิบายที่มนุษย์อ่านได้ ยังถูกใช้โดย LLM classifier สำหรับ routing |
| `keywords` | string[] | `[]` | คำสำคัญที่ trigger การ routing ไปยัง agent นี้ใน smart dispatch |
| `provider` | string | `defaultProvider` | ชื่อ provider (key ใน map `providers`) |
| `permissionMode` | string | `defaultPermissionMode` | Claude permission mode สำหรับ agent นี้ |
| `allowedDirs` | string[] | `allowedDirs` | path ระบบไฟล์ที่ agent เข้าถึงได้ Override การตั้งค่า global |
| `docker` | bool\|null | `null` | การ override Docker sandbox ต่อ agent `null` = รับค่า global `docker.enabled` |
| `fallbackProviders` | string[] | `[]` | รายการ fallback provider ตามลำดับหาก primary ล้มเหลว |
| `trustLevel` | string | `"auto"` | ระดับความไว้วางใจ: `"observe"` (อ่านอย่างเดียว), `"suggest"` (เสนอแต่ไม่ apply), `"auto"` (อิสระเต็มที่) |
| `tools` | AgentToolPolicy | `{}` | นโยบายการเข้าถึง tool ดู [Tool Policy](#tool-policy) |
| `toolProfile` | string | `"standard"` | profile ของ tool ที่ตั้งชื่อ: `"minimal"`, `"standard"`, `"full"` |
| `workspace` | WorkspaceConfig | `{}` | การตั้งค่า workspace isolation |

---

## Smart Dispatch

Smart Dispatch จะ route task ขาเข้าไปยัง agent ที่เหมาะสมที่สุดโดยอัตโนมัติตาม rule, คำสำคัญ และ LLM classification

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน smart dispatch routing |
| `coordinator` | string | first agent | Agent ที่ใช้สำหรับ LLM classification ของ task |
| `defaultAgent` | string | first agent | Agent fallback เมื่อไม่มี rule ที่ตรงกัน |
| `classifyBudget` | float64 | `0.1` | งบประมาณ (USD) สำหรับการเรียก LLM จัดหมวดหมู่ |
| `classifyTimeout` | string | `"30s"` | timeout สำหรับการเรียกจัดหมวดหมู่ |
| `review` | bool | `false` | รัน review agent บน output หลังจาก task เสร็จสิ้น |
| `reviewLoop` | bool | `false` | เปิดใช้งาน Dev↔QA retry loop: review → feedback → retry (สูงสุด `maxRetries`) |
| `maxRetries` | int | `3` | จำนวนครั้ง QA retry สูงสุดใน review loop |
| `reviewAgent` | string | coordinator | Agent รับผิดชอบ review output ตั้งค่าเป็น QA agent ที่เข้มงวดสำหรับ adversarial review |
| `reviewBudget` | float64 | `0.2` | งบประมาณ (USD) สำหรับการเรียก LLM review |
| `fallback` | string | `"smart"` | กลยุทธ์ fallback: `"smart"` (LLM routing) หรือ `"coordinator"` (ใช้ default agent เสมอ) |
| `rules` | RoutingRule[] | `[]` | Keyword/regex routing rule ที่ประเมินก่อน LLM classification |
| `bindings` | RoutingBinding[] | `[]` | การผูก channel/user/guild → agent (ลำดับความสำคัญสูงสุด ประเมินก่อน) |

### `rules` — `RoutingRule`

| Field | Type | คำอธิบาย |
|---|---|---|
| `agent` | string | ชื่อ agent เป้าหมาย |
| `keywords` | string[] | คำสำคัญที่ไม่คำนึงถึงตัวพิมพ์ใหญ่เล็ก ตรงกันข้อใดข้อหนึ่งก็ route ไปยัง agent นี้ |
| `patterns` | string[] | Go regex pattern ตรงกันข้อใดข้อหนึ่งก็ route ไปยัง agent นี้ |

### `bindings` — `RoutingBinding`

| Field | Type | คำอธิบาย |
|---|---|---|
| `channel` | string | Platform: `"telegram"`, `"discord"`, `"slack"` เป็นต้น |
| `userId` | string | User ID บน platform นั้น |
| `channelId` | string | Channel หรือ chat ID |
| `guildId` | string | Guild/server ID (Discord เท่านั้น) |
| `agent` | string | ชื่อ agent เป้าหมาย |

---

## Session

ควบคุมวิธีการรักษาและ compact บริบทการสนทนาตลอดการโต้ตอบหลายรอบ

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `contextMessages` | int | `20` | จำนวนข้อความล่าสุดสูงสุดที่ inject เป็น context ใน task ใหม่ |
| `compactAfter` | int | `30` | Compact เมื่อจำนวนข้อความเกินค่านี้ Deprecated: ใช้ `compaction.maxMessages` แทน |
| `compactKeep` | int | `10` | เก็บข้อความล่าสุด N รายการหลัง compaction Deprecated: ใช้ `compaction.compactTo` แทน |
| `compactTokens` | int | `200000` | Compact เมื่อ input token รวมเกิน threshold นี้ |

### `session.compaction` — `CompactionConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน session compaction อัตโนมัติ |
| `maxMessages` | int | `50` | Trigger compaction เมื่อ session เกินจำนวนข้อความนี้ |
| `compactTo` | int | `10` | จำนวนข้อความล่าสุดที่เก็บไว้หลัง compaction |
| `model` | string | `"haiku"` | LLM model สำหรับสร้าง compaction summary |
| `maxCost` | float64 | `0.02` | ค่าใช้จ่ายสูงสุดต่อการเรียก compaction (USD) |
| `provider` | string | `defaultProvider` | Provider ที่ใช้สำหรับการเรียก compaction summary |

---

## Task Board

Task board ในตัวติดตามรายการงานและสามารถ dispatch ไปยัง agent โดยอัตโนมัติ

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน task board |
| `maxRetries` | int | `3` | จำนวนครั้ง retry สูงสุดต่อ task ก่อนทำเครื่องหมายว่าล้มเหลว |
| `requireReview` | bool | `false` | Quality gate: task ต้องผ่าน review ก่อนถูกทำเครื่องหมายว่าเสร็จ |
| `defaultWorkflow` | string | `""` | ชื่อ workflow ที่รันสำหรับ task ที่ auto-dispatch ทั้งหมด ว่างเปล่า = ไม่มี workflow |
| `gitCommit` | bool | `false` | Auto-commit เมื่อ task ถูกทำเครื่องหมายว่าเสร็จ |
| `gitPush` | bool | `false` | Auto-push หลัง commit (ต้องใช้ `gitCommit: true`) |
| `gitPR` | bool | `false` | Auto-สร้าง GitHub PR หลัง push (ต้องใช้ `gitPush: true`) |
| `gitWorktree` | bool | `false` | ใช้ git worktree สำหรับ task isolation (กำจัด file conflict ระหว่าง task ที่รันพร้อมกัน) |
| `idleAnalyze` | bool | `false` | Auto-รัน analysis เมื่อ board ว่าง |
| `problemScan` | bool | `false` | สแกน task output เพื่อหาปัญหาที่ซ่อนอยู่หลังเสร็จสิ้น |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน polling อัตโนมัติและ dispatch task ที่อยู่ในคิว |
| `interval` | string | `"5m"` | ความถี่ในการสแกนหา task ที่พร้อม |
| `maxConcurrentTasks` | int | `3` | Task สูงสุดที่ dispatch ต่อรอบสแกน |
| `defaultModel` | string | `""` | Override model สำหรับ task ที่ auto-dispatch |
| `maxBudget` | float64 | `0` | ค่าใช้จ่ายสูงสุดต่อ task (USD) `0` = ไม่จำกัด |
| `defaultAgent` | string | `""` | Agent fallback สำหรับ task ที่ยังไม่ได้กำหนด |
| `backlogAgent` | string | `""` | Agent สำหรับ backlog triage |
| `reviewAgent` | string | `""` | Agent สำหรับ review task ที่เสร็จสิ้น |
| `escalateAssignee` | string | `""` | กำหนด task ที่ถูก review ปฏิเสธไปยังผู้ใช้นี้ |
| `stuckThreshold` | string | `"2h"` | Task ที่อยู่ใน "doing" นานกว่านี้จะถูก reset เป็น "todo" |
| `backlogTriageInterval` | string | `"1h"` | ความถี่ในการรัน backlog triage |
| `reviewLoop` | bool | `false` | เปิดใช้งาน Dev↔QA loop อัตโนมัติสำหรับ task ที่ dispatch |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | template ชื่อ branch ตัวแปร: `{type}`, `{agent}`, `{description}` |
| `types` | string[] | `["feat","fix","refactor","chore"]` | prefix ประเภท branch ที่อนุญาต |
| `defaultType` | string | `"feat"` | ประเภท fallback เมื่อไม่มีการระบุ |
| `autoMerge` | bool | `false` | Merge กลับ main อัตโนมัติเมื่อ task เสร็จสิ้น (เฉพาะเมื่อ `gitWorktree: true`) |

---

## Slot Pressure

ควบคุมพฤติกรรมของระบบเมื่อใกล้ถึงขีดจำกัด slot ของ `maxConcurrent` session แบบ interactive (ที่มนุษย์เริ่ม) ได้รับการจอง slot ไว้ task พื้นหลังจะรอ

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งานการจัดการ slot pressure |
| `reservedSlots` | int | `2` | Slot ที่จองไว้สำหรับ interactive session Task พื้นหลังใช้สิ่งเหล่านี้ไม่ได้ |
| `warnThreshold` | int | `3` | เตือนผู้ใช้เมื่อ slot ที่ใช้ได้น้อยกว่านี้ |
| `nonInteractiveTimeout` | string | `"5m"` | นานเท่าใดที่ background task รอ slot ก่อน timeout |
| `pollInterval` | string | `"2s"` | ความถี่ที่ background task ตรวจสอบ slot ว่าง |
| `monitorEnabled` | bool | `false` | เปิดใช้งานการแจ้งเตือน slot pressure แบบ proactive ผ่าน notification channel |
| `monitorInterval` | string | `"30s"` | ความถี่ในการตรวจสอบและส่งการแจ้งเตือน pressure |

---

## Workflows

Workflow ถูกนิยามเป็นไฟล์ YAML ใน directory `workflowDir` ชี้ไปยัง directory นั้น ตัวแปรให้ค่า template เริ่มต้น

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | Directory ที่เก็บไฟล์ YAML ของ workflow |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | Trigger workflow อัตโนมัติบน system event |

---

## Integrations

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน Telegram bot |
| `botToken` | string | `""` | Telegram bot token จาก @BotFather รองรับ `$ENV_VAR` |
| `chatID` | int64 | `0` | Telegram chat หรือ group ID สำหรับส่งการแจ้งเตือน |
| `pollTimeout` | int | `30` | timeout long-poll เป็นวินาทีสำหรับรับข้อความ |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน Discord bot |
| `botToken` | string | `""` | Discord bot token รองรับ `$ENV_VAR` |
| `guildID` | string | `""` | จำกัดเฉพาะ Discord server (guild) ที่ระบุ |
| `channelIDs` | string[] | `[]` | Channel ID ที่ bot ตอบกลับข้อความทั้งหมด (ไม่ต้องกล่าวถึง `@`) |
| `mentionChannelIDs` | string[] | `[]` | Channel ID ที่ bot ตอบกลับเฉพาะเมื่อถูก `@` กล่าวถึง |
| `notifyChannelID` | string | `""` | Channel สำหรับการแจ้งเตือนการสิ้นสุด task (สร้าง thread ต่อ task) |
| `showProgress` | bool | `true` | แสดงข้อความ streaming "Working..." แบบ live ใน Discord |
| `webhooks` | map[string]string | `{}` | Webhook URL ที่ตั้งชื่อสำหรับการแจ้งเตือนขาออกเท่านั้น |
| `routes` | map[string]DiscordRouteConfig | `{}` | Map ของ channel ID ไปยังชื่อ agent สำหรับ routing ต่อ channel |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน Slack bot |
| `botToken` | string | `""` | Slack bot OAuth token (`xoxb-...`) รองรับ `$ENV_VAR` |
| `signingSecret` | string | `""` | Slack signing secret สำหรับยืนยัน request รองรับ `$ENV_VAR` |
| `appToken` | string | `""` | Slack app-level token สำหรับ Socket Mode (`xapp-...`) เป็นตัวเลือก รองรับ `$ENV_VAR` |
| `defaultChannel` | string | `""` | Channel ID เริ่มต้นสำหรับการแจ้งเตือนขาออก |

### Outbound Webhooks

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `url` | string | required | URL endpoint ของ webhook |
| `headers` | map[string]string | `{}` | HTTP header ที่รวมไว้ ค่ารองรับ `$ENV_VAR` |
| `events` | string[] | all | Event ที่ส่ง: `"success"`, `"error"`, `"timeout"`, `"all"` ว่างเปล่า = ทั้งหมด |

### Incoming Webhooks

Incoming webhook อนุญาตให้บริการภายนอก trigger task ของ Tetora ผ่าน HTTP POST

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### Notification Channels

Notification channel ที่ตั้งชื่อสำหรับ routing task event ไปยัง endpoint ของ Slack/Discord ต่างๆ

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `name` | string | `""` | การอ้างอิงที่ตั้งชื่อใช้ใน field `channel` ของ job (เช่น `"discord:alerts"`) |
| `type` | string | required | `"slack"` หรือ `"discord"` |
| `webhookUrl` | string | required | Webhook URL รองรับ `$ENV_VAR` |
| `events` | string[] | all | กรองตามประเภท event: `"all"`, `"error"`, `"success"` |
| `minPriority` | string | all | ความสำคัญขั้นต่ำ: `"critical"`, `"high"`, `"normal"`, `"low"` |

---

## Store (Template Marketplace)

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน template store |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | URL registry ระยะไกลสำหรับเรียกดูและติดตั้ง template |
| `authToken` | string | `""` | Authentication token สำหรับ registry รองรับ `$ENV_VAR` |

---

## ค่าใช้จ่ายและการแจ้งเตือน

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | ขีดจำกัดการใช้จ่ายรายวันเป็น USD `0` = ไม่จำกัด |
| `weeklyLimit` | float64 | `0` | ขีดจำกัดการใช้จ่ายรายสัปดาห์เป็น USD `0` = ไม่จำกัด |
| `dailyTokenLimit` | int | `0` | โควตา token รายวันรวม (input + output) `0` = ไม่จำกัด |
| `action` | string | `"warn"` | การกระทำเมื่อถึงขีดจำกัด: `"warn"` (log และแจ้งเตือน) หรือ `"pause"` (บล็อก task ใหม่) |

### `estimate` — `EstimateConfig`

การประมาณค่าใช้จ่ายก่อนรัน task

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | ขอการยืนยันเมื่อค่าใช้จ่ายที่ประมาณเกินค่า USD นี้ |
| `defaultOutputTokens` | int | `500` | การประมาณ output token สำรองเมื่อไม่ทราบการใช้งานจริง |

### `budgets` — `BudgetConfig`

งบประมาณค่าใช้จ่ายระดับ agent และระดับ team

---

## Logging

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `level` | string | `"info"` | ระดับ log: `"debug"`, `"info"`, `"warn"`, `"error"` |
| `format` | string | `"text"` | รูปแบบ log: `"text"` (อ่านได้โดยมนุษย์) หรือ `"json"` (structured) |
| `file` | string | `runtime/logs/tetora.log` | path ไฟล์ log เทียบกับ runtime dir |
| `maxSizeMB` | int | `50` | ขนาดไฟล์ log สูงสุดเป็น MB ก่อน rotation |
| `maxFiles` | int | `5` | จำนวนไฟล์ log ที่ rotate ที่เก็บไว้ |

---

## ความปลอดภัย

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน HTTP Basic Auth บน dashboard |
| `username` | string | `"admin"` | ชื่อผู้ใช้ basic auth |
| `password` | string | `""` | รหัสผ่าน basic auth รองรับ `$ENV_VAR` |
| `token` | string | `""` | ทางเลือก: static token ที่ส่งเป็น cookie |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| Field | Type | คำอธิบาย |
|---|---|---|
| `certFile` | string | path ไปยังไฟล์ TLS certificate PEM เปิดใช้งาน HTTPS เมื่อตั้งค่า (พร้อมกับ `keyFile`) |
| `keyFile` | string | path ไปยังไฟล์ TLS private key PEM |

### `rateLimit` — `RateLimitConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน rate limiting ต่อ IP |
| `maxPerMin` | int | `60` | API request สูงสุดต่อนาทีต่อ IP |

### `securityAlert` — `SecurityAlertConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งานการแจ้งเตือนความปลอดภัยเมื่อ auth ล้มเหลวซ้ำๆ |
| `failThreshold` | int | `10` | จำนวนครั้งที่ล้มเหลวในช่วงเวลาก่อนแจ้งเตือน |
| `failWindowMin` | int | `5` | Sliding window เป็นนาที |

### `approvalGates` — `ApprovalGateConfig`

กำหนดให้ต้องได้รับการอนุมัติจากมนุษย์ก่อนที่ tool บางตัวจะรัน

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน approval gate |
| `timeout` | int | `120` | วินาทีที่รอการอนุมัติก่อนยกเลิก |
| `tools` | string[] | `[]` | ชื่อ tool ที่ต้องได้รับการอนุมัติก่อนรัน |
| `autoApproveTools` | string[] | `[]` | Tool ที่ได้รับการอนุมัติล่วงหน้าเมื่อเริ่มต้น (ไม่ต้องถาม) |

---

## ความน่าเชื่อถือ

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `true` | เปิดใช้งาน circuit breaker สำหรับ provider failover |
| `failThreshold` | int | `5` | ความล้มเหลวต่อเนื่องก่อนเปิด circuit |
| `successThreshold` | int | `2` | ความสำเร็จในสถานะ half-open ก่อนปิด |
| `openTimeout` | string | `"30s"` | ระยะเวลาในสถานะ open ก่อนลองอีกครั้ง (half-open) |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

รายการ fallback provider ระดับ global ตามลำดับหาก default provider ล้มเหลว

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งานการ monitor heartbeat ของ agent |
| `interval` | string | `"30s"` | ความถี่ในการตรวจสอบ task ที่รันอยู่ว่า stall หรือไม่ |
| `stallThreshold` | string | `"5m"` | ไม่มี output ในระยะเวลานี้ = task หยุดชะงัก |
| `timeoutWarnRatio` | float64 | `0.8` | เตือนเมื่อเวลาที่ผ่านไปเกินอัตราส่วนนี้ของ task timeout |
| `autoCancel` | bool | `false` | ยกเลิก task ที่หยุดชะงักนานกว่า `2x stallThreshold` โดยอัตโนมัติ |
| `notifyOnStall` | bool | `true` | ส่งการแจ้งเตือนเมื่อตรวจพบว่า task หยุดชะงัก |

### `retention` — `RetentionConfig`

ควบคุมการล้างข้อมูลเก่าโดยอัตโนมัติ

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `history` | int | `90` | วันที่เก็บประวัติการรัน job |
| `sessions` | int | `30` | วันที่เก็บข้อมูล session |
| `auditLog` | int | `365` | วันที่เก็บรายการ audit log |
| `logs` | int | `14` | วันที่เก็บไฟล์ log |
| `workflows` | int | `90` | วันที่เก็บบันทึกการรัน workflow |
| `reflections` | int | `60` | วันที่เก็บบันทึก reflection |
| `sla` | int | `90` | วันที่เก็บบันทึกการตรวจสอบ SLA |
| `trustEvents` | int | `90` | วันที่เก็บบันทึก trust event |
| `handoffs` | int | `60` | วันที่เก็บบันทึก handoff/message ของ agent |
| `queue` | int | `7` | วันที่เก็บรายการ offline queue |
| `versions` | int | `180` | วันที่เก็บ snapshot เวอร์ชัน config |
| `outputs` | int | `30` | วันที่เก็บไฟล์ output ของ agent |
| `uploads` | int | `7` | วันที่เก็บไฟล์ที่อัปโหลด |
| `memory` | int | `30` | วันก่อนที่รายการ memory ที่ไม่ได้ใช้จะถูก archive |
| `claudeSessions` | int | `3` | วันที่เก็บ artifact ของ Claude CLI session |
| `piiPatterns` | string[] | `[]` | Regex pattern สำหรับการ redact PII ในเนื้อหาที่เก็บ |

---

## Quiet Hours และ Digest

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน quiet hours การแจ้งเตือนจะถูกระงับในช่วงเวลานี้ |
| `start` | string | `""` | เริ่มต้นช่วงเงียบ (เวลาท้องถิ่น รูปแบบ `"HH:MM"`) |
| `end` | string | `""` | สิ้นสุดช่วงเงียบ (เวลาท้องถิ่น) |
| `tz` | string | local | Timezone เช่น `"Asia/Taipei"`, `"UTC"` |
| `digest` | bool | `false` | ส่ง digest ของการแจ้งเตือนที่ถูกระงับเมื่อ quiet hours สิ้นสุด |

### `digest` — `DigestConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน daily digest ตามกำหนด |
| `time` | string | `"08:00"` | เวลาส่ง digest (`"HH:MM"`) |
| `tz` | string | local | Timezone |

---

## Tools

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `maxIterations` | int | `10` | จำนวนครั้ง tool call สูงสุดต่อ task |
| `timeout` | int | `120` | global tool engine timeout เป็นวินาที |
| `toolOutputLimit` | int | `10240` | จำนวน character สูงสุดต่อ tool output (ตัดเกินกว่านี้) |
| `toolTimeout` | int | `30` | timeout การรัน tool ต่อตัวเป็นวินาที |
| `defaultProfile` | string | `"standard"` | ชื่อ tool profile เริ่มต้น |
| `builtin` | map[string]bool | `{}` | เปิด/ปิด built-in tool แต่ละตัวตามชื่อ |
| `profiles` | map[string]ToolProfile | `{}` | Tool profile แบบกำหนดเอง |
| `trustOverride` | map[string]string | `{}` | Override ระดับความไว้วางใจต่อชื่อ tool |

### `tools.webSearch` — `WebSearchConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `provider` | string | `""` | Search provider: `"brave"`, `"tavily"`, `"searxng"` |
| `apiKey` | string | `""` | API key สำหรับ provider รองรับ `$ENV_VAR` |
| `baseURL` | string | `""` | endpoint แบบกำหนดเอง (สำหรับ searxng ที่ self-hosted) |
| `maxResults` | int | `5` | จำนวนผลการค้นหาสูงสุดที่ส่งคืน |

### `tools.vision` — `VisionConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `provider` | string | `""` | Vision provider: `"anthropic"`, `"openai"`, `"google"` |
| `apiKey` | string | `""` | API key รองรับ `$ENV_VAR` |
| `model` | string | `""` | ชื่อ model สำหรับ vision provider |
| `maxImageSize` | int | `5242880` | ขนาดรูปภาพสูงสุดเป็น byte (ค่าเริ่มต้น 5 MB) |
| `baseURL` | string | `""` | API endpoint แบบกำหนดเอง |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

การกำหนดค่า MCP server ที่ตั้งชื่อ แต่ละ key คือชื่อ MCP config ค่าคือ MCP JSON config ทั้งหมด Tetora เขียนสิ่งเหล่านี้ลงไฟล์ temp และส่งไปยัง claude binary ผ่าน `--mcp-config`

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

คำนิยาม MCP server แบบ simplified ที่จัดการโดย Tetora โดยตรง

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `command` | string | required | คำสั่งที่รัน |
| `args` | string[] | `[]` | argument ของคำสั่ง |
| `env` | map[string]string | `{}` | Environment variable สำหรับ process ค่ารองรับ `$ENV_VAR` |
| `enabled` | bool | `true` | MCP server นี้ active หรือไม่ |

---

## Prompt Budget

ควบคุมงบประมาณ character สูงสุดสำหรับแต่ละส่วนของ system prompt ปรับเมื่อ prompt ถูกตัดอย่างไม่คาดคิด

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `soulMax` | int | `8000` | Character สูงสุดสำหรับ agent soul/personality prompt |
| `rulesMax` | int | `4000` | Character สูงสุดสำหรับ workspace rule |
| `knowledgeMax` | int | `8000` | Character สูงสุดสำหรับเนื้อหา knowledge base |
| `skillsMax` | int | `4000` | Character สูงสุดสำหรับ skill ที่ inject |
| `maxSkillsPerTask` | int | `3` | จำนวน skill สูงสุดที่ inject ต่อ task |
| `contextMax` | int | `16000` | Character สูงสุดสำหรับ session context |
| `totalMax` | int | `40000` | ขีดจำกัด hard สำหรับขนาด system prompt รวม (ทุกส่วนรวมกัน) |

---

## Agent Communication

ควบคุมการ dispatch sub-agent แบบซ้อน (tool agent_dispatch)

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| Field | Type | Default | คำอธิบาย |
|---|---|---|---|
| `enabled` | bool | `false` | เปิดใช้งาน tool `agent_dispatch` สำหรับการเรียก sub-agent แบบซ้อน |
| `maxConcurrent` | int | `3` | การเรียก `agent_dispatch` สูงสุดพร้อมกัน global |
| `defaultTimeout` | int | `900` | timeout sub-agent เริ่มต้นเป็นวินาที |
| `maxDepth` | int | `3` | ความลึกสูงสุดของ sub-agent ซ้อนกัน |
| `maxChildrenPerTask` | int | `5` | child agent สูงสุดพร้อมกันต่อ parent task |

---

## ตัวอย่าง

### Config ขั้นต่ำ

Config ขั้นต่ำสำหรับเริ่มต้นกับ Claude CLI provider:

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### Multi-Agent Config พร้อม Smart Dispatch

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### Full Config (ทุก Section หลัก)

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
