# Menginstal Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.zh-CN.md">简体中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.pt.md">Português</a> | <strong>Bahasa Indonesia</strong>
</p>

---

## Persyaratan Sistem

| Persyaratan | Detail |
|---|---|
| **Sistem operasi** | macOS, Linux, atau Windows (WSL) |
| **Terminal** | Emulator terminal apa pun |
| **sqlite3** | Harus tersedia di `PATH` |
| **Penyedia AI** | Lihat Jalur 1 atau Jalur 2 di bawah |

### Menginstal sqlite3

**macOS:**
```bash
brew install sqlite3
```

**Ubuntu / Debian:**
```bash
sudo apt install sqlite3
```

**Fedora / RHEL:**
```bash
sudo dnf install sqlite
```

**Windows (WSL):** Instal di dalam distribusi WSL Anda menggunakan perintah Linux di atas.

---

## Mengunduh Tetora

Kunjungi [halaman Releases](https://github.com/TakumaLee/Tetora/releases/latest) dan unduh binary untuk platform Anda:

| Platform | File |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Gunakan binary Linux di dalam WSL |

**Menginstal binary:**
```bash
# Ganti nama file dengan yang Anda unduh
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Pastikan ~/.tetora/bin ada di PATH Anda
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # atau ~/.bashrc
source ~/.zshrc
```

**Atau gunakan penginstal satu baris (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Jalur 1: Claude Pro ($20/bulan) — Direkomendasikan untuk pemula

Jalur ini menggunakan **Claude Code CLI** sebagai backend AI. Membutuhkan langganan Claude Pro aktif ($20/bulan di [claude.ai](https://claude.ai)).

> **Mengapa jalur ini?** Tidak perlu mengelola API key, tidak ada tagihan penggunaan yang mengejutkan. Langganan Pro Anda mencakup semua penggunaan Tetora melalui Claude Code.

> [!IMPORTANT]
> **Prasyarat:** Jalur ini memerlukan langganan Claude Pro aktif ($20/bulan). Jika belum berlangganan, kunjungi [claude.ai/upgrade](https://claude.ai/upgrade) terlebih dahulu.

### Langkah 1: Menginstal Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

Jika belum ada Node.js, instal terlebih dahulu:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)
- **Windows (WSL):** Ikuti instruksi Linux di atas

Verifikasi instalasi:
```bash
claude --version
```

Masuk dengan akun Claude Pro Anda:
```bash
claude
# Ikuti alur login berbasis browser
```

### Langkah 2: Jalankan tetora init

```bash
tetora init
```

Wizard pengaturan akan memandu Anda melalui:
1. **Pilih bahasa** — pilih bahasa yang Anda inginkan
2. **Pilih saluran pesan** — Telegram, Discord, Slack, atau Tidak Ada
3. **Pilih penyedia AI** — pilih **"Claude Code CLI"**
   - Wizard mendeteksi lokasi binary `claude` secara otomatis
   - Tekan Enter untuk menerima jalur yang terdeteksi
4. **Pilih akses direktori** — folder mana yang dapat dibaca/ditulis Tetora
5. **Buat peran agen pertama Anda** — beri nama dan kepribadian

### Langkah 3: Verifikasi dan mulai

```bash
# Periksa apakah semuanya dikonfigurasi dengan benar
tetora doctor

# Mulai daemon
tetora serve
```

Buka dasbor web:
```bash
tetora dashboard
```

---

## Jalur 2: API Key

Jalur ini menggunakan API key langsung. Penyedia yang didukung:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Endpoint kompatibel OpenAI** — Ollama, LM Studio, Azure OpenAI, dll.

> **Catatan biaya:** Penggunaan API ditagih per token. Periksa harga penyedia Anda sebelum mengaktifkan model mahal atau alur kerja frekuensi tinggi.

### Langkah 1: Dapatkan API key Anda

**Claude API:**
1. Kunjungi [console.anthropic.com](https://console.anthropic.com)
2. Buat akun atau masuk
3. Navigasi ke **API Keys** → **Create Key**
4. Salin key (dimulai dengan `sk-ant-...`)

**OpenAI:**
1. Kunjungi [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Klik **Create new secret key**
3. Salin key (dimulai dengan `sk-...`)

**Kompatibel OpenAI (mis., Ollama):**
```bash
# Mulai server Ollama lokal
ollama serve
# Endpoint default: http://localhost:11434/v1
# Tidak diperlukan API key untuk model lokal
```

### Langkah 2: Jalankan tetora init

```bash
tetora init
```

Wizard pengaturan akan memandu Anda:
1. **Pilih bahasa**
2. **Pilih saluran pesan**
3. **Pilih penyedia AI:**
   - Pilih **"Claude API Key"** untuk Anthropic Claude
   - Pilih **"Endpoint kompatibel OpenAI"** untuk OpenAI atau model lokal
4. **Masukkan API key Anda** (atau URL endpoint untuk model lokal)
5. **Pilih akses direktori**
6. **Buat peran agen pertama Anda**

### Langkah 3: Verifikasi dan mulai

```bash
tetora doctor
tetora serve
```

---

## Wizard Pengaturan Web (untuk non-engineer)

Jika Anda lebih suka pengalaman pengaturan grafis, gunakan wizard web:

```bash
tetora setup --web
```

Ini membuka jendela browser di `http://localhost:7474` dengan wizard pengaturan 4 langkah. Tidak diperlukan konfigurasi terminal.

---

## Setelah Instalasi

| Perintah | Deskripsi |
|---|---|
| `tetora doctor` | Pemeriksaan kesehatan — jalankan ini jika ada yang tidak beres |
| `tetora serve` | Mulai daemon (bot + HTTP API + pekerjaan terjadwal) |
| `tetora dashboard` | Buka dasbor web |
| `tetora status` | Ikhtisar status cepat |
| `tetora init` | Jalankan ulang wizard pengaturan untuk mengubah konfigurasi |

### File konfigurasi

Semua pengaturan disimpan di `~/.tetora/config.json`. Anda dapat mengedit file ini langsung — jalankan `tetora serve` lagi untuk menerapkan perubahan, atau kirim `SIGHUP` untuk memuat ulang tanpa memulai ulang:

```bash
kill -HUP $(pgrep tetora)
```

---

## Pemecahan Masalah

### `tetora: command not found`

Pastikan `~/.tetora/bin` ada di `PATH` Anda:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Instal sqlite3 untuk platform Anda (lihat Persyaratan Sistem di atas).

### `tetora doctor` melaporkan kesalahan penyedia

- **Jalur Claude Code CLI:** Jalankan `which claude` dan perbarui `claudePath` di `~/.tetora/config.json`
- **API key tidak valid:** Periksa kembali key Anda di konsol penyedia
- **Model tidak ditemukan:** Verifikasi nama model sesuai dengan tier langganan Anda

### Masalah login Claude Code

```bash
# Autentikasi ulang
claude logout
claude
```

### Permission denied pada binary

```bash
chmod +x ~/.tetora/bin/tetora
```

### Port 8991 sudah digunakan

Edit `~/.tetora/config.json` dan ubah `listenAddr` ke port yang bebas:
```json
"listenAddr": "127.0.0.1:9000"
```

---

## Build dari Source

Membutuhkan Go 1.25+:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

Ini membangun dan menginstal ke `~/.tetora/bin/tetora`.

---

## Langkah Selanjutnya

- Baca [README](README.md) untuk dokumentasi fitur lengkap
- Bergabung dengan komunitas: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Laporkan masalah: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
