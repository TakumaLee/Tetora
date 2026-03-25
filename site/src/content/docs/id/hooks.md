---
title: "Integrasi Claude Code Hooks"
lang: "id"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Integrasi Claude Code Hooks

## Gambaran Umum

Claude Code Hooks adalah sistem event yang tertanam dalam Claude Code yang menjalankan perintah shell pada titik-titik penting selama session berlangsung. Tetora mendaftarkan dirinya sebagai penerima hook sehingga dapat mengamati setiap session agent yang berjalan secara real-time — tanpa polling, tanpa tmux, dan tanpa menyuntikkan skrip wrapper.

**Yang dapat dilakukan hooks:**

- Pelacakan progres real-time di dashboard (pemanggilan tool, status session, daftar worker langsung)
- Pemantauan biaya dan token melalui jembatan statusline
- Audit penggunaan tool (tool mana yang dijalankan, dalam session mana, di direktori mana)
- Deteksi penyelesaian session dan pembaruan status task otomatis
- Gerbang plan mode: menahan `ExitPlanMode` hingga manusia menyetujui rencana di dashboard
- Routing pertanyaan interaktif: `AskUserQuestion` diarahkan ke jembatan MCP sehingga pertanyaan muncul di platform chat Anda alih-alih memblokir terminal

Hooks adalah jalur integrasi yang disarankan mulai Tetora v2.0. Pendekatan berbasis tmux yang lebih lama (v1.x) masih berfungsi tetapi tidak mendukung fitur khusus hooks seperti plan gate dan routing pertanyaan.

---

## Arsitektur

```
Claude Code session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan gate: long-poll hingga disetujui
  │   (AskUserQuestion)               └─► Tolak: arahkan ke jembatan MCP
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► Perbarui status worker
  │                                   └─► Deteksi penulisan file rencana
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► Tandai worker selesai
  │                                   └─► Picu penyelesaian task
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► Teruskan ke Discord/Telegram
```

Perintah hook adalah panggilan curl kecil yang disuntikkan ke `~/.claude/settings.json` milik Claude Code. Setiap event dikirim ke `POST /api/hooks/event` pada daemon Tetora yang berjalan.

---

## Pengaturan

### Instal hooks

Dengan daemon Tetora berjalan:

```bash
tetora hooks install
```

Ini menulis entri ke `~/.claude/settings.json` dan menghasilkan konfigurasi jembatan MCP di `~/.tetora/mcp/bridge.json`.

Contoh apa yang ditulis ke `~/.claude/settings.json`:

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

### Periksa status

```bash
tetora hooks status
```

Output menampilkan hooks mana yang terinstal, berapa banyak aturan Tetora yang terdaftar, dan total jumlah event yang diterima sejak daemon dimulai.

Anda juga dapat memeriksa dari dashboard: **Engineering Details → Hooks** menampilkan status yang sama ditambah feed event langsung.

### Hapus hooks

```bash
tetora hooks remove
```

Menghapus semua entri Tetora dari `~/.claude/settings.json`. Hook non-Tetora yang ada tetap dipertahankan.

---

## Event Hook

### PostToolUse

Dijalankan setelah setiap pemanggilan tool selesai. Tetora menggunakannya untuk:

- Melacak tool yang digunakan agent (`Bash`, `Write`, `Edit`, `Read`, dll.)
- Memperbarui `lastTool` dan `toolCount` worker dalam daftar worker langsung
- Mendeteksi ketika agent menulis ke file rencana (memicu pembaruan cache rencana)

### Stop

Dijalankan ketika session Claude Code berakhir (selesai secara alami atau dibatalkan). Tetora menggunakannya untuk:

- Menandai worker sebagai `done` dalam daftar worker langsung
- Menerbitkan event SSE penyelesaian ke dashboard
- Memicu pembaruan status task downstream untuk task taskboard

### Notification

Dijalankan ketika Claude Code mengirim notifikasi (mis. izin diperlukan, jeda panjang). Tetora meneruskan ini ke Discord/Telegram dan menerbitkannya ke stream SSE dashboard.

### PreToolUse: ExitPlanMode (plan gate)

Ketika agent akan keluar dari plan mode, Tetora mencegat event dengan long-poll (timeout: 600 detik). Konten rencana di-cache dan ditampilkan di dashboard pada tampilan detail session.

Manusia dapat menyetujui atau menolak rencana dari dashboard. Jika disetujui, hook mengembalikan nilai dan Claude Code melanjutkan. Jika ditolak (atau jika timeout habis), keluaran diblokir dan Claude Code tetap dalam plan mode.

### PreToolUse: AskUserQuestion (routing pertanyaan)

Ketika Claude Code mencoba mengajukan pertanyaan kepada pengguna secara interaktif, Tetora mencegat dan menolak perilaku default. Pertanyaan kemudian diarahkan melalui jembatan MCP, muncul di platform chat yang dikonfigurasi (Discord, Telegram, dll.) sehingga Anda dapat menjawab tanpa duduk di terminal.

---

## Pelacakan Progres Real-Time

Setelah hooks terinstal, panel **Workers** di dashboard menampilkan session langsung:

| Field | Sumber |
|---|---|
| Session ID | `session_id` dalam event hook |
| State | `working` / `idle` / `done` |
| Tool terakhir | Nama tool `PostToolUse` terbaru |
| Direktori kerja | `cwd` dari event hook |
| Jumlah tool | Jumlah kumulatif `PostToolUse` |
| Biaya / token | Jembatan statusline (`POST /api/hooks/usage`) |
| Asal | Task terhubung atau cron job jika di-dispatch oleh Tetora |

Data biaya dan token berasal dari skrip statusline Claude Code, yang mengirim ke `/api/hooks/usage` pada interval yang dapat dikonfigurasi. Skrip statusline terpisah dari hooks — ia membaca output status bar Claude Code dan meneruskannya ke Tetora.

---

## Pemantauan Biaya

Endpoint penggunaan (`POST /api/hooks/usage`) menerima:

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

Data ini terlihat di panel Workers dashboard dan diagregasi ke dalam grafik biaya harian. Peringatan anggaran diaktifkan ketika biaya session melebihi anggaran per-role atau global yang dikonfigurasi.

---

## Pemecahan Masalah

### Hooks tidak berjalan

**Periksa daemon berjalan:**
```bash
tetora status
```

**Periksa hooks terinstal:**
```bash
tetora hooks status
```

**Periksa settings.json secara langsung:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

Jika key hooks hilang, jalankan ulang `tetora hooks install`.

**Periksa daemon dapat menerima event hook:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# Diharapkan: {"ok":true}
```

Jika daemon tidak mendengarkan pada port yang diharapkan, periksa `listenAddr` di `config.json`.

### Error izin pada settings.json

`settings.json` milik Claude Code berada di `~/.claude/settings.json`. Jika file dimiliki oleh pengguna lain atau memiliki izin yang ketat:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### Panel workers dashboard kosong

1. Konfirmasi hooks terinstal dan daemon berjalan.
2. Mulai session Claude Code secara manual dan jalankan satu tool (mis. `ls`).
3. Periksa panel Workers dashboard — session seharusnya muncul dalam beberapa detik.
4. Jika tidak, periksa log daemon: `tetora logs -f | grep hooks`

### Plan gate tidak muncul

Plan gate hanya aktif ketika Claude Code mencoba memanggil `ExitPlanMode`. Ini hanya terjadi dalam session plan mode (dimulai dengan `--plan` atau diset melalui `permissionMode: "plan"` dalam konfigurasi role). Session `acceptEdits` interaktif tidak menggunakan plan mode.

### Pertanyaan tidak diarahkan ke chat

Hook deny `AskUserQuestion` memerlukan jembatan MCP yang dikonfigurasi. Jalankan `tetora hooks install` lagi — ini menghasilkan ulang konfigurasi jembatan. Kemudian tambahkan jembatan ke pengaturan MCP Claude Code Anda:

```bash
cat ~/.tetora/mcp/bridge.json
```

Tambahkan file tersebut sebagai server MCP di `~/.claude/settings.json` di bawah `mcpServers`.

---

## Migrasi dari tmux (v1.x)

Dalam Tetora v1.x, agent berjalan di dalam panel tmux dan Tetora memantau mereka dengan membaca output panel. Dalam v2.0, agent berjalan sebagai proses Claude Code biasa dan Tetora mengamati mereka melalui hooks.

**Jika Anda melakukan upgrade dari v1.x:**

1. Jalankan `tetora hooks install` sekali setelah upgrade.
2. Hapus konfigurasi manajemen session tmux dari `config.json` (key `tmux.*` sekarang diabaikan).
3. Riwayat session yang ada dipertahankan di `history.db` — tidak perlu migrasi.
4. Perintah `tetora session list` dan tab Sessions di dashboard terus berfungsi seperti sebelumnya.

Jembatan terminal tmux (`discord_terminal.go`) masih tersedia untuk akses terminal interaktif melalui Discord. Ini terpisah dari eksekusi agent — ini memungkinkan Anda mengirim keystroke ke session terminal yang sedang berjalan. Hooks dan jembatan terminal saling melengkapi, bukan saling menggantikan.
