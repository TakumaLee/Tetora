---
title: "Panduan Pemecahan Masalah"
lang: "id"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# Panduan Pemecahan Masalah

Panduan ini mencakup masalah yang paling sering dijumpai saat menjalankan Tetora. Untuk setiap masalah, penyebab yang paling mungkin dicantumkan terlebih dahulu.

---

## tetora doctor

Selalu mulai dari sini. Jalankan `tetora doctor` setelah instalasi atau ketika sesuatu berhenti berfungsi:

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

Setiap baris adalah satu pemeriksaan. Tanda `✗` merah berarti kegagalan keras (daemon tidak akan berfungsi tanpa memperbaikinya). Tanda `~` kuning berarti saran (opsional tetapi disarankan).

Perbaikan umum untuk pemeriksaan yang gagal:

| Pemeriksaan yang gagal | Perbaikan |
|---|---|
| `Config: not found` | Jalankan `tetora init` |
| `Claude CLI: not found` | Atur `claudePath` di `config.json` atau instal Claude Code |
| `sqlite3: not found` | `brew install sqlite3` (macOS) atau `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | Buat `~/.tetora/agents/{name}/SOUL.md` atau jalankan `tetora init` |
| `Workspace: not found` | Jalankan `tetora init` untuk membuat struktur direktori |

---

## "session produced no output"

Task selesai tetapi outputnya kosong. Task secara otomatis ditandai `failed`.

**Penyebab 1: Context window terlalu besar.** Prompt yang disuntikkan ke session melebihi batas konteks model. Claude Code langsung keluar ketika tidak dapat muat konteksnya.

Perbaikan: Aktifkan kompaksi session di `config.json`:

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

Atau kurangi jumlah konteks yang disuntikkan ke dalam task (deskripsi lebih pendek, komentar spec lebih sedikit, rantai `dependsOn` lebih kecil).

**Penyebab 2: Kegagalan startup CLI Claude Code.** Binary di `claudePath` crash saat startup — biasanya karena API key yang salah, masalah jaringan, atau ketidakcocokan versi.

Perbaikan: Jalankan binary Claude Code secara manual untuk melihat error:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

Perbaiki error yang dilaporkan, lalu coba ulang task:

```bash
tetora task move task-abc123 --status=todo
```

**Penyebab 3: Prompt kosong.** Task memiliki judul tetapi tidak ada deskripsi, dan judul saja terlalu ambigu bagi agent untuk ditindaklanjuti. Session berjalan, menghasilkan output yang tidak memenuhi pemeriksaan kosong, dan ditandai.

Perbaikan: Tambahkan deskripsi konkret:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## Error "unauthorized" di dashboard

Dashboard mengembalikan 401 atau menampilkan halaman kosong setelah reload.

**Penyebab 1: Service Worker meng-cache token auth yang lama.** PWA Service Worker meng-cache respons termasuk header auth. Setelah restart daemon dengan token baru, versi yang di-cache menjadi basi.

Perbaikan: Hard refresh halaman. Di Chrome/Safari:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

Atau buka DevTools → Application → Service Workers → klik "Unregister", lalu reload.

**Penyebab 2: Ketidakcocokan header Referer.** Middleware auth dashboard memvalidasi header `Referer`. Permintaan dari ekstensi browser, proxy, atau curl tanpa header `Referer` ditolak.

Perbaikan: Akses dashboard langsung di `http://localhost:8991/dashboard`, bukan melalui proxy. Jika Anda membutuhkan akses API dari tool eksternal, gunakan API token alih-alih auth session browser.

---

## Dashboard tidak memperbarui

Dashboard dimuat tetapi feed aktivitas, daftar worker, atau task board tetap basi.

**Penyebab: Ketidakcocokan versi Service Worker.** PWA Service Worker menyajikan versi yang di-cache dari JS/HTML dashboard bahkan setelah pembaruan `make bump`.

Perbaikan:

1. Hard refresh (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. Jika itu tidak berhasil, buka DevTools → Application → Service Workers → klik "Update" atau "Unregister"
3. Reload halaman

**Penyebab: Koneksi SSE terputus.** Dashboard menerima pembaruan langsung melalui Server-Sent Events. Jika koneksi terputus (gangguan jaringan, laptop tidur), feed berhenti memperbarui.

Perbaikan: Reload halaman. Koneksi SSE terbentuk kembali secara otomatis saat halaman dimuat.

---

## Peringatan "排程接近滿載"

Anda melihat pesan ini di Discord/Telegram atau feed notifikasi dashboard.

Ini adalah peringatan slot pressure. Diaktifkan ketika slot konkurensi yang tersedia turun ke atau di bawah `warnThreshold` (default: 3). Artinya Tetora berjalan mendekati kapasitas.

**Apa yang harus dilakukan:**

- Jika ini diharapkan (banyak task berjalan): tidak perlu tindakan. Peringatan bersifat informatif.
- Jika Anda tidak menjalankan banyak task: periksa task yang terhenti dalam status `doing`:

```bash
tetora task list --status=doing
```

- Jika Anda ingin menaikkan kapasitas: tingkatkan `maxConcurrent` di `config.json` dan sesuaikan `slotPressure.warnThreshold` accordingly.
- Jika session interaktif tertunda: tingkatkan `slotPressure.reservedSlots` untuk menahan lebih banyak slot bagi penggunaan interaktif.

---

## Task terhenti di "doing"

Task menampilkan `status=doing` tetapi tidak ada agent yang aktif mengerjakannya.

**Penyebab 1: Daemon di-restart di tengah task.** Task sedang berjalan ketika daemon dimatikan. Pada startup berikutnya, Tetora memeriksa task `doing` yang ditinggalkan dan memulihkannya ke `done` (jika ada bukti biaya/durasi) atau mereset ke `todo`.

Ini otomatis — tunggu startup daemon berikutnya. Jika daemon sudah berjalan dan task masih terhenti, heartbeat atau reset task yang terhenti akan menangkapnya dalam `stuckThreshold` (default: 2h).

Untuk memaksa reset segera:

```bash
tetora task move task-abc123 --status=todo
```

**Penyebab 2: Deteksi heartbeat/stall.** Monitor heartbeat (`heartbeat.go`) memeriksa session yang berjalan. Jika session tidak menghasilkan output selama ambang batas stall, secara otomatis dibatalkan dan task dipindahkan ke `failed`.

Periksa komentar task untuk komentar sistem `[auto-reset]` atau `[stall-detected]`:

```bash
tetora task show task-abc123 --full
```

**Batalkan manual melalui API:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Kegagalan merge worktree

Task selesai dan berpindah ke `partial-done` dengan komentar seperti `[worktree] merge failed`.

Ini berarti perubahan agent pada branch task berkonflik dengan `main`.

**Langkah pemulihan:**

```bash
# Lihat detail task dan branch mana yang dibuat
tetora task show task-abc123 --full

# Navigasi ke repositori proyek
cd /path/to/your/repo

# Merge branch secara manual
git merge feat/kokuyou-task-abc123

# Selesaikan konflik di editor Anda, lalu commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# Tandai task sebagai selesai
tetora task move task-abc123 --status=done
```

Direktori worktree dipertahankan di `~/.tetora/runtime/worktrees/task-abc123/` hingga Anda membersihkannya secara manual atau memindahkan task ke `done`.

---

## Biaya token tinggi

Session menggunakan lebih banyak token dari yang diharapkan.

**Penyebab 1: Konteks tidak dikompaksi.** Tanpa kompaksi session, setiap giliran mengakumulasi seluruh riwayat percakapan. Task yang berjalan lama (banyak pemanggilan tool) membuat konteks tumbuh secara linier.

Perbaikan: Aktifkan `sessionCompaction` (lihat bagian "session produced no output" di atas).

**Penyebab 2: Knowledge base atau file aturan yang besar.** File dalam `workspace/rules/` dan `workspace/knowledge/` disuntikkan ke setiap prompt agent. Jika file-file ini besar, mereka mengonsumsi token di setiap panggilan.

Perbaikan:
- Audit `workspace/knowledge/` — jaga file individual di bawah 50 KB.
- Pindahkan materi referensi yang jarang Anda butuhkan keluar dari path auto-inject.
- Jalankan `tetora knowledge list` untuk melihat apa yang sedang disuntikkan dan ukurannya.

**Penyebab 3: Routing model yang salah.** Model mahal (Opus) digunakan untuk task rutin.

Perbaikan: Tinjau `defaultModel` dalam konfigurasi agent dan atur default yang lebih murah untuk task massal:

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

## Error timeout provider

Task gagal dengan error timeout seperti `context deadline exceeded` atau `provider request timed out`.

**Penyebab 1: Timeout task terlalu pendek.** Timeout default mungkin terlalu pendek untuk task yang kompleks.

Perbaikan: Atur timeout yang lebih panjang dalam konfigurasi agent task atau per-task:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

Atau tingkatkan estimasi timeout LLM dengan menambahkan lebih banyak detail ke deskripsi task (Tetora menggunakan deskripsi untuk memperkirakan timeout melalui panggilan model cepat).

**Penyebab 2: Rate limiting API atau kontentsi.** Terlalu banyak permintaan bersamaan yang mengenai provider yang sama.

Perbaikan: Kurangi `maxConcurrentTasks` atau tambahkan `maxBudget` untuk membatasi task yang mahal:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` menginterupsi workflow

Anda menjalankan `make bump` saat workflow atau task sedang dieksekusi. Daemon di-restart di tengah task.

Restart memicu logika pemulihan orphan Tetora:

- Task dengan bukti penyelesaian (biaya tercatat, durasi tercatat) dipulihkan ke `done`.
- Task tanpa bukti penyelesaian tetapi melewati periode grace (2 menit) direset ke `todo` untuk di-dispatch ulang.
- Task yang diperbarui dalam 2 menit terakhir dibiarkan hingga pemindaian task terhenti berikutnya.

**Untuk memeriksa apa yang terjadi:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

Tinjau komentar task untuk entri `[auto-restore]` atau `[auto-reset]`.

**Jika Anda perlu mencegah bump selama task aktif** (belum tersedia sebagai flag), periksa bahwa tidak ada task yang berjalan sebelum melakukan bump:

```bash
tetora task list --status=doing
# Jika kosong, aman untuk bump
make bump
```

---

## Error SQLite

Anda melihat error seperti `database is locked`, `SQLITE_BUSY`, atau `index.lock` dalam log.

**Penyebab 1: Pragma WAL mode hilang.** Tanpa WAL mode, SQLite menggunakan penguncian file eksklusif, yang menyebabkan `database is locked` dalam kondisi baca/tulis bersamaan.

Semua panggilan DB Tetora melalui `queryDB()` dan `execDB()` yang menambahkan `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`. Jika Anda memanggil sqlite3 langsung dalam skrip, tambahkan pragma ini:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**Penyebab 2: File `index.lock` yang basi.** Operasi git meninggalkan `index.lock` jika diinterupsi. Manajer worktree memeriksa lock basi sebelum memulai pekerjaan git, tetapi crash dapat meninggalkan satu di belakang.

Perbaikan:

```bash
# Temukan file lock basi
find ~/.tetora/runtime/worktrees -name "index.lock"

# Hapus (hanya jika tidak ada operasi git yang aktif berjalan)
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram tidak merespons

Pesan ke bot tidak mendapat balasan.

**Penyebab 1: Konfigurasi channel yang salah.** Discord memiliki dua daftar channel: `channelIDs` (balas langsung ke semua pesan) dan `mentionChannelIDs` (hanya balas ketika di-mention dengan @). Jika channel tidak ada di salah satu daftar, pesan diabaikan.

Perbaikan: Periksa `config.json`:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**Penyebab 2: Token bot kedaluwarsa atau salah.** Token bot Telegram tidak kedaluwarsa, tetapi token Discord dapat dibatalkan jika bot dikeluarkan dari server atau token di-regenerasi.

Perbaikan: Buat ulang token bot di portal developer Discord dan perbarui `config.json`.

**Penyebab 3: Daemon tidak berjalan.** Gateway bot hanya aktif ketika `tetora serve` berjalan.

Perbaikan:

```bash
tetora status
tetora serve   # jika tidak berjalan
```

---

## Error CLI glab / gh

Integrasi git gagal dengan error dari `glab` atau `gh`.

**Error umum: `gh: command not found`**

Perbaikan:
```bash
brew install gh      # macOS
gh auth login        # autentikasi
```

**Error umum: `glab: You are not logged in`**

Perbaikan:
```bash
brew install glab    # macOS
glab auth login      # autentikasi dengan instance GitLab Anda
```

**Error umum: `remote: HTTP Basic: Access denied`**

Perbaikan: Pastikan SSH key atau kredensial HTTPS Anda dikonfigurasi untuk host repositori. Untuk GitLab:

```bash
glab auth status
ssh -T git@gitlab.com   # uji konektivitas SSH
```

Untuk GitHub:

```bash
gh auth status
ssh -T git@github.com
```

**PR/MR berhasil dibuat tetapi menunjuk ke branch base yang salah**

Secara default, PR menarget branch default repositori (`main` atau `master`). Jika workflow Anda menggunakan base yang berbeda, atur secara eksplisit dalam konfigurasi git post-task Anda atau pastikan branch default repositori dikonfigurasi dengan benar di platform hosting.
