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
- **Dashboard web** -- pusat komando CEO dengan metrik ROI, kantor pixel, dan umpan aktivitas langsung
- **Mesin workflow** -- eksekusi pipeline berbasis DAG dengan cabang kondisi, langkah paralel, logika percobaan ulang, dan routing model dinamis (Sonnet untuk tugas rutin, Opus untuk yang kompleks)
- **Marketplace template** -- tab Store untuk menjelajahi, mengimpor, dan mengekspor template workflow
- **Dispatch otomatis taskboard** -- papan Kanban dengan penugasan tugas otomatis, slot bersamaan yang dapat dikonfigurasi, dan sistem tekanan slot yang menyimpan kapasitas untuk sesi interaktif
- **GitLab MR + GitHub PR** -- pembuatan PR/MR otomatis setelah workflow selesai; deteksi otomatis host remote
- **Pemadatan sesi** -- kompresi konteks otomatis berbasis token dan jumlah pesan untuk menjaga sesi dalam batas model
- **Service Worker PWA** -- dashboard dengan kemampuan offline dan caching cerdas
- **Status selesai sebagian** -- tugas yang selesai tetapi gagal di post-processing (git merge, review) masuk ke status antara yang dapat dipulihkan, bukan hilang
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

## Dashboard

Tetora dilengkapi dashboard web bawaan di `http://localhost:8991/dashboard`. Dashboard diatur dalam empat zona:

| Zona | Isi |
|------|----------|
| **Pusat Komando** | Ringkasan eksekutif (kartu ROI), sprite tim pixel, kantor Agent World yang dapat diperluas |
| **Operasi** | Bar ops ringkas, kartu skor agen + umpan aktivitas langsung (berdampingan), tugas berjalan |
| **Wawasan** | Grafik tren 7 hari, grafik historis throughput dan biaya tugas |
| **Detail Engineering** | Dashboard biaya, cron job, sesi, kesehatan provider, kepercayaan, SLA, riwayat versi, routing, memori, dan lainnya (dapat dilipat) |

Editor agen mencakup **pemilih model sadar-provider** dengan peralihan satu klik antara model cloud dan lokal (Ollama). **Toggle mode inferensi** global memungkinkan Anda mengalihkan semua agen antara cloud dan lokal dengan satu tombol. Setiap kartu agen menampilkan lencana Cloud/Local dan dropdown peralihan cepat.

Tersedia beberapa tema (Glass, Clean, Material, Boardroom, Retro). Kantor pixel Agent World dapat dikustomisasi dengan dekorasi dan kontrol zoom.

```bash
# Buka dashboard di browser default Anda
tetora dashboard
```

---

## Perintah Discord

Tetora merespons perintah dengan awalan `!` di Discord:

| Perintah | Deskripsi |
|---------|-------------|
| `!model` | Tampilkan semua agen dikelompokkan berdasarkan Cloud / Local |
| `!model pick [agent]` | Pemilih model interaktif (tombol + dropdown) |
| `!model <model> [agent]` | Atur model secara langsung (deteksi otomatis provider) |
| `!local [agent]` | Beralih ke model lokal (Ollama) |
| `!cloud [agent]` | Pulihkan model cloud |
| `!mode` | Ringkasan mode inferensi dengan tombol toggle |
| `!chat <agent>` | Kunci channel ke agen tertentu |
| `!end` | Buka kunci channel, lanjutkan dispatch cerdas |
| `!new` | Mulai sesi baru |
| `!ask <prompt>` | Pertanyaan sekali pakai |
| `!cancel` | Batalkan semua tugas yang berjalan |
| `!approve [tool\|reset]` | Kelola tool yang disetujui otomatis |
| `!status` / `!cost` / `!jobs` | Gambaran operasi |
| `!help` | Tampilkan referensi perintah |
| `@Tetora <text>` | Dispatch cerdas ke agen terbaik |

**[Referensi Lengkap Perintah Discord](docs/discord-commands.md)** -- peralihan model, toggle remote/lokal, konfigurasi provider, dan lainnya.

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
  bin/               Binary yang terinstal
  agents/            Soul file per agen (agents/{name}/SOUL.md)
  workspace/
    rules/           Aturan tata kelola, auto-injeksi ke semua prompt agen
    memory/          Pengamatan bersama, dapat dibaca/ditulis oleh semua agen
    knowledge/       Materi referensi (auto-injeksi hingga 50 KB)
    skills/          Prosedur yang dapat digunakan kembali, dimuat melalui pencocokan prompt
    tasks/           File tugas dan daftar pekerjaan
  runtime/
    sessions/        File sesi per agen
    outputs/         File output yang dihasilkan
    logs/            File log terstruktur
    cache/           Cache sementara
```

Konfigurasi menggunakan JSON biasa dengan dukungan referensi `$ENV_VAR`, sehingga rahasia tidak perlu di-hardcode. Wizard pengaturan (`tetora init`) menghasilkan `config.json` yang berfungsi secara interaktif.

Hot-reload didukung: kirim `SIGHUP` ke daemon yang berjalan untuk memuat ulang `config.json` tanpa downtime.

---

## Workflow

Tetora dilengkapi dengan mesin workflow bawaan untuk mengorkestrasikan tugas multi-langkah dan multi-agen. Definisikan pipeline Anda dalam JSON, dan biarkan agen berkolaborasi secara otomatis.

**[Dokumentasi Workflow Lengkap](docs/workflow.id.md)** — jenis langkah, variabel, trigger, referensi CLI & API.

Contoh cepat:

```bash
# Validasi dan impor workflow
tetora workflow create examples/workflow-basic.json

# Jalankan
tetora workflow run research-and-summarize --var topic="LLM safety"

# Periksa hasil
tetora workflow status <run-id>
```

Lihat [`examples/`](examples/) untuk file JSON workflow yang siap digunakan.

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
| `tetora workflow run <name>` | Jalankan workflow (gunakan `--var key=value` untuk variabel) |
| `tetora workflow status <run-id>` | Tampilkan status eksekusi workflow |
| `tetora workflow export <name>` | Ekspor workflow ke file JSON yang dapat dibagikan |
| `tetora workflow create <file>` | Validasi dan impor workflow dari file JSON |
| `tetora mcp list` | Daftar koneksi server MCP |
| `tetora budget show` | Tampilkan status anggaran |
| `tetora config show` | Tampilkan konfigurasi saat ini |
| `tetora config validate` | Validasi config.json |
| `tetora backup` | Buat arsip cadangan |
| `tetora restore <file>` | Pulihkan dari arsip cadangan |
| `tetora dashboard` | Buka dashboard web di browser |
| `tetora logs` | Lihat log daemon (`-f` untuk mengikuti, `--json` untuk output terstruktur) |
| `tetora health` | Kesehatan runtime (daemon, worker, taskboard, disk) |
| `tetora drain` | Shutdown elegan: hentikan tugas baru, tunggu agen yang berjalan |
| `tetora data status` | Tampilkan status retensi data |
| `tetora security scan` | Pemindaian keamanan dan baseline |
| `tetora prompt list` | Kelola template prompt |
| `tetora project add` | Tambahkan proyek ke workspace |
| `tetora guide` | Panduan onboarding interaktif |
| `tetora upgrade` | Upgrade ke versi terbaru |
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
