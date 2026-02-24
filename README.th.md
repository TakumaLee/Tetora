<p align="center">
  <img src="assets/banner.png" alt="Tetora — ตัวจัดการ AI Agent" width="800">
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Bahasa Indonesia](README.id.md) | **ภาษาไทย** | [Filipino](README.fil.md) | [Español](README.es.md) | [Français](README.fr.md) | [Deutsch](README.de.md)

<p align="center">
  <strong>แพลตฟอร์มผู้ช่วย AI แบบ self-hosted พร้อมสถาปัตยกรรม multi-agent</strong>
</p>

Tetora ทำงานเป็น binary Go ตัวเดียวโดยไม่ต้องพึ่งพา dependency ภายนอก เชื่อมต่อกับผู้ให้บริการ AI ที่คุณใช้อยู่แล้ว ทำงานร่วมกับแพลตฟอร์มส่งข้อความที่ทีมของคุณใช้งาน และเก็บข้อมูลทั้งหมดไว้บนฮาร์ดแวร์ของคุณเอง

---

## Tetora คืออะไร

Tetora คือตัวจัดการ AI agent ที่ให้คุณกำหนด role ของ agent หลายตัว -- แต่ละตัวมีบุคลิกภาพ, system prompt, โมเดล และสิทธิ์เข้าถึง tool ของตัวเอง -- และโต้ตอบกับพวกมันผ่านแพลตฟอร์มแชท, HTTP API หรือ command line

**ความสามารถหลัก:**

- **Role แบบ multi-agent** -- กำหนด agent ที่แตกต่างกันพร้อมบุคลิกภาพ งบประมาณ และสิทธิ์ tool แยกกัน
- **Multi-provider** -- Claude API, OpenAI, Gemini และอื่น ๆ; สลับหรือรวมกันได้อย่างอิสระ
- **Multi-platform** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Cron job** -- ตั้งเวลางานที่ทำซ้ำพร้อมขั้นตอนอนุมัติและการแจ้งเตือน
- **ฐานความรู้** -- ป้อนเอกสารให้ agent เพื่อการตอบกลับที่แม่นยำ
- **หน่วยความจำถาวร** -- agent จดจำบริบทข้ามเซสชัน; ชั้นหน่วยความจำรวมพร้อมการรวบรวม
- **รองรับ MCP** -- เชื่อมต่อเซิร์ฟเวอร์ Model Context Protocol เป็นผู้ให้บริการ tool
- **Skill และ workflow** -- ชุด skill ที่ประกอบเข้าด้วยกันได้ และ pipeline workflow หลายขั้นตอน
- **Webhook** -- เรียก action ของ agent จากระบบภายนอก
- **การกำกับดูแลต้นทุน** -- งบประมาณต่อ role และแบบรวม พร้อมการลดระดับโมเดลอัตโนมัติ
- **การเก็บรักษาข้อมูล** -- นโยบายการล้างข้อมูลที่ปรับแต่งได้ต่อตาราง พร้อมการส่งออกและล้างข้อมูลทั้งหมด
- **Plugin** -- ขยายฟังก์ชันการทำงานผ่านกระบวนการ plugin ภายนอก
- **การเตือนความจำอัจฉริยะ, นิสัย, เป้าหมาย, รายชื่อผู้ติดต่อ, การติดตามการเงิน, สรุปรายวัน และอื่น ๆ**

---

## เริ่มต้นอย่างรวดเร็ว

### สำหรับวิศวกร

```bash
# ติดตั้งรุ่นล่าสุด
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# เรียกใช้ตัวช่วยตั้งค่า
tetora init

# ตรวจสอบว่าทุกอย่างถูกตั้งค่าอย่างถูกต้อง
tetora doctor

# เริ่ม daemon
tetora serve
```

### สำหรับผู้ที่ไม่ใช่วิศวกร

1. ไปที่[หน้า Releases](https://github.com/TakumaLee/Tetora/releases/latest)
2. ดาวน์โหลด binary สำหรับแพลตฟอร์มของคุณ (เช่น `tetora-darwin-arm64` สำหรับ Mac Apple Silicon)
3. ย้ายไปยังไดเรกทอรีใน PATH ของคุณแล้วเปลี่ยนชื่อเป็น `tetora` หรือวางไว้ใน `~/.tetora/bin/`
4. เปิด terminal แล้วรัน:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## Agent

Agent ทุกตัวของ Tetora มากกว่าแค่ chatbot -- มันมีตัวตน agent แต่ละตัว (เรียกว่า **role**) ถูกกำหนดโดย **soul file**: เอกสาร Markdown ที่ให้บุคลิกภาพ ความเชี่ยวชาญ สไตล์การสื่อสาร และแนวทางพฤติกรรมแก่ agent

### การกำหนด role

Role ถูกประกาศใน `config.json` ภายใต้ key `roles`:

```json
{
  "roles": {
    "default": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose assistant",
      "permissionMode": "acceptEdits"
    },
    "researcher": {
      "soulFile": "SOUL-researcher.md",
      "model": "opus",
      "description": "Deep research and analysis",
      "permissionMode": "plan"
    }
  }
}
```

### Soul file

Soul file บอก agent ว่า*มันคือใคร* วางไว้ในไดเรกทอรี workspace (`~/.tetora/workspace/` โดยค่าเริ่มต้น):

```markdown
# Koto — Soul File

## Identity
You are Koto, a thoughtful assistant who lives inside the Tetora system.
You speak in a warm, concise tone and prefer actionable advice.

## Expertise
- Software architecture and code review
- Technical writing and documentation

## Behavioral Guidelines
- Think step by step before answering
- Ask clarifying questions when the request is ambiguous
- Record important decisions in memory for future reference

## Output Format
- Start with a one-line summary
- Use bullet points for details
- End with next steps if applicable
```

### เริ่มต้นใช้งาน

`tetora init` จะแนะนำคุณในการสร้าง role แรกและสร้าง soul file เริ่มต้นให้โดยอัตโนมัติ คุณสามารถแก้ไขได้ตลอดเวลา -- การเปลี่ยนแปลงจะมีผลในเซสชันถัดไป

---

## Build จาก Source

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

คำสั่งนี้จะ build binary และติดตั้งไปที่ `~/.tetora/bin/tetora` ตรวจสอบให้แน่ใจว่า `~/.tetora/bin` อยู่ใน `PATH` ของคุณ

สำหรับการรัน test suite:

```bash
make test
```

---

## ข้อกำหนด

| ข้อกำหนด | รายละเอียด |
|---|---|
| **sqlite3** | ต้องพร้อมใช้งานใน `PATH` ใช้สำหรับการจัดเก็บข้อมูลถาวรทั้งหมด |
| **API key ผู้ให้บริการ AI** | อย่างน้อยหนึ่งรายการ: Claude API, OpenAI, Gemini หรือ endpoint ที่เข้ากันได้กับ OpenAI |
| **Go 1.25+** | จำเป็นเฉพาะเมื่อ build จาก source เท่านั้น |

---

## แพลตฟอร์มที่รองรับ

| แพลตฟอร์ม | สถาปัตยกรรม | สถานะ |
|---|---|---|
| macOS | amd64, arm64 | เสถียร |
| Linux | amd64, arm64 | เสถียร |
| Windows | amd64 | Beta |

---

## สถาปัตยกรรม

ข้อมูล runtime ทั้งหมดอยู่ภายใต้ `~/.tetora/`:

```
~/.tetora/
  config.json        การตั้งค่าหลัก (provider, role, การเชื่อมต่อ)
  jobs.json          คำจำกัดความ cron job
  history.db         ฐานข้อมูล SQLite (ประวัติ, หน่วยความจำ, เซสชัน, embedding, ...)
  sessions/          ไฟล์เซสชันต่อ agent
  knowledge/         เอกสารฐานความรู้
  logs/              ไฟล์ log แบบมีโครงสร้าง
  outputs/           ไฟล์ output ที่สร้างขึ้น
  uploads/           พื้นที่จัดเก็บอัปโหลดชั่วคราว
  bin/               binary ที่ติดตั้ง
```

การตั้งค่าใช้ JSON ธรรมดาพร้อมรองรับการอ้างอิง `$ENV_VAR` เพื่อไม่ต้อง hardcode ความลับ ตัวช่วยตั้งค่า (`tetora init`) จะสร้าง `config.json` ที่ใช้งานได้แบบโต้ตอบ

รองรับ Hot-reload: ส่ง `SIGHUP` ไปยัง daemon ที่กำลังทำงานเพื่อโหลด `config.json` ใหม่โดยไม่ต้องหยุดระบบ

---

## คู่มือ CLI

| คำสั่ง | คำอธิบาย |
|---|---|
| `tetora init` | ตัวช่วยตั้งค่าแบบโต้ตอบ |
| `tetora doctor` | การตรวจสอบสุขภาพและการวินิจฉัย |
| `tetora serve` | เริ่ม daemon (chat bot + HTTP API + cron) |
| `tetora run --file tasks.json` | ดำเนินงานจากไฟล์ JSON (โหมด CLI) |
| `tetora dispatch "Summarize this"` | รันงาน ad-hoc ผ่าน daemon |
| `tetora route "Review code security"` | Dispatch อัจฉริยะ -- กำหนดเส้นทางอัตโนมัติไปยัง role ที่เหมาะสมที่สุด |
| `tetora status` | ภาพรวมของ daemon, job และต้นทุน |
| `tetora job list` | แสดงรายการ cron job ทั้งหมด |
| `tetora job trigger <name>` | เรียก cron job ด้วยตนเอง |
| `tetora role list` | แสดงรายการ role ที่ตั้งค่าทั้งหมด |
| `tetora role show <name>` | แสดงรายละเอียด role และตัวอย่าง soul |
| `tetora history list` | แสดงประวัติการดำเนินงานล่าสุด |
| `tetora history cost` | แสดงสรุปต้นทุน |
| `tetora session list` | แสดงรายการเซสชันล่าสุด |
| `tetora memory list` | แสดงรายการบันทึกหน่วยความจำของ agent |
| `tetora knowledge list` | แสดงรายการเอกสารฐานความรู้ |
| `tetora skill list` | แสดงรายการ skill ที่พร้อมใช้งาน |
| `tetora workflow list` | แสดงรายการ workflow ที่ตั้งค่า |
| `tetora mcp list` | แสดงรายการการเชื่อมต่อเซิร์ฟเวอร์ MCP |
| `tetora budget show` | แสดงสถานะงบประมาณ |
| `tetora config show` | แสดงการตั้งค่าปัจจุบัน |
| `tetora config validate` | ตรวจสอบความถูกต้อง config.json |
| `tetora backup` | สร้างไฟล์สำรองข้อมูล |
| `tetora restore <file>` | กู้คืนจากไฟล์สำรองข้อมูล |
| `tetora dashboard` | เปิดแดชบอร์ดเว็บในเบราว์เซอร์ |
| `tetora logs` | ดู log ของ daemon (`-f` เพื่อติดตาม, `--json` สำหรับ output แบบมีโครงสร้าง) |
| `tetora data status` | แสดงสถานะการเก็บรักษาข้อมูล |
| `tetora service install` | ติดตั้งเป็นบริการ launchd (macOS) |
| `tetora completion <shell>` | สร้าง shell completion (bash, zsh, fish) |
| `tetora version` | แสดงเวอร์ชัน |

รัน `tetora help` เพื่อดูคู่มือคำสั่งทั้งหมด

---

## การมีส่วนร่วม

ยินดีรับการมีส่วนร่วม กรุณาเปิด issue เพื่อหารือเกี่ยวกับการเปลี่ยนแปลงที่สำคัญก่อนส่ง pull request

- **Issues**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **การสนทนา**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

โปรเจกต์นี้อยู่ภายใต้สัญญาอนุญาต AGPL-3.0 ซึ่งกำหนดให้ผลงานดัดแปลงและการ deploy ที่เข้าถึงได้ผ่านเครือข่ายต้องเป็น open source ภายใต้สัญญาอนุญาตเดียวกัน กรุณาตรวจสอบสัญญาอนุญาตก่อนมีส่วนร่วม

---

## สัญญาอนุญาต

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

ลิขสิทธิ์ (c) ผู้มีส่วนร่วม Tetora
