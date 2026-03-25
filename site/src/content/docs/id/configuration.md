---
title: "Referensi Konfigurasi"
lang: "id"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# Referensi Konfigurasi

## Gambaran Umum

Tetora dikonfigurasi melalui satu file JSON yang terletak di `~/.tetora/config.json`.

**Perilaku utama:**

- **Substitusi `$ENV_VAR`** — setiap nilai string yang diawali dengan `$` akan diganti dengan environment variable yang sesuai saat startup. Gunakan ini untuk menyimpan rahasia (API key, token) daripada hardcoding.
- **Hot-reload** — mengirim `SIGHUP` ke daemon akan memuat ulang konfigurasi. Konfigurasi yang bermasalah akan ditolak dan konfigurasi yang sedang berjalan tetap dipertahankan; daemon tidak akan crash.
- **Path relatif** — `jobsFile`, `historyDB`, `defaultWorkdir`, dan field direktori diselesaikan relatif terhadap direktori file konfigurasi (`~/.tetora/`).
- **Kompatibilitas mundur** — key lama `"roles"` adalah alias untuk `"agents"`. Key lama `"defaultRole"` di dalam `smartDispatch` adalah alias untuk `"defaultAgent"`.

---

## Field Tingkat Atas

### Pengaturan Inti

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | Alamat listen HTTP untuk API dan dashboard. Format: `host:port`. |
| `apiToken` | string | `""` | Bearer token yang diperlukan untuk semua permintaan API. Kosong berarti tanpa autentikasi (tidak disarankan untuk produksi). Mendukung `$ENV_VAR`. |
| `maxConcurrent` | int | `8` | Jumlah maksimum task agent yang berjalan secara bersamaan. Nilai di atas 20 menghasilkan peringatan saat startup. |
| `defaultModel` | string | `"sonnet"` | Nama model Claude default. Diteruskan ke provider kecuali jika di-override per agent. |
| `defaultTimeout` | string | `"1h"` | Timeout task default. Format durasi Go: `"15m"`, `"1h"`, `"30s"`. |
| `defaultBudget` | float64 | `0` | Anggaran biaya default per task dalam USD. `0` berarti tanpa batas. |
| `defaultPermissionMode` | string | `"acceptEdits"` | Mode izin Claude default. Nilai umum: `"acceptEdits"`, `"default"`. |
| `defaultAgent` | string | `""` | Nama agent fallback di seluruh sistem ketika tidak ada aturan routing yang cocok. |
| `defaultWorkdir` | string | `""` | Direktori kerja default untuk task agent. Harus ada di disk. |
| `claudePath` | string | `"claude"` | Path ke binary CLI `claude`. Default mencari `claude` di `$PATH`. |
| `defaultProvider` | string | `"claude"` | Nama provider yang digunakan ketika tidak ada override di level agent. |
| `log` | bool | `false` | Flag lama untuk mengaktifkan logging ke file. Sebaiknya gunakan `logging.level`. |
| `maxPromptLen` | int | `102400` | Panjang maksimum prompt dalam byte (100 KB). Permintaan yang melebihi ini akan ditolak. |
| `configVersion` | int | `0` | Versi skema konfigurasi. Digunakan untuk migrasi otomatis. Jangan diset secara manual. |
| `encryptionKey` | string | `""` | Kunci AES untuk enkripsi field-level data sensitif. Mendukung `$ENV_VAR`. |
| `streamToChannels` | bool | `false` | Stream status task secara langsung ke channel messaging yang terhubung (Discord, Telegram, dll.). |
| `cronNotify` | bool\|null | `null` (true) | `false` menekan semua notifikasi penyelesaian cron job. `null` atau `true` mengaktifkannya. |
| `cronReplayHours` | int | `2` | Berapa jam yang akan dilihat ke belakang untuk cron job yang terlewat saat daemon startup. |
| `diskBudgetGB` | float64 | `1.0` | Ruang disk bebas minimum dalam GB. Cron job ditolak di bawah level ini. |
| `diskWarnMB` | int | `500` | Ambang batas peringatan disk bebas dalam MB. Mencatat WARN tetapi job tetap berjalan. |
| `diskBlockMB` | int | `200` | Ambang batas blokir disk bebas dalam MB. Job dilewati dengan status `skipped_disk_full`. |

### Override Direktori

Secara default semua direktori berada di bawah `~/.tetora/`. Override hanya jika Anda membutuhkan tata letak non-standar.

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | Direktori untuk file knowledge workspace. |
| `agentsDir` | string | `~/.tetora/agents/` | Direktori yang berisi file SOUL.md per agent. |
| `workspaceDir` | string | `~/.tetora/workspace/` | Direktori untuk rules, memory, skills, drafts, dll. |
| `runtimeDir` | string | `~/.tetora/runtime/` | Direktori untuk sessions, outputs, logs, cache. |
| `vaultDir` | string | `~/.tetora/vault/` | Direktori untuk vault rahasia terenkripsi. |
| `historyDB` | string | `history.db` | Path database SQLite untuk riwayat job. Relatif terhadap direktori konfigurasi. |
| `jobsFile` | string | `jobs.json` | Path ke file definisi cron job. Relatif terhadap direktori konfigurasi. |

### Direktori yang Diizinkan Secara Global

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| Field | Tipe | Deskripsi |
|---|---|---|
| `allowedDirs` | string[] | Direktori yang diizinkan dibaca dan ditulis oleh agent. Diterapkan secara global; dapat dipersempit per agent. |
| `defaultAddDirs` | string[] | Direktori yang disuntikkan sebagai `--add-dir` untuk setiap task (konteks read-only). |
| `allowedIPs` | string[] | Alamat IP atau rentang CIDR yang diizinkan memanggil API. Kosong = izinkan semua. Contoh: `["192.168.1.0/24", "10.0.0.1"]`. |

---

## Provider

Provider mendefinisikan cara Tetora menjalankan task agent. Beberapa provider dapat dikonfigurasi dan dipilih per agent.

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `type` | string | wajib | Tipe provider. Salah satu dari: `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"`. |
| `path` | string | `""` | Path binary. Digunakan oleh tipe `claude-cli` dan `claude-code`. Jatuh kembali ke `claudePath` jika kosong. |
| `baseUrl` | string | `""` | URL base API. Wajib untuk `openai-compatible`. |
| `apiKey` | string | `""` | API key. Mendukung `$ENV_VAR`. Wajib untuk `claude-api`; opsional untuk `openai-compatible`. |
| `model` | string | `""` | Model default untuk provider ini. Menggantikan `defaultModel` untuk task yang menggunakan provider ini. |
| `maxTokens` | int | `8192` | Maksimum token output (digunakan oleh `claude-api`). |
| `firstTokenTimeout` | string | `"60s"` | Berapa lama menunggu token respons pertama sebelum timeout (SSE stream). |

**Tipe provider:**
- `claude-cli` — menjalankan binary `claude` sebagai subprocess (default, paling kompatibel)
- `claude-api` — memanggil Anthropic API langsung menggunakan HTTP (memerlukan `ANTHROPIC_API_KEY`)
- `openai-compatible` — REST API yang kompatibel dengan OpenAI (OpenAI, Ollama, Groq, dll.)
- `claude-code` — menggunakan mode CLI Claude Code

---

## Agent

Agent mendefinisikan persona bernama dengan model, soul file, dan akses tool masing-masing.

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `soulFile` | string | wajib | Path ke file kepribadian SOUL.md agent, relatif terhadap `agentsDir`. |
| `model` | string | `defaultModel` | Model yang digunakan untuk agent ini. |
| `description` | string | `""` | Deskripsi yang mudah dibaca manusia. Juga digunakan oleh classifier LLM untuk routing. |
| `keywords` | string[] | `[]` | Kata kunci yang memicu routing ke agent ini dalam smart dispatch. |
| `provider` | string | `defaultProvider` | Nama provider (key dalam map `providers`). |
| `permissionMode` | string | `defaultPermissionMode` | Mode izin Claude untuk agent ini. |
| `allowedDirs` | string[] | `allowedDirs` | Path filesystem yang dapat diakses agent ini. Menggantikan pengaturan global. |
| `docker` | bool\|null | `null` | Override sandbox Docker per agent. `null` = mewarisi `docker.enabled` global. |
| `fallbackProviders` | string[] | `[]` | Daftar terurut nama provider fallback jika provider utama gagal. |
| `trustLevel` | string | `"auto"` | Level kepercayaan: `"observe"` (read-only), `"suggest"` (mengusulkan tapi tidak menerapkan), `"auto"` (otonomi penuh). |
| `tools` | AgentToolPolicy | `{}` | Kebijakan akses tool. Lihat [Tool Policy](#tool-policy). |
| `toolProfile` | string | `"standard"` | Profil tool bernama: `"minimal"`, `"standard"`, `"full"`. |
| `workspace` | WorkspaceConfig | `{}` | Pengaturan isolasi workspace. |

---

## Smart Dispatch

Smart Dispatch secara otomatis merutekan task masuk ke agent yang paling sesuai berdasarkan aturan, kata kunci, dan klasifikasi LLM.

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan routing smart dispatch. |
| `coordinator` | string | agent pertama | Agent yang digunakan untuk klasifikasi task berbasis LLM. |
| `defaultAgent` | string | agent pertama | Agent fallback ketika tidak ada aturan yang cocok. |
| `classifyBudget` | float64 | `0.1` | Anggaran biaya (USD) untuk panggilan LLM klasifikasi. |
| `classifyTimeout` | string | `"30s"` | Timeout untuk panggilan klasifikasi. |
| `review` | bool | `false` | Jalankan review agent pada output setelah task selesai. |
| `reviewLoop` | bool | `false` | Aktifkan loop retry Dev↔QA: review → feedback → retry (hingga `maxRetries`). |
| `maxRetries` | int | `3` | Maksimum percobaan ulang QA dalam loop review. |
| `reviewAgent` | string | coordinator | Agent yang bertanggung jawab untuk mereview output. Atur ke agent QA yang ketat untuk review adversarial. |
| `reviewBudget` | float64 | `0.2` | Anggaran biaya (USD) untuk panggilan LLM review. |
| `fallback` | string | `"smart"` | Strategi fallback: `"smart"` (routing LLM) atau `"coordinator"` (selalu agent default). |
| `rules` | RoutingRule[] | `[]` | Aturan routing kata kunci/regex yang dievaluasi sebelum klasifikasi LLM. |
| `bindings` | RoutingBinding[] | `[]` | Binding channel/user/guild → agent (prioritas tertinggi, dievaluasi pertama). |

### `rules` — `RoutingRule`

| Field | Tipe | Deskripsi |
|---|---|---|
| `agent` | string | Nama agent target. |
| `keywords` | string[] | Kata kunci tidak peka huruf besar/kecil. Cocok mana saja merutekan ke agent ini. |
| `patterns` | string[] | Pola regex Go. Cocok mana saja merutekan ke agent ini. |

### `bindings` — `RoutingBinding`

| Field | Tipe | Deskripsi |
|---|---|---|
| `channel` | string | Platform: `"telegram"`, `"discord"`, `"slack"`, dll. |
| `userId` | string | ID pengguna di platform tersebut. |
| `channelId` | string | ID channel atau chat. |
| `guildId` | string | ID guild/server (Discord saja). |
| `agent` | string | Nama agent target. |

---

## Session

Mengontrol bagaimana konteks percakapan dipertahankan dan dikompaksi dalam interaksi multi-giliran.

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `contextMessages` | int | `20` | Jumlah maksimum pesan terbaru yang disuntikkan sebagai konteks ke dalam task baru. |
| `compactAfter` | int | `30` | Kompaksi ketika jumlah pesan melebihi ini. Deprecated: gunakan `compaction.maxMessages`. |
| `compactKeep` | int | `10` | Pertahankan N pesan terakhir setelah kompaksi. Deprecated: gunakan `compaction.compactTo`. |
| `compactTokens` | int | `200000` | Kompaksi ketika total token input melebihi ambang batas ini. |

### `session.compaction` — `CompactionConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan kompaksi session otomatis. |
| `maxMessages` | int | `50` | Picu kompaksi ketika session melebihi jumlah pesan ini. |
| `compactTo` | int | `10` | Jumlah pesan terbaru yang dipertahankan setelah kompaksi. |
| `model` | string | `"haiku"` | Model LLM yang digunakan untuk menghasilkan ringkasan kompaksi. |
| `maxCost` | float64 | `0.02` | Biaya maksimum per panggilan kompaksi (USD). |
| `provider` | string | `defaultProvider` | Provider yang digunakan untuk panggilan ringkasan kompaksi. |

---

## Task Board

Task board bawaan melacak item pekerjaan dan dapat secara otomatis mendispatch ke agent.

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan task board. |
| `maxRetries` | int | `3` | Maksimum percobaan ulang per task sebelum ditandai gagal. |
| `requireReview` | bool | `false` | Gerbang kualitas: task harus lulus review sebelum ditandai selesai. |
| `defaultWorkflow` | string | `""` | Nama workflow yang dijalankan untuk semua task yang di-dispatch otomatis. Kosong = tanpa workflow. |
| `gitCommit` | bool | `false` | Auto-commit ketika task ditandai selesai. |
| `gitPush` | bool | `false` | Auto-push setelah commit (memerlukan `gitCommit: true`). |
| `gitPR` | bool | `false` | Auto-buat GitHub PR setelah push (memerlukan `gitPush: true`). |
| `gitWorktree` | bool | `false` | Gunakan git worktrees untuk isolasi task (menghilangkan konflik file antara task yang berjalan bersamaan). |
| `idleAnalyze` | bool | `false` | Auto-jalankan analisis ketika board idle. |
| `problemScan` | bool | `false` | Pindai output task untuk masalah laten setelah selesai. |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan polling otomatis dan dispatch task yang mengantri. |
| `interval` | string | `"5m"` | Seberapa sering memindai task yang siap. |
| `maxConcurrentTasks` | int | `3` | Maksimum task yang di-dispatch per siklus pemindaian. |
| `defaultModel` | string | `""` | Override model untuk task yang di-dispatch otomatis. |
| `maxBudget` | float64 | `0` | Biaya maksimum per task (USD). `0` = tanpa batas. |
| `defaultAgent` | string | `""` | Agent fallback untuk task yang tidak ditugaskan. |
| `backlogAgent` | string | `""` | Agent untuk triage backlog. |
| `reviewAgent` | string | `""` | Agent untuk mereview task yang selesai. |
| `escalateAssignee` | string | `""` | Tugaskan task yang ditolak review ke pengguna ini. |
| `stuckThreshold` | string | `"2h"` | Task dalam status "doing" lebih lama dari ini akan direset ke "todo". |
| `backlogTriageInterval` | string | `"1h"` | Seberapa sering menjalankan triage backlog. |
| `reviewLoop` | bool | `false` | Aktifkan loop Dev↔QA otomatis untuk task yang di-dispatch. |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | Template penamaan branch. Variabel: `{type}`, `{agent}`, `{description}`. |
| `types` | string[] | `["feat","fix","refactor","chore"]` | Prefiks tipe branch yang diizinkan. |
| `defaultType` | string | `"feat"` | Tipe fallback ketika tidak ada yang ditentukan. |
| `autoMerge` | bool | `false` | Gabungkan otomatis ke main ketika task selesai (hanya ketika `gitWorktree: true`). |

---

## Slot Pressure

Mengontrol perilaku sistem ketika mendekati batas slot `maxConcurrent`. Session interaktif (yang dimulai manusia) mendapatkan slot yang dicadangkan; task latar belakang menunggu.

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan manajemen slot pressure. |
| `reservedSlots` | int | `2` | Slot yang dicadangkan untuk session interaktif. Task latar belakang tidak dapat menggunakan ini. |
| `warnThreshold` | int | `3` | Peringatkan pengguna ketika tersedia lebih sedikit slot dari jumlah ini. |
| `nonInteractiveTimeout` | string | `"5m"` | Berapa lama task latar belakang menunggu slot sebelum timeout. |
| `pollInterval` | string | `"2s"` | Seberapa sering task latar belakang memeriksa slot yang tersedia. |
| `monitorEnabled` | bool | `false` | Aktifkan peringatan slot pressure proaktif melalui channel notifikasi. |
| `monitorInterval` | string | `"30s"` | Seberapa sering memeriksa dan mengeluarkan peringatan tekanan. |

---

## Workflow

Workflow didefinisikan sebagai file YAML dalam sebuah direktori. `workflowDir` menunjuk ke direktori tersebut; variabel menyediakan nilai template default.

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | Direktori tempat file YAML workflow disimpan. |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | Pemicu workflow otomatis pada event sistem. |

---

## Integrasi

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan bot Telegram. |
| `botToken` | string | `""` | Token bot Telegram dari @BotFather. Mendukung `$ENV_VAR`. |
| `chatID` | int64 | `0` | ID chat atau grup Telegram untuk mengirim notifikasi. |
| `pollTimeout` | int | `30` | Timeout long-poll dalam detik untuk menerima pesan. |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan bot Discord. |
| `botToken` | string | `""` | Token bot Discord. Mendukung `$ENV_VAR`. |
| `guildID` | string | `""` | Batasi ke server Discord (guild) tertentu. |
| `channelIDs` | string[] | `[]` | ID channel tempat bot membalas semua pesan (tidak perlu mention `@`). |
| `mentionChannelIDs` | string[] | `[]` | ID channel tempat bot hanya membalas ketika di-mention dengan `@`. |
| `notifyChannelID` | string | `""` | Channel untuk notifikasi penyelesaian task (membuat thread per task). |
| `showProgress` | bool | `true` | Tampilkan pesan streaming "Working..." secara langsung di Discord. |
| `webhooks` | map[string]string | `{}` | URL webhook bernama untuk notifikasi hanya keluar. |
| `routes` | map[string]DiscordRouteConfig | `{}` | Peta ID channel ke nama agent untuk routing per channel. |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan bot Slack. |
| `botToken` | string | `""` | Token OAuth bot Slack (`xoxb-...`). Mendukung `$ENV_VAR`. |
| `signingSecret` | string | `""` | Signing secret Slack untuk verifikasi permintaan. Mendukung `$ENV_VAR`. |
| `appToken` | string | `""` | Token level app Slack untuk Socket Mode (`xapp-...`). Opsional. Mendukung `$ENV_VAR`. |
| `defaultChannel` | string | `""` | ID channel default untuk notifikasi keluar. |

### Webhook Keluar

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `url` | string | wajib | URL endpoint webhook. |
| `headers` | map[string]string | `{}` | Header HTTP yang disertakan. Nilai mendukung `$ENV_VAR`. |
| `events` | string[] | semua | Event yang dikirim: `"success"`, `"error"`, `"timeout"`, `"all"`. Kosong = semua. |

### Webhook Masuk

Webhook masuk memungkinkan layanan eksternal memicu task Tetora melalui HTTP POST.

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### Channel Notifikasi

Channel notifikasi bernama untuk merutekan event task ke endpoint Slack/Discord yang berbeda.

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `name` | string | `""` | Referensi bernama yang digunakan dalam field `channel` job (mis., `"discord:alerts"`). |
| `type` | string | wajib | `"slack"` atau `"discord"`. |
| `webhookUrl` | string | wajib | URL webhook. Mendukung `$ENV_VAR`. |
| `events` | string[] | semua | Filter berdasarkan tipe event: `"all"`, `"error"`, `"success"`. |
| `minPriority` | string | semua | Prioritas minimum: `"critical"`, `"high"`, `"normal"`, `"low"`. |

---

## Store (Marketplace Template)

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan template store. |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | URL registry remote untuk menelusuri dan menginstal template. |
| `authToken` | string | `""` | Token autentikasi untuk registry. Mendukung `$ENV_VAR`. |

---

## Biaya dan Peringatan

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | Batas pengeluaran harian dalam USD. `0` = tanpa batas. |
| `weeklyLimit` | float64 | `0` | Batas pengeluaran mingguan dalam USD. `0` = tanpa batas. |
| `dailyTokenLimit` | int | `0` | Batas total token harian (input + output). `0` = tanpa batas. |
| `action` | string | `"warn"` | Tindakan ketika batas terlampaui: `"warn"` (log dan notifikasi) atau `"pause"` (blokir task baru). |

### `estimate` — `EstimateConfig`

Estimasi biaya sebelum eksekusi sebelum menjalankan task.

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | Minta konfirmasi ketika estimasi biaya melebihi nilai USD ini. |
| `defaultOutputTokens` | int | `500` | Estimasi token output fallback ketika penggunaan aktual tidak diketahui. |

### `budgets` — `BudgetConfig`

Anggaran biaya di level agent dan level tim.

---

## Logging

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `level` | string | `"info"` | Level log: `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `format` | string | `"text"` | Format log: `"text"` (mudah dibaca manusia) atau `"json"` (terstruktur). |
| `file` | string | `runtime/logs/tetora.log` | Path file log. Relatif terhadap direktori runtime. |
| `maxSizeMB` | int | `50` | Ukuran file log maksimum dalam MB sebelum rotasi. |
| `maxFiles` | int | `5` | Jumlah file log yang dirotasi untuk dipertahankan. |

---

## Keamanan

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan HTTP Basic Auth pada dashboard. |
| `username` | string | `"admin"` | Username basic auth. |
| `password` | string | `""` | Password basic auth. Mendukung `$ENV_VAR`. |
| `token` | string | `""` | Alternatif: token statis yang diteruskan sebagai cookie. |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| Field | Tipe | Deskripsi |
|---|---|---|
| `certFile` | string | Path ke file PEM sertifikat TLS. Mengaktifkan HTTPS ketika diset (bersama dengan `keyFile`). |
| `keyFile` | string | Path ke file PEM private key TLS. |

### `rateLimit` — `RateLimitConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan pembatasan laju permintaan per IP. |
| `maxPerMin` | int | `60` | Maksimum permintaan API per menit per IP. |

### `securityAlert` — `SecurityAlertConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan peringatan keamanan pada kegagalan autentikasi yang berulang. |
| `failThreshold` | int | `10` | Jumlah kegagalan dalam rentang waktu sebelum memberikan peringatan. |
| `failWindowMin` | int | `5` | Jendela waktu geser dalam menit. |

### `approvalGates` — `ApprovalGateConfig`

Memerlukan persetujuan manusia sebelum tool tertentu dijalankan.

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan approval gates. |
| `timeout` | int | `120` | Detik menunggu persetujuan sebelum dibatalkan. |
| `tools` | string[] | `[]` | Nama tool yang memerlukan persetujuan sebelum eksekusi. |
| `autoApproveTools` | string[] | `[]` | Tool yang sudah disetujui otomatis saat startup (tidak pernah meminta konfirmasi). |

---

## Keandalan

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `true` | Aktifkan circuit breaker untuk failover provider. |
| `failThreshold` | int | `5` | Kegagalan berturut-turut sebelum sirkuit terbuka. |
| `successThreshold` | int | `2` | Keberhasilan dalam status half-open sebelum menutup. |
| `openTimeout` | string | `"30s"` | Durasi dalam status terbuka sebelum mencoba lagi (half-open). |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

Daftar terurut global provider fallback jika provider default gagal.

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan pemantauan heartbeat agent. |
| `interval` | string | `"30s"` | Seberapa sering memeriksa task yang berjalan untuk stall. |
| `stallThreshold` | string | `"5m"` | Tidak ada output selama durasi ini = task terhenti. |
| `timeoutWarnRatio` | float64 | `0.8` | Peringatkan ketika waktu yang berlalu melebihi rasio ini dari timeout task. |
| `autoCancel` | bool | `false` | Batalkan otomatis task yang terhenti lebih lama dari `2x stallThreshold`. |
| `notifyOnStall` | bool | `true` | Kirim notifikasi ketika task terdeteksi terhenti. |

### `retention` — `RetentionConfig`

Mengontrol pembersihan otomatis data lama.

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `history` | int | `90` | Hari untuk menyimpan riwayat job run. |
| `sessions` | int | `30` | Hari untuk menyimpan data session. |
| `auditLog` | int | `365` | Hari untuk menyimpan entri audit log. |
| `logs` | int | `14` | Hari untuk menyimpan file log. |
| `workflows` | int | `90` | Hari untuk menyimpan catatan workflow run. |
| `reflections` | int | `60` | Hari untuk menyimpan catatan reflection. |
| `sla` | int | `90` | Hari untuk menyimpan catatan pemeriksaan SLA. |
| `trustEvents` | int | `90` | Hari untuk menyimpan catatan trust event. |
| `handoffs` | int | `60` | Hari untuk menyimpan catatan handoff/pesan agent. |
| `queue` | int | `7` | Hari untuk menyimpan item antrian offline. |
| `versions` | int | `180` | Hari untuk menyimpan snapshot versi konfigurasi. |
| `outputs` | int | `30` | Hari untuk menyimpan file output agent. |
| `uploads` | int | `7` | Hari untuk menyimpan file yang diunggah. |
| `memory` | int | `30` | Hari sebelum entri memory yang tidak aktif diarsipkan. |
| `claudeSessions` | int | `3` | Hari untuk menyimpan artefak session Claude CLI. |
| `piiPatterns` | string[] | `[]` | Pola regex untuk redaksi PII dalam konten yang disimpan. |

---

## Quiet Hours dan Digest

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan quiet hours. Notifikasi ditahan selama rentang ini. |
| `start` | string | `""` | Awal periode quiet (waktu lokal, format `"HH:MM"`). |
| `end` | string | `""` | Akhir periode quiet (waktu lokal). |
| `tz` | string | lokal | Zona waktu, mis. `"Asia/Taipei"`, `"UTC"`. |
| `digest` | bool | `false` | Kirim digest notifikasi yang ditahan ketika quiet hours berakhir. |

### `digest` — `DigestConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan digest harian terjadwal. |
| `time` | string | `"08:00"` | Waktu untuk mengirim digest (`"HH:MM"`). |
| `tz` | string | lokal | Zona waktu. |

---

## Tool

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `maxIterations` | int | `10` | Maksimum iterasi pemanggilan tool per task. |
| `timeout` | int | `120` | Timeout mesin tool global dalam detik. |
| `toolOutputLimit` | int | `10240` | Maksimum karakter per output tool (dipotong melampaui ini). |
| `toolTimeout` | int | `30` | Timeout eksekusi per tool dalam detik. |
| `defaultProfile` | string | `"standard"` | Nama profil tool default. |
| `builtin` | map[string]bool | `{}` | Aktifkan/nonaktifkan tool bawaan individual berdasarkan nama. |
| `profiles` | map[string]ToolProfile | `{}` | Profil tool kustom. |
| `trustOverride` | map[string]string | `{}` | Override level kepercayaan per nama tool. |

### `tools.webSearch` — `WebSearchConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `provider` | string | `""` | Provider pencarian: `"brave"`, `"tavily"`, `"searxng"`. |
| `apiKey` | string | `""` | API key untuk provider. Mendukung `$ENV_VAR`. |
| `baseURL` | string | `""` | Endpoint kustom (untuk searxng yang di-host sendiri). |
| `maxResults` | int | `5` | Maksimum hasil pencarian yang dikembalikan. |

### `tools.vision` — `VisionConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `provider` | string | `""` | Provider vision: `"anthropic"`, `"openai"`, `"google"`. |
| `apiKey` | string | `""` | API key. Mendukung `$ENV_VAR`. |
| `model` | string | `""` | Nama model untuk provider vision. |
| `maxImageSize` | int | `5242880` | Ukuran gambar maksimum dalam byte (default 5 MB). |
| `baseURL` | string | `""` | Endpoint API kustom. |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

Konfigurasi server MCP bernama. Setiap key adalah nama konfigurasi MCP; nilainya adalah konfigurasi JSON MCP lengkap. Tetora menulis ini ke file temp dan meneruskannya ke binary claude melalui `--mcp-config`.

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

Definisi server MCP yang disederhanakan dan dikelola langsung oleh Tetora.

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `command` | string | wajib | Perintah yang dapat dieksekusi. |
| `args` | string[] | `[]` | Argumen perintah. |
| `env` | map[string]string | `{}` | Environment variable untuk proses. Nilai mendukung `$ENV_VAR`. |
| `enabled` | bool | `true` | Apakah server MCP ini aktif. |

---

## Anggaran Prompt

Mengontrol anggaran karakter maksimum untuk setiap bagian dari system prompt. Sesuaikan ketika prompt dipotong secara tidak terduga.

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `soulMax` | int | `8000` | Maksimum karakter untuk prompt kepribadian/soul agent. |
| `rulesMax` | int | `4000` | Maksimum karakter untuk rules workspace. |
| `knowledgeMax` | int | `8000` | Maksimum karakter untuk konten knowledge base. |
| `skillsMax` | int | `4000` | Maksimum karakter untuk skills yang disuntikkan. |
| `maxSkillsPerTask` | int | `3` | Jumlah maksimum skill yang disuntikkan per task. |
| `contextMax` | int | `16000` | Maksimum karakter untuk konteks session. |
| `totalMax` | int | `40000` | Batas keras pada ukuran total system prompt (semua bagian digabungkan). |

---

## Komunikasi Agent

Mengontrol dispatch sub-agent bersarang (tool agent_dispatch).

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| Field | Tipe | Default | Deskripsi |
|---|---|---|---|
| `enabled` | bool | `false` | Aktifkan tool `agent_dispatch` untuk panggilan sub-agent bersarang. |
| `maxConcurrent` | int | `3` | Maksimum panggilan `agent_dispatch` bersamaan secara global. |
| `defaultTimeout` | int | `900` | Timeout sub-agent default dalam detik. |
| `maxDepth` | int | `3` | Kedalaman bersarang maksimum untuk sub-agent. |
| `maxChildrenPerTask` | int | `5` | Maksimum agent anak bersamaan per task induk. |

---

## Contoh

### Konfigurasi Minimal

Konfigurasi minimal untuk memulai dengan provider Claude CLI:

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### Konfigurasi Multi-Agent dengan Smart Dispatch

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### Konfigurasi Lengkap (Semua Bagian Utama)

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
