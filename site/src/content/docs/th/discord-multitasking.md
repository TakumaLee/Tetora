---
title: "คู่มือการใช้งาน Discord แบบ Multi-tasking"
lang: "th"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# คู่มือการใช้งาน Discord แบบ Multi-tasking

Tetora รองรับการสนทนาพร้อมกันหลายสายบน Discord ผ่าน **Thread + `/focus`** โดยแต่ละ thread มี session และการผูก agent เป็นของตัวเอง

---

## แนวคิดพื้นฐาน

### Main Channel — Session เดียว

แต่ละ Discord channel มีเพียง **หนึ่ง active session** ข้อความทั้งหมดใช้บริบทการสนทนาเดียวกัน

- รูปแบบ session key: `discord:{channelID}`
- ข้อความทุกคนใน channel เดียวกันจะเข้าสู่ session เดียวกัน
- ประวัติการสนทนาจะสะสมต่อเนื่องจนกว่าคุณจะรีเซ็ตด้วย `!new`

### Thread — Session แยกอิสระ

Discord thread สามารถผูกกับ agent เฉพาะผ่าน `/focus` เพื่อรับ session ที่แยกออกมาโดยสมบูรณ์

- รูปแบบ session key: `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- แยกออกจาก session ของ main channel โดยสมบูรณ์ บริบทไม่รบกวนกัน
- แต่ละ thread สามารถผูกกับ agent ต่างกันได้

---

## คำสั่ง

| คำสั่ง | ตำแหน่ง | คำอธิบาย |
|---|---|---|
| `/focus <agent>` | ภายใน Thread | ผูก thread นี้กับ agent ที่ระบุ สร้าง session แยก |
| `/unfocus` | ภายใน Thread | ยกเลิกการผูก agent ของ thread |
| `!new` | Main Channel | Archive session ปัจจุบัน ข้อความถัดไปจะเปิด session ใหม่ |

---

## ขั้นตอนการใช้งาน Multi-tasking

### ขั้นตอนที่ 1: สร้าง Discord Thread

คลิกขวาที่ข้อความใน main channel → **Create Thread** (หรือใช้ฟีเจอร์สร้าง thread ของ Discord)

### ขั้นตอนที่ 2: ผูก Agent ภายใน Thread

```
/focus ruri
```

หลังจากผูกสำเร็จ การสนทนาทั้งหมดใน thread นี้จะ:
- ใช้การตั้งค่าบุคลิกภาพ SOUL.md ของ ruri
- มีประวัติการสนทนาแยกอิสระ
- ไม่ส่งผลต่อ session ของ main channel

### ขั้นตอนที่ 3: เปิดหลาย Thread ตามต้องการ

```
#general (main channel)                ← การสนทนาทั่วไป 1 session
  └─ Thread: "รีแฟคเตอร์ auth module"  ← /focus kokuyou → session แยก
  └─ Thread: "เขียน blog ประจำสัปดาห์" ← /focus kohaku  → session แยก
  └─ Thread: "รายงานวิเคราะห์คู่แข่ง"  ← /focus hisui   → session แยก
  └─ Thread: "วางแผน project"           ← /focus ruri    → session แยก
```

แต่ละ thread คือพื้นที่สนทนาที่แยกออกจากกันโดยสมบูรณ์ สามารถดำเนินการพร้อมกันได้

---

## ข้อควรระวัง

### TTL (Time-to-Live)

- การผูก thread จะหมดอายุหลัง **24 ชั่วโมง** ตามค่าเริ่มต้น
- หลังหมดอายุ thread จะกลับสู่โหมดปกติ (ใช้ logic routing ของ main channel)
- สามารถปรับได้ใน config ด้วย `threadBindings.ttlHours`

### ขีดจำกัด Concurrency

- จำนวน concurrency สูงสุดทั้งระบบถูกควบคุมโดย `maxConcurrent` (ค่าเริ่มต้น 8)
- ทุก channel + thread ใช้ขีดจำกัดนี้ร่วมกัน
- ข้อความที่เกินขีดจำกัดจะรอในคิว

### การเปิดใช้งานใน Config

ยืนยันว่า thread bindings เปิดใช้งานใน config แล้ว:

```json
{
  "discord": {
    "threadBindings": {
      "enabled": true,
      "ttlHours": 24
    }
  }
}
```

### ข้อจำกัดของ Main Channel

- ไม่สามารถใช้ `/focus` บน main channel เพื่อสร้าง session ที่สองได้
- หากต้องการรีเซ็ตบริบทการสนทนา ใช้ `!new`
- การส่งหลายข้อความพร้อมกันใน channel เดียวกันจะใช้ session ร่วมกัน บริบทอาจรบกวนกัน

---

## คำแนะนำตามสถานการณ์

| สถานการณ์ | วิธีที่แนะนำ |
|---|---|
| คุยทั่วไป ถามตอบง่ายๆ | สนทนาใน main channel โดยตรง |
| ต้องการพูดคุยเจาะจงหัวข้อใดหัวข้อหนึ่ง | เปิด thread + `/focus` |
| งานต่างๆ มอบหมายให้ agent ต่างกัน | แต่ละงานหนึ่ง thread แต่ละ thread `/focus` ไปยัง agent ที่ตรงกัน |
| บริบทการสนทนายาวเกินไปอยากเริ่มใหม่ | Main channel ใช้ `!new`, thread ใช้ `/unfocus` แล้ว `/focus` ใหม่ |
| ทีมหลายคนร่วมกันในหัวข้อเดียว | เปิด thread ร่วมกัน ทุกคนสนทนาภายใน thread |
