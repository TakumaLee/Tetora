---
title: "Panduan Taskboard & Auto-Dispatch"
lang: "id"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Panduan Taskboard & Auto-Dispatch

## Gambaran Umum

Taskboard adalah sistem kanban bawaan Tetora untuk melacak dan mengeksekusi task secara otomatis. Sistem ini memadukan penyimpanan task persisten (didukung SQLite) dengan mesin auto-dispatch yang memantau task yang siap dan menyerahkannya ke agent tanpa intervensi manual.

Kasus penggunaan umum:

- Mengantri backlog task engineering dan membiarkan agent mengerjakannya semalam
- Merutekan task ke agent tertentu berdasarkan keahlian (mis. `kokuyou` untuk backend, `kohaku` untuk konten)
- Menghubungkan task dengan relasi ketergantungan sehingga agent melanjutkan dari tempat yang lain berhenti
- Mengintegrasikan eksekusi task dengan git: pembuatan branch otomatis, commit, push, dan PR/MR

**Prasyarat:** `taskBoard.enabled: true` di `config.json` dan daemon Tetora berjalan.

---

## Siklus Hidup Task

Task mengalir melalui status dalam urutan ini:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| Status | Arti |
|---|---|
| `idea` | Konsep kasar, belum diperhalus |
| `needs-thought` | Memerlukan analisis atau desain sebelum implementasi |
| `backlog` | Sudah didefinisikan dan diprioritaskan, tetapi belum dijadwalkan |
| `todo` | Siap dieksekusi — auto-dispatch akan mengambilnya jika ada assignee |
| `doing` | Sedang berjalan |
| `review` | Eksekusi selesai, menunggu review kualitas |
| `done` | Selesai dan sudah direview |
| `partial-done` | Eksekusi berhasil tetapi post-processing gagal (mis. konflik merge git). Dapat dipulihkan. |
| `failed` | Eksekusi gagal atau menghasilkan output kosong. Akan dicoba ulang hingga `maxRetries`. |

Auto-dispatch mengambil task dengan `status=todo`. Jika task tidak memiliki assignee, secara otomatis ditugaskan ke `defaultAgent` (default: `ruri`). Task dalam `backlog` ditriage secara berkala oleh `backlogAgent` yang dikonfigurasi (default: `ruri`) yang mempromosikan task yang menjanjikan ke `todo`.

---

## Membuat Task

### CLI

```bash
# Task minimal (masuk ke backlog, tanpa assignee)
tetora task create --title="Add rate limiting to API"

# Dengan semua opsi
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# Daftar task
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# Tampilkan task tertentu
tetora task show task-abc123
tetora task show task-abc123 --full   # termasuk komentar/thread

# Pindahkan task secara manual
tetora task move task-abc123 --status=todo

# Tugaskan ke agent
tetora task assign task-abc123 --assignee=kokuyou

# Tambahkan komentar (tipe spec, context, log, atau system)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

ID task dibuat secara otomatis dalam format `task-<uuid>`. Anda dapat mereferensikan task berdasarkan ID lengkapnya atau prefiks pendek — CLI akan menyarankan kecocokan.

### HTTP API

```bash
# Buat
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# Daftar (filter berdasarkan status)
curl "http://localhost:8991/api/tasks?status=todo"

# Pindahkan ke status baru
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

Buka tab **Taskboard** di dashboard (`http://localhost:8991/dashboard`). Task ditampilkan dalam kolom kanban. Seret kartu antar kolom untuk mengubah status, klik kartu untuk membuka panel detail dengan komentar dan tampilan diff.

---

## Auto-Dispatch

Auto-dispatch adalah loop latar belakang yang mengambil task `todo` dan menjalankannya melalui agent.

### Cara kerjanya

1. Ticker dijalankan setiap `interval` (default: `5m`).
2. Scanner memeriksa berapa banyak task yang sedang berjalan. Jika `activeCount >= maxConcurrentTasks`, pemindaian dilewati.
3. Untuk setiap task `todo` dengan assignee, task di-dispatch ke agent tersebut. Task tanpa assignee secara otomatis ditugaskan ke `defaultAgent`.
4. Ketika task selesai, pemindaian ulang segera dijalankan sehingga batch berikutnya dimulai tanpa menunggu interval penuh.
5. Saat daemon startup, task `doing` yang ditinggalkan dari crash sebelumnya dipulihkan ke `done` (jika ada bukti penyelesaian) atau direset ke `todo` (jika benar-benar ditinggalkan).

### Alur Dispatch

```
                          ┌─────────┐
                          │  idea   │  (entri konsep manual)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (memerlukan analisis)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Triage (backlogAgent, default: ruri) berjalan berkala:   │
  │   • "ready"     → tugaskan agent → pindahkan ke todo      │
  │   • "decompose" → buat subtask → induk ke doing           │
  │   • "clarify"   → tambah komentar pertanyaan → tetap di backlog │
  │                                                           │
  │  Fast-path: sudah ada assignee + tidak ada dependensi     │
  │   → lewati triage LLM, promosikan langsung ke todo        │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Auto-dispatch mengambil task setiap siklus pemindaian:   │
  │   • Ada assignee       → dispatch ke agent tersebut       │
  │   • Tidak ada assignee → tugaskan defaultAgent, lalu jalankan │
  │   • Ada workflow       → jalankan melalui pipeline workflow │
  │   • Ada dependsOn      → tunggu hingga dep selesai        │
  │   • Ada run sebelumnya yang dapat dilanjutkan → lanjutkan dari checkpoint │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Agent mengeksekusi task (prompt tunggal atau workflow DAG) │
  │                                                           │
  │  Guard: stuckThreshold (default 2h)                       │
  │   • Jika workflow masih berjalan → perbarui timestamp      │
  │   • Jika benar-benar terhenti    → reset ke todo           │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     berhasil  sebagian  gagal
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     Tombol Resume     │  Coba ulang (hingga maxRetries)
           │     di dashboard      │  atau eskalasi
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ eskalasi │
       └────────┘            │ ke manusia│
                             └──────────┘
```

### Detail Triage

Triage berjalan setiap `backlogTriageInterval` (default: `1h`) dan dilakukan oleh `backlogAgent` (default: `ruri`). Agent menerima setiap task backlog beserta komentarnya dan roster agent yang tersedia, kemudian memutuskan:

| Tindakan | Efek |
|---|---|
| `ready` | Menugaskan agent tertentu dan mempromosikan ke `todo` |
| `decompose` | Membuat subtask (dengan assignee), induk pindah ke `doing` |
| `clarify` | Menambahkan pertanyaan sebagai komentar, task tetap di `backlog` |

**Fast-path**: Task yang sudah memiliki assignee dan tidak ada ketergantungan yang memblokir melewati triage LLM sepenuhnya dan langsung dipromosikan ke `todo`.

### Penugasan Otomatis

Ketika task `todo` tidak memiliki assignee, dispatcher secara otomatis menugaskannya ke `defaultAgent` (dapat dikonfigurasi, default: `ruri`). Ini mencegah task terhenti secara diam-diam. Alur umum:

1. Task dibuat tanpa assignee → masuk ke `backlog`
2. Triage mempromosikan ke `todo` (dengan atau tanpa menugaskan agent)
3. Jika triage tidak menugaskan → dispatcher menugaskan `defaultAgent`
4. Task dieksekusi secara normal

### Konfigurasi

Tambahkan ke `config.json`:

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

| Field | Default | Deskripsi |
|---|---|---|
| `enabled` | `false` | Aktifkan loop auto-dispatch |
| `interval` | `5m` | Seberapa sering memindai task yang siap |
| `maxConcurrentTasks` | `3` | Maksimum task yang berjalan secara bersamaan |
| `defaultAgent` | `ruri` | Ditugaskan otomatis ke task `todo` yang tidak memiliki assignee sebelum dispatch |
| `backlogAgent` | `ruri` | Agent yang mereview dan mempromosikan task backlog |
| `reviewAgent` | `ruri` | Agent yang mereview output task yang selesai |
| `escalateAssignee` | `takuma` | Siapa yang ditugaskan ketika auto-review memerlukan penilaian manusia |
| `stuckThreshold` | `2h` | Waktu maksimum task dapat berada dalam `doing` sebelum direset |
| `backlogTriageInterval` | `1h` | Interval minimum antara jalannya triage backlog |
| `reviewLoop` | `false` | Aktifkan loop Dev↔QA (eksekusi → review → perbaiki, hingga `maxRetries`) |
| `maxBudget` | tanpa batas | Biaya maksimum per task dalam USD |
| `defaultModel` | — | Override model untuk semua task yang di-dispatch otomatis |

---

## Slot Pressure

Slot pressure mencegah auto-dispatch menghabiskan semua slot konkurensi dan menyebabkan kelaparan pada session interaktif (pesan chat manusia, dispatch on-demand).

Aktifkan di `config.json`:

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

| Field | Default | Deskripsi |
|---|---|---|
| `reservedSlots` | `2` | Slot yang dicadangkan untuk penggunaan interaktif. Task non-interaktif harus menunggu jika slot yang tersedia turun ke level ini. |
| `warnThreshold` | `3` | Peringatan diaktifkan ketika slot yang tersedia turun ke level ini. Pesan "排程接近滿載" muncul di dashboard dan channel notifikasi. |
| `nonInteractiveTimeout` | `5m` | Berapa lama task non-interaktif menunggu slot sebelum dibatalkan. |

Sumber interaktif (chat manusia, `tetora dispatch`, `tetora route`) selalu mengambil slot segera. Sumber latar belakang (taskboard, cron) menunggu jika tekanan tinggi.

---

## Integrasi Git

Ketika `gitCommit`, `gitPush`, dan `gitPR` diaktifkan, dispatcher menjalankan operasi git setelah task selesai dengan sukses.

**Penamaan branch** dikendalikan oleh `gitWorkflow.branchConvention`:

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

Template default `{type}/{agent}-{description}` menghasilkan branch seperti `feat/kokuyou-add-rate-limiting`. Bagian `{description}` diturunkan dari judul task (huruf kecil, spasi diganti tanda hubung, dipotong hingga 40 karakter).

Field `type` task mengatur prefiks branch. Jika task tidak memiliki tipe, `defaultType` digunakan.

**Auto PR/MR** mendukung GitHub (`gh`) dan GitLab (`glab`). Binary yang tersedia di `PATH` digunakan secara otomatis.

---

## Mode Worktree

Ketika `gitWorktree: true`, setiap task berjalan dalam git worktree terisolasi sebagai pengganti direktori kerja bersama. Ini menghilangkan konflik file ketika beberapa task dieksekusi secara bersamaan pada repositori yang sama.

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← salinan terisolasi untuk task ini
  task-def456/   ← salinan terisolasi untuk task ini
```

Saat task selesai:

- Jika `autoMerge: true` (default), branch worktree digabungkan kembali ke `main` dan worktree dihapus.
- Jika merge gagal, task pindah ke status `partial-done`. Worktree dipertahankan untuk resolusi manual.

Untuk memulihkan dari `partial-done`:

```bash
# Periksa apa yang terjadi
tetora task show task-abc123 --full

# Merge branch secara manual
git merge feat/kokuyou-add-rate-limiting

# Tandai sebagai selesai
tetora task move task-abc123 --status=done
```

---

## Integrasi Workflow

Task dapat dijalankan melalui pipeline workflow alih-alih satu prompt agent. Ini berguna ketika task memerlukan beberapa langkah yang terkoordinasi (mis. riset → implementasi → uji → dokumentasi).

Tugaskan workflow ke task:

```bash
# Atur saat pembuatan task
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# Atau perbarui task yang sudah ada
tetora task update task-abc123 --workflow=engineering-pipeline
```

Untuk menonaktifkan workflow default di level board untuk task tertentu:

```json
{ "workflow": "none" }
```

Workflow default di level board diterapkan ke semua task yang di-dispatch otomatis kecuali jika di-override:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

Field `workflowRunId` pada task menautkannya ke eksekusi workflow tertentu, yang terlihat di tab Workflows dashboard.

---

## Tampilan Dashboard

Buka dashboard di `http://localhost:8991/dashboard` dan navigasi ke tab **Taskboard**.

**Papan kanban** — kolom untuk setiap status. Kartu menampilkan judul, assignee, lencana prioritas, dan biaya. Seret untuk mengubah status.

**Panel detail task** — klik kartu mana saja untuk membuka. Menampilkan:
- Deskripsi lengkap dan semua komentar (entri spec, context, log)
- Tautan session (melompat ke terminal worker langsung jika masih berjalan)
- Biaya, durasi, jumlah percobaan ulang
- Tautan workflow run jika ada

**Panel review diff** — ketika `requireReview: true`, task yang selesai muncul dalam antrian review. Reviewer melihat diff perubahan dan dapat menyetujui atau meminta perubahan.

---

## Tips

**Ukuran task.** Jaga task dalam cakupan 30–90 menit. Task yang terlalu besar (refactor multi-hari) cenderung timeout atau menghasilkan output kosong dan ditandai gagal. Pecah menjadi subtask menggunakan field `parentId`.

**Batas dispatch bersamaan.** `maxConcurrentTasks: 3` adalah default yang aman. Menaikkannya melampaui jumlah koneksi API yang diizinkan provider Anda menyebabkan kontentsi dan timeout. Mulai dari 3, naikkan ke 5 hanya setelah memastikan perilaku yang stabil.

**Pemulihan partial-done.** Jika task masuk ke `partial-done`, agent telah menyelesaikan pekerjaannya dengan sukses — hanya langkah merge git yang gagal. Selesaikan konflik secara manual, lalu pindahkan task ke `done`. Data biaya dan session dipertahankan.

**Menggunakan `dependsOn`.** Task dengan ketergantungan yang belum terpenuhi dilewati oleh dispatcher hingga semua ID task yang tercantum mencapai status `done`. Hasil task upstream secara otomatis disuntikkan ke dalam prompt task dependen di bawah "Previous Task Results".

**Triage backlog.** `backlogAgent` membaca setiap task `backlog`, menilai kelayakan dan prioritas, serta mempromosikan task yang jelas ke `todo`. Tulis deskripsi terperinci dan kriteria penerimaan dalam task `backlog` Anda — agent triage menggunakannya untuk memutuskan apakah akan mempromosikan atau membiarkan task untuk review manusia.

**Percobaan ulang dan loop review.** Dengan `reviewLoop: false` (default), task yang gagal dicoba ulang hingga `maxRetries` kali dengan komentar log sebelumnya disuntikkan. Dengan `reviewLoop: true`, setiap eksekusi direview oleh `reviewAgent` sebelum dianggap selesai — agent mendapatkan umpan balik dan mencoba lagi jika ada masalah yang ditemukan.
