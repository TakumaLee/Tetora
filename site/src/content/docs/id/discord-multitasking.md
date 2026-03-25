---
title: "Panduan Multitasking Discord"
lang: "id"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# Panduan Multitasking Discord

Tetora mendukung percakapan paralel di Discord melalui **Thread + `/focus`**, di mana setiap thread memiliki session dan binding agent yang independen.

---

## Konsep Dasar

### Channel Utama — Session Tunggal

Setiap Discord channel hanya memiliki **satu active session**, semua pesan berbagi konteks percakapan yang sama.

- Format session key: `discord:{channelID}`
- Pesan dari semua orang dalam channel yang sama masuk ke session yang sama
- Riwayat percakapan terus terakumulasi hingga Anda mereset dengan `!new`

### Thread — Session Independen

Discord thread dapat diikat ke agent tertentu melalui `/focus`, mendapatkan session yang sepenuhnya independen.

- Format session key: `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- Sepenuhnya terisolasi dari session channel utama, konteks tidak saling mempengaruhi
- Setiap thread dapat diikat ke agent yang berbeda

---

## Perintah

| Perintah | Lokasi | Deskripsi |
|---|---|---|
| `/focus <agent>` | Di dalam Thread | Ikat thread ini ke agent tertentu, buat session independen |
| `/unfocus` | Di dalam Thread | Lepaskan binding agent dari thread |
| `!new` | Channel Utama | Arsipkan session saat ini, pesan berikutnya akan membuka session baru |

---

## Alur Multitasking

### Langkah 1: Buat Discord Thread

Klik kanan pada pesan di channel utama → **Create Thread** (atau gunakan fitur pembuatan thread Discord).

### Langkah 2: Ikat Agent di Dalam Thread

```
/focus ruri
```

Setelah binding berhasil, semua percakapan dalam thread ini akan:
- Menggunakan pengaturan karakter SOUL.md milik ruri
- Memiliki riwayat percakapan yang independen
- Tidak mempengaruhi session channel utama

### Langkah 3: Buka Beberapa Thread Sesuai Kebutuhan

```
#general (channel utama)              ← percakapan sehari-hari, 1 session
  └─ Thread: "重構 auth 模組"      ← /focus kokuyou → session independen
  └─ Thread: "寫這週部落格"        ← /focus kohaku  → session independen
  └─ Thread: "競品分析報告"        ← /focus hisui   → session independen
  └─ Thread: "專案規劃討論"        ← /focus ruri    → session independen
```

Setiap thread adalah ruang percakapan yang sepenuhnya terisolasi dan dapat berjalan secara bersamaan.

---

## Catatan Penting

### TTL (Time to Live)

- Binding thread kedaluwarsa setelah **24 jam** secara default
- Setelah kedaluwarsa, thread kembali ke mode normal (mengikuti logika routing channel utama)
- Dapat disesuaikan di config dengan `threadBindings.ttlHours`

### Batas Konkurensi

- Jumlah maksimum konkurensi global dikendalikan oleh `maxConcurrent` (default 8)
- Semua channel + thread berbagi batas ini
- Pesan yang melebihi batas akan mengantri menunggu

### Aktifkan Konfigurasi

Pastikan thread bindings diaktifkan di config:

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

### Batasan Channel Utama

- Channel utama tidak dapat menggunakan `/focus` untuk membuat session kedua
- Untuk mereset konteks percakapan, gunakan `!new`
- Mengirim beberapa pesan secara bersamaan dalam channel yang sama akan berbagi session, konteks mungkin saling mengganggu

---

## Rekomendasi Berdasarkan Situasi

| Situasi | Pendekatan yang Disarankan |
|---|---|
| Obrolan sehari-hari, tanya jawab sederhana | Langsung bicara di channel utama |
| Perlu diskusi mendalam tentang topik tertentu | Buka thread + `/focus` |
| Tugaskan task berbeda ke agent yang berbeda | Satu thread per task, masing-masing `/focus` ke agent yang sesuai |
| Konteks percakapan terlalu panjang ingin mulai ulang | Gunakan `!new` di channel utama, `/unfocus` lalu `/focus` di thread |
| Kolaborasi banyak orang dalam topik yang sama | Buka satu thread bersama, semua orang berdiskusi di dalam thread |
