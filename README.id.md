<p align="center">
  <img src="assets/banner.png" alt="Tetora — Orkestrator Agen AI" width="800">
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | **Bahasa Indonesia** | [ภาษาไทย](README.th.md) | [Filipino](README.fil.md) | [Español](README.es.md) | [Français](README.fr.md) | [Deutsch](README.de.md)

<p align="center">
  <strong>Platform asisten AI self-hosted dengan arsitektur multi-agen.</strong>
</p>

Tetora berjalan sebagai satu binary Go tanpa dependensi eksternal. Tetora terhubung ke penyedia AI yang sudah Anda gunakan, terintegrasi dengan platform pesan yang digunakan tim Anda sehari-hari, dan menyimpan semua data di perangkat keras Anda sendiri.

---

## Apa itu Tetora

Tetora adalah orkestrator agen AI yang memungkinkan Anda mendefinisikan beberapa peran agen -- masing-masing dengan kepribadian, system prompt, model, dan akses tool tersendiri -- dan berinteraksi dengan mereka melalui platform chat, HTTP API, atau command line.

**Kemampuan utama:**

- **Peran multi-agen** -- definisikan agen berbeda dengan kepribadian, anggaran, dan izin tool masing-masing
- **Multi-provider** -- Claude API, OpenAI, Gemini, dan lainnya; tukar atau kombinasikan dengan bebas
- **Multi-platform** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Cron job** -- jadwalkan tugas berulang dengan gerbang persetujuan dan notifikasi
- **Basis pengetahuan** -- berikan dokumen ke agen untuk respons yang lebih akurat
- **Memori persisten** -- agen mengingat konteks lintas sesi; lapisan memori terpadu dengan konsolidasi
- **Dukungan MCP** -- hubungkan server Model Context Protocol sebagai penyedia tool
- **Skill dan workflow** -- paket skill yang dapat digabungkan dan pipeline workflow multi-langkah
- **Webhook** -- picu aksi agen dari sistem eksternal
- **Tata kelola biaya** -- anggaran per-peran dan global dengan penurunan model otomatis
- **Retensi data** -- kebijakan pembersihan yang dapat dikonfigurasi per tabel, dengan ekspor dan pembersihan penuh
- **Plugin** -- perluas fungsionalitas melalui proses plugin eksternal
- **Pengingat cerdas, kebiasaan, tujuan, kontak, pelacakan keuangan, briefing, dan lainnya**

---

## Mulai Cepat

### Untuk engineer

```bash
# Instal rilis terbaru
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# Jalankan wizard pengaturan
tetora init

# Verifikasi bahwa semuanya terkonfigurasi dengan benar
tetora doctor

# Mulai daemon
tetora serve
```

### Untuk non-engineer

1. Kunjungi [halaman Releases](https://github.com/TakumaLee/Tetora/releases/latest)
2. Unduh binary untuk platform Anda (contoh: `tetora-darwin-arm64` untuk Mac Apple Silicon)
3. Pindahkan ke direktori di PATH Anda dan ganti namanya menjadi `tetora`, atau letakkan di `~/.tetora/bin/`
4. Buka terminal dan jalankan:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## Agen

Setiap agen Tetora lebih dari sekadar chatbot -- ia memiliki identitas. Setiap agen (disebut **role**) didefinisikan oleh sebuah **soul file**: dokumen Markdown yang memberikan kepribadian, keahlian, gaya komunikasi, dan pedoman perilaku kepada agen tersebut.

### Mendefinisikan role

Role dideklarasikan di `config.json` di bawah key `roles`:

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

Soul file memberi tahu agen *siapa dirinya*. Letakkan di direktori workspace (`~/.tetora/workspace/` secara default):

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

### Memulai

`tetora init` akan memandu Anda membuat role pertama dan menghasilkan soul file awal secara otomatis. Anda dapat mengeditnya kapan saja -- perubahan berlaku pada sesi berikutnya.

---

## Build dari Source

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

Perintah ini mem-build binary dan menginstalnya ke `~/.tetora/bin/tetora`. Pastikan `~/.tetora/bin` ada di `PATH` Anda.

Untuk menjalankan test suite:

```bash
make test
```

---

## Persyaratan

| Persyaratan | Detail |
|---|---|
| **sqlite3** | Harus tersedia di `PATH`. Digunakan untuk semua penyimpanan persisten. |
| **API key penyedia AI** | Minimal satu: Claude API, OpenAI, Gemini, atau endpoint yang kompatibel dengan OpenAI. |
| **Go 1.25+** | Hanya diperlukan jika build dari source. |

---

## Platform yang Didukung

| Platform | Arsitektur | Status |
|---|---|---|
| macOS | amd64, arm64 | Stabil |
| Linux | amd64, arm64 | Stabil |
| Windows | amd64 | Beta |

---

## Arsitektur

Semua data runtime berada di bawah `~/.tetora/`:

```
~/.tetora/
  config.json        Konfigurasi utama (provider, role, integrasi)
  jobs.json          Definisi cron job
  history.db         Database SQLite (riwayat, memori, sesi, embedding, ...)
  sessions/          File sesi per-agen
  knowledge/         Dokumen basis pengetahuan
  logs/              File log terstruktur
  outputs/           File output yang dihasilkan
  uploads/           Penyimpanan upload sementara
  bin/               Binary yang terinstal
```

Konfigurasi menggunakan JSON biasa dengan dukungan referensi `$ENV_VAR`, sehingga rahasia tidak perlu di-hardcode. Wizard pengaturan (`tetora init`) menghasilkan `config.json` yang berfungsi secara interaktif.

Hot-reload didukung: kirim `SIGHUP` ke daemon yang berjalan untuk memuat ulang `config.json` tanpa downtime.

---

## Referensi CLI

| Perintah | Deskripsi |
|---|---|
| `tetora init` | Wizard pengaturan interaktif |
| `tetora doctor` | Pemeriksaan kesehatan dan diagnostik |
| `tetora serve` | Mulai daemon (chat bot + HTTP API + cron) |
| `tetora run --file tasks.json` | Jalankan tugas dari file JSON (mode CLI) |
| `tetora dispatch "Summarize this"` | Jalankan tugas ad-hoc melalui daemon |
| `tetora route "Review code security"` | Dispatch cerdas -- rute otomatis ke role terbaik |
| `tetora status` | Gambaran singkat daemon, job, dan biaya |
| `tetora job list` | Daftar semua cron job |
| `tetora job trigger <name>` | Picu cron job secara manual |
| `tetora role list` | Daftar semua role yang terkonfigurasi |
| `tetora role show <name>` | Tampilkan detail role dan pratinjau soul |
| `tetora history list` | Tampilkan riwayat eksekusi terbaru |
| `tetora history cost` | Tampilkan ringkasan biaya |
| `tetora session list` | Daftar sesi terbaru |
| `tetora memory list` | Daftar entri memori agen |
| `tetora knowledge list` | Daftar dokumen basis pengetahuan |
| `tetora skill list` | Daftar skill yang tersedia |
| `tetora workflow list` | Daftar workflow yang terkonfigurasi |
| `tetora mcp list` | Daftar koneksi server MCP |
| `tetora budget show` | Tampilkan status anggaran |
| `tetora config show` | Tampilkan konfigurasi saat ini |
| `tetora config validate` | Validasi config.json |
| `tetora backup` | Buat arsip cadangan |
| `tetora restore <file>` | Pulihkan dari arsip cadangan |
| `tetora dashboard` | Buka dashboard web di browser |
| `tetora logs` | Lihat log daemon (`-f` untuk mengikuti, `--json` untuk output terstruktur) |
| `tetora data status` | Tampilkan status retensi data |
| `tetora service install` | Instal sebagai layanan launchd (macOS) |
| `tetora completion <shell>` | Hasilkan completions shell (bash, zsh, fish) |
| `tetora version` | Tampilkan versi |

Jalankan `tetora help` untuk referensi perintah lengkap.

---

## Kontribusi

Kontribusi sangat diterima. Silakan buka issue untuk mendiskusikan perubahan besar sebelum mengirimkan pull request.

- **Issues**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Diskusi**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

Proyek ini dilisensikan di bawah AGPL-3.0, yang mengharuskan karya turunan dan deployment yang dapat diakses melalui jaringan juga bersifat open source di bawah lisensi yang sama. Harap tinjau lisensi sebelum berkontribusi.

---

## Lisensi

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Hak Cipta (c) Kontributor Tetora.
