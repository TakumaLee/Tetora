# Alur Kerja (Workflow)

## Ikhtisar

Workflow adalah sistem orkestrasi tugas multi-langkah milik Tetora. Definisikan urutan langkah dalam JSON, biarkan berbagai agent berkolaborasi, dan otomatiskan tugas-tugas yang kompleks.

**Kasus penggunaan:**

- Tugas yang memerlukan beberapa agent bekerja secara berurutan atau paralel
- Proses dengan percabangan kondisional dan logika retry pada error
- Pekerjaan otomatis yang dipicu oleh jadwal cron, event, atau webhook
- Proses formal yang membutuhkan riwayat eksekusi dan pelacakan biaya

## Mulai Cepat

### 1. Buat JSON workflow

Buat file `my-workflow.json`:

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

### 2. Impor dan validasi

```bash
# Validate the JSON structure
tetora workflow validate my-workflow.json

# Import to ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. Jalankan

```bash
# Execute the workflow
tetora workflow run research-and-summarize

# Override variables
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run (no LLM calls, cost estimation only)
tetora workflow run research-and-summarize --dry-run
```

### 4. Periksa hasil

```bash
# List execution history
tetora workflow runs research-and-summarize

# View detailed status of a specific run
tetora workflow status <run-id>
```

## Struktur JSON Workflow

### Kolom Tingkat Atas

| Kolom | Tipe | Wajib | Deskripsi |
|-------|------|:-----:|-----------|
| `name` | string | Ya | Nama workflow. Hanya alfanumerik, `-`, dan `_` (contoh: `my-workflow`) |
| `description` | string | | Deskripsi |
| `steps` | WorkflowStep[] | Ya | Minimal satu langkah |
| `variables` | map[string]string | | Variabel input dengan nilai default (nilai `""` kosong = wajib diisi) |
| `timeout` | string | | Batas waktu keseluruhan dalam format durasi Go (contoh: `"30m"`, `"1h"`) |
| `onSuccess` | string | | Template notifikasi saat berhasil |
| `onFailure` | string | | Template notifikasi saat gagal |

### Kolom WorkflowStep

| Kolom | Tipe | Deskripsi |
|-------|------|-----------|
| `id` | string | **Wajib** — Pengenal langkah yang unik |
| `type` | string | Tipe langkah, default-nya `"dispatch"`. Lihat tipe-tipe di bawah |
| `agent` | string | Peran agent yang menjalankan langkah ini |
| `prompt` | string | Instruksi untuk agent (mendukung template `{{}}`) |
| `skill` | string | Nama skill (untuk type=skill) |
| `skillArgs` | string[] | Argumen skill (mendukung template) |
| `dependsOn` | string[] | ID langkah prasyarat (dependensi DAG) |
| `model` | string | Override model LLM |
| `provider` | string | Override provider |
| `timeout` | string | Batas waktu per langkah |
| `budget` | number | Batas biaya (USD) |
| `permissionMode` | string | Mode izin |
| `if` | string | Ekspresi kondisi (type=condition) |
| `then` | string | ID langkah yang dituju saat kondisi benar |
| `else` | string | ID langkah yang dituju saat kondisi salah |
| `handoffFrom` | string | ID langkah sumber (type=handoff) |
| `parallel` | WorkflowStep[] | Sub-langkah yang dijalankan secara paralel (type=parallel) |
| `retryMax` | int | Jumlah maksimum percobaan ulang (memerlukan `onError: "retry"`) |
| `retryDelay` | string | Interval percobaan ulang, contoh: `"10s"` |
| `onError` | string | Penanganan error: `"stop"` (default), `"skip"`, `"retry"` |
| `toolName` | string | Nama tool (type=tool_call) |
| `toolInput` | map[string]string | Parameter input tool (mendukung ekspansi `{{var}}`) |
| `delay` | string | Durasi tunggu (type=delay), contoh: `"30s"`, `"5m"` |
| `notifyMsg` | string | Pesan notifikasi (type=notify, mendukung template) |
| `notifyTo` | string | Petunjuk saluran notifikasi (contoh: `"telegram"`) |

## Tipe Langkah

### dispatch (default)

Mengirim prompt ke agent yang ditentukan untuk dieksekusi. Ini adalah tipe langkah yang paling umum dan digunakan ketika `type` dihilangkan.

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**Wajib:** `prompt`
**Opsional:** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

Menjalankan skill yang telah terdaftar.

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**Wajib:** `skill`
**Opsional:** `skillArgs`

### condition

Mengevaluasi ekspresi kondisi untuk menentukan cabang. Jika benar, mengambil `then`; jika salah, mengambil `else`. Cabang yang tidak dipilih ditandai sebagai dilewati.

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**Wajib:** `if`, `then`
**Opsional:** `else`

Operator yang didukung:
- `==` — sama dengan (contoh: `{{type}} == 'technical'`)
- `!=` — tidak sama dengan
- Pemeriksaan truthy — nilai non-kosong dan bukan `"false"`/`"0"` dievaluasi sebagai benar

### parallel

Menjalankan beberapa sub-langkah secara bersamaan, menunggu semua selesai. Output sub-langkah digabung dengan `\n---\n`.

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

**Wajib:** `parallel` (minimal satu sub-langkah)

Hasil masing-masing sub-langkah dapat direferensikan melalui `{{steps.search-papers.output}}`.

### handoff

Meneruskan output satu langkah ke agent lain untuk diproses lebih lanjut. Output lengkap langkah sumber menjadi konteks agent penerima.

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

**Wajib:** `handoffFrom`, `agent`
**Opsional:** `prompt` (instruksi untuk agent penerima)

### tool_call

Memanggil tool yang terdaftar dari registri tool.

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

**Wajib:** `toolName`
**Opsional:** `toolInput` (mendukung ekspansi `{{var}}`)

### delay

Menunggu selama durasi yang ditentukan sebelum melanjutkan.

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**Wajib:** `delay` (format durasi Go: `"30s"`, `"5m"`, `"1h"`)

### notify

Mengirim pesan notifikasi. Pesan diterbitkan sebagai event SSE (type=`workflow_notify`) sehingga konsumen eksternal dapat memicu Telegram, Slack, dan sebagainya.

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**Wajib:** `notifyMsg`
**Opsional:** `notifyTo` (petunjuk saluran)

## Variabel dan Template

Workflow mendukung sintaks template `{{}}` yang diekspansi sebelum eksekusi langkah.

### Variabel Input

```
{{varName}}
```

Diselesaikan dari nilai default `variables` atau override `--var key=value`.

### Hasil Langkah

```
{{steps.ID.output}}    — Teks output langkah
{{steps.ID.status}}    — Status langkah (success/error/skipped/timeout)
{{steps.ID.error}}     — Pesan error langkah
```

### Variabel Lingkungan

```
{{env.KEY}}            — Variabel lingkungan sistem
```

### Contoh

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## Dependensi dan Kontrol Alur

### dependsOn — Dependensi DAG

Gunakan `dependsOn` untuk mendefinisikan urutan eksekusi. Sistem secara otomatis mengurutkan langkah sebagai DAG (Graf Berarah Asiklik).

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` menunggu `step-a` dan `step-b` keduanya selesai
- Langkah tanpa `dependsOn` dimulai segera (kemungkinan secara paralel)
- Dependensi sirkular terdeteksi dan ditolak

### Percabangan Kondisional

`then`/`else` dari langkah `condition` menentukan langkah hilir mana yang dieksekusi:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

Langkah cabang yang tidak dipilih ditandai sebagai `skipped`. Langkah hilir tetap dievaluasi secara normal berdasarkan `dependsOn`-nya.

## Penanganan Error

### Strategi onError

Setiap langkah dapat menetapkan `onError`:

| Nilai | Perilaku |
|-------|----------|
| `"stop"` | **Default** — Hentikan workflow saat gagal; langkah yang tersisa ditandai sebagai dilewati |
| `"skip"` | Tandai langkah yang gagal sebagai dilewati dan lanjutkan |
| `"retry"` | Ulangi sesuai `retryMax` + `retryDelay`; jika semua percobaan gagal, perlakukan sebagai error |

### Konfigurasi Retry

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

- `retryMax`: Jumlah maksimum percobaan ulang (tidak termasuk percobaan pertama)
- `retryDelay`: Jeda antara percobaan ulang, default 5 detik
- Hanya berlaku saat `onError: "retry"`

## Trigger

Trigger memungkinkan eksekusi workflow otomatis. Konfigurasikan di `config.json` di bawah array `workflowTriggers`.

### Struktur WorkflowTriggerConfig

| Kolom | Tipe | Deskripsi |
|-------|------|-----------|
| `name` | string | Nama trigger |
| `workflowName` | string | Workflow yang akan dieksekusi |
| `enabled` | bool | Apakah diaktifkan (default: true) |
| `trigger` | TriggerSpec | Kondisi trigger |
| `variables` | map[string]string | Override variabel untuk workflow |
| `cooldown` | string | Periode cooldown (contoh: `"5m"`, `"1h"`) |

### Struktur TriggerSpec

| Kolom | Tipe | Deskripsi |
|-------|------|-----------|
| `type` | string | `"cron"`, `"event"`, atau `"webhook"` |
| `cron` | string | Ekspresi cron (5 kolom: min hour day month weekday) |
| `tz` | string | Zona waktu (contoh: `"Asia/Taipei"`), hanya untuk cron |
| `event` | string | Tipe event SSE, mendukung wildcard sufiks `*` (contoh: `"deploy_*"`) |
| `webhook` | string | Sufiks path webhook |

### Trigger Cron

Diperiksa setiap 30 detik, diaktifkan paling banyak sekali per menit (deduplikasi).

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Trigger Event

Mendengarkan pada saluran SSE `_triggers` dan mencocokkan tipe event. Mendukung wildcard sufiks `*`.

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Trigger event secara otomatis menginjeksi variabel tambahan: `event_type`, `task_id`, `session_id`, ditambah kolom data event (dengan prefiks `event_`).

### Trigger Webhook

Dipicu melalui HTTP POST:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

Penggunaan:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

Pasangan kunci-nilai JSON pada body POST diinjeksi sebagai variabel workflow tambahan.

### Cooldown

Semua trigger mendukung `cooldown` untuk mencegah pemanggilan berulang dalam waktu singkat. Trigger yang terjadi selama cooldown diabaikan secara diam-diam.

### Meta-Variabel Trigger

Sistem secara otomatis menginjeksi variabel-variabel berikut pada setiap trigger:

- `_trigger_name` — Nama trigger
- `_trigger_type` — Tipe trigger (cron/event/webhook)
- `_trigger_time` — Waktu trigger (RFC3339)

## Mode Eksekusi

### live (default)

Eksekusi penuh: memanggil LLM, merekam riwayat, menerbitkan event SSE.

```bash
tetora workflow run my-workflow
```

### dry-run

Tanpa pemanggilan LLM; memperkirakan biaya untuk setiap langkah. Langkah condition dievaluasi secara normal; langkah dispatch/skill/handoff mengembalikan estimasi biaya.

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

Menjalankan pemanggilan LLM secara normal tetapi tidak merekam ke riwayat tugas atau log sesi. Berguna untuk pengujian.

```bash
tetora workflow run my-workflow --shadow
```

## Referensi CLI

```
tetora workflow <command> [options]
```

| Perintah | Deskripsi |
|----------|-----------|
| `list` | Daftarkan semua workflow yang tersimpan |
| `show <name>` | Tampilkan definisi workflow (ringkasan + JSON) |
| `validate <name\|file>` | Validasi sebuah workflow (menerima nama atau path file JSON) |
| `create <file>` | Impor workflow dari file JSON (divalidasi terlebih dahulu) |
| `delete <name>` | Hapus sebuah workflow |
| `run <name> [flags]` | Jalankan sebuah workflow |
| `runs [name]` | Daftarkan riwayat eksekusi (opsional difilter berdasarkan nama) |
| `status <run-id>` | Tampilkan status terperinci sebuah eksekusi (output JSON) |
| `messages <run-id>` | Tampilkan pesan agent dan catatan handoff untuk sebuah eksekusi |
| `history <name>` | Tampilkan riwayat versi workflow |
| `rollback <name> <version-id>` | Kembalikan ke versi tertentu |
| `diff <version1> <version2>` | Bandingkan dua versi |

### Flag Perintah run

| Flag | Deskripsi |
|------|-----------|
| `--var key=value` | Override variabel workflow (dapat digunakan berkali-kali) |
| `--dry-run` | Mode dry-run (tanpa pemanggilan LLM) |
| `--shadow` | Mode shadow (tanpa perekaman riwayat) |

### Alias

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## Referensi HTTP API

### CRUD Workflow

| Metode | Path | Deskripsi |
|--------|------|-----------|
| GET | `/workflows` | Daftarkan semua workflow |
| POST | `/workflows` | Buat sebuah workflow (body: Workflow JSON) |
| GET | `/workflows/{name}` | Ambil definisi satu workflow |
| DELETE | `/workflows/{name}` | Hapus sebuah workflow |
| POST | `/workflows/{name}/validate` | Validasi sebuah workflow |
| POST | `/workflows/{name}/run` | Jalankan sebuah workflow (asinkron, mengembalikan `202 Accepted`) |
| GET | `/workflows/{name}/runs` | Ambil riwayat eksekusi sebuah workflow |

#### Body POST /workflows/{name}/run

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### Eksekusi Workflow

| Metode | Path | Deskripsi |
|--------|------|-----------|
| GET | `/workflow-runs` | Daftarkan semua catatan eksekusi (tambahkan `?workflow=name` untuk filter) |
| GET | `/workflow-runs/{id}` | Ambil detail eksekusi (termasuk handoff + pesan agent) |

### Trigger

| Metode | Path | Deskripsi |
|--------|------|-----------|
| GET | `/api/triggers` | Daftarkan semua status trigger |
| POST | `/api/triggers/{name}/fire` | Aktifkan trigger secara manual |
| GET | `/api/triggers/{name}/runs` | Lihat riwayat eksekusi trigger (tambahkan `?limit=N`) |
| POST | `/api/triggers/webhook/{id}` | Trigger webhook (body: variabel JSON kunci-nilai) |

## Manajemen Versi

Setiap `create` atau modifikasi secara otomatis membuat snapshot versi.

```bash
# View version history
tetora workflow history my-workflow

# Restore to a specific version
tetora workflow rollback my-workflow <version-id>

# Compare two versions
tetora workflow diff <version-id-1> <version-id-2>
```

## Aturan Validasi

Sistem melakukan validasi sebelum `create` maupun `run`:

- `name` wajib diisi; hanya alfanumerik, `-`, dan `_` yang diizinkan
- Minimal satu langkah diperlukan
- ID langkah harus unik
- Referensi `dependsOn` harus menunjuk ke ID langkah yang ada
- Langkah tidak boleh bergantung pada dirinya sendiri
- Dependensi sirkular ditolak (deteksi siklus DAG)
- Kolom wajib per tipe langkah (contoh: dispatch memerlukan `prompt`, condition memerlukan `if` + `then`)
- `timeout`, `retryDelay`, `delay` harus dalam format durasi Go yang valid
- `onError` hanya menerima `stop`, `skip`, `retry`
- `then`/`else` pada condition harus mereferensikan ID langkah yang ada
- `handoffFrom` pada handoff harus mereferensikan ID langkah yang ada
