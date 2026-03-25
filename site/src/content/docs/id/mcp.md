---
title: "Integrasi MCP (Model Context Protocol)"
lang: "id"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# Integrasi MCP (Model Context Protocol)

Tetora menyertakan server MCP bawaan yang memungkinkan AI agent (Claude Code, dll.) berinteraksi dengan API Tetora melalui protokol MCP standar.

## Arsitektur

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

Server MCP adalah **jembatan stdio JSON-RPC 2.0** — ia membaca permintaan dari stdin, memproksi ke HTTP API Tetora, dan menulis respons ke stdout. Claude Code meluncurkannya sebagai proses anak.

## Pengaturan

### 1. Tambahkan server MCP ke pengaturan Claude Code

Tambahkan berikut ini ke `~/.claude/settings.json`:

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

Ganti path dengan lokasi binary `tetora` Anda yang sebenarnya. Temukan dengan:

```bash
which tetora
# atau
ls ~/.tetora/bin/tetora
```

### 2. Pastikan daemon Tetora berjalan

Jembatan MCP memproksi ke HTTP API Tetora, sehingga daemon harus berjalan:

```bash
tetora start
```

### 3. Verifikasi

Restart Claude Code. Tool MCP akan muncul sebagai tool yang tersedia dengan prefiks `tetora_`.

## Tool yang Tersedia

### Manajemen Task

| Tool | Deskripsi |
|------|-------------|
| `tetora_taskboard_list` | Daftar tiket papan kanban. Filter opsional: `project`, `assignee`, `priority`. |
| `tetora_taskboard_update` | Perbarui task (status, assignee, priority, title). Memerlukan `id`. |
| `tetora_taskboard_comment` | Tambahkan komentar ke task. Memerlukan `id` dan `comment`. |

### Memory

| Tool | Deskripsi |
|------|-------------|
| `tetora_memory_get` | Baca entri memory. Memerlukan `agent` dan `key`. |
| `tetora_memory_set` | Tulis entri memory. Memerlukan `agent`, `key`, dan `value`. |
| `tetora_memory_search` | Daftar semua entri memory. Filter opsional: `role`. |

### Dispatch

| Tool | Deskripsi |
|------|-------------|
| `tetora_dispatch` | Dispatch task ke agent lain. Membuat session Claude Code baru. Memerlukan `prompt`. Opsional: `agent`, `workdir`, `model`. |

### Knowledge

| Tool | Deskripsi |
|------|-------------|
| `tetora_knowledge_search` | Cari knowledge base bersama. Memerlukan `q`. Opsional: `limit`. |

### Notifikasi

| Tool | Deskripsi |
|------|-------------|
| `tetora_notify` | Kirim notifikasi ke pengguna melalui Discord/Telegram. Memerlukan `message`. Opsional: `level` (info/warn/error). |
| `tetora_ask_user` | Ajukan pertanyaan ke pengguna melalui Discord dan tunggu respons (hingga 6 menit). Memerlukan `question`. Opsional: `options` (tombol quick-reply, maks 4). |

## Detail Tool

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

Semua parameter opsional. Mengembalikan array JSON task.

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

Hanya `id` yang wajib. Field lain diperbarui hanya jika disediakan. Nilai status: `todo`, `in_progress`, `review`, `done`.

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

Hanya `prompt` yang wajib. Jika `agent` dihilangkan, smart dispatch Tetora merutekan ke agent terbaik.

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

Ini adalah **panggilan pemblokir** — menunggu hingga 6 menit agar pengguna merespons melalui Discord. Pengguna melihat pertanyaan dengan tombol quick-reply opsional dan juga dapat mengetikkan jawaban kustom.

## Perintah CLI

### Mengelola Server MCP Eksternal

Tetora juga dapat bertindak sebagai **host** MCP, terhubung ke server MCP eksternal:

```bash
# Daftar server MCP yang dikonfigurasi
tetora mcp list

# Tampilkan konfigurasi lengkap untuk server
tetora mcp show <name>

# Tambahkan server MCP baru
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# Hapus konfigurasi server
tetora mcp remove <name>

# Uji koneksi server
tetora mcp test <name>
```

### Menjalankan Jembatan MCP

```bash
# Mulai server jembatan MCP (biasanya diluncurkan oleh Claude Code, bukan secara manual)
tetora mcp-server
```

Pada jalannya pertama, ini menghasilkan `~/.tetora/mcp/bridge.json` dengan path binary yang benar.

## Konfigurasi

Pengaturan terkait MCP di `config.json`:

| Field | Tipe | Default | Deskripsi |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | Peta konfigurasi server MCP eksternal (nama → {command, args, env}). |

Server jembatan membaca `listenAddr` dan `apiToken` dari konfigurasi utama untuk terhubung ke daemon.

## Autentikasi

Jika `apiToken` diset di `config.json`, jembatan MCP secara otomatis menyertakan `Authorization: Bearer <token>` dalam semua permintaan HTTP ke daemon. Tidak diperlukan autentikasi tambahan di level MCP.

## Pemecahan Masalah

**Tool tidak muncul di Claude Code:**
- Verifikasi path binary di `settings.json` sudah benar
- Pastikan daemon Tetora berjalan (`tetora start`)
- Periksa log Claude Code untuk error koneksi MCP

**Error "HTTP 401":**
- `apiToken` di `config.json` harus cocok. Jembatan membacanya secara otomatis.

**Error "connection refused":**
- Daemon tidak berjalan, atau `listenAddr` tidak cocok. Default: `127.0.0.1:8991`.

**`tetora_ask_user` timeout:**
- Pengguna memiliki 6 menit untuk merespons melalui Discord. Pastikan bot Discord terhubung.
