---
title: "Referencia de Configuración"
lang: "es"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# Referencia de Configuración

## Descripción General

Tetora se configura mediante un único archivo JSON ubicado en `~/.tetora/config.json`.

**Comportamientos clave:**

- **Sustitución de `$ENV_VAR`** — cualquier valor de cadena que empiece con `$` se reemplaza por la variable de entorno correspondiente al iniciar. Úsalo para secretos (claves API, tokens) en lugar de escribirlos directamente en el archivo.
- **Recarga en caliente** — enviar `SIGHUP` al daemon recarga la configuración. Una configuración incorrecta es rechazada y se mantiene la configuración activa; el daemon no se detiene.
- **Rutas relativas** — los campos `jobsFile`, `historyDB`, `defaultWorkdir` y los campos de directorio se resuelven en relación al directorio del archivo de configuración (`~/.tetora/`).
- **Compatibilidad hacia atrás** — la clave antigua `"roles"` es un alias de `"agents"`. La antigua clave `"defaultRole"` dentro de `smartDispatch` es un alias de `"defaultAgent"`.

---

## Campos de Nivel Superior

### Configuración Principal

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | Dirección de escucha HTTP para la API y el dashboard. Formato: `host:port`. |
| `apiToken` | string | `""` | Token Bearer requerido para todas las solicitudes API. Vacío significa sin autenticación (no recomendado para producción). Admite `$ENV_VAR`. |
| `maxConcurrent` | int | `8` | Número máximo de tareas de agente concurrentes. Valores superiores a 20 generan una advertencia al iniciar. |
| `defaultModel` | string | `"sonnet"` | Nombre del modelo Claude predeterminado. Se pasa al proveedor salvo que se sobreescriba por agente. |
| `defaultTimeout` | string | `"1h"` | Tiempo de espera predeterminado por tarea. Formato de duración de Go: `"15m"`, `"1h"`, `"30s"`. |
| `defaultBudget` | float64 | `0` | Presupuesto de costo predeterminado por tarea en USD. `0` significa sin límite. |
| `defaultPermissionMode` | string | `"acceptEdits"` | Modo de permisos Claude predeterminado. Valores comunes: `"acceptEdits"`, `"default"`. |
| `defaultAgent` | string | `""` | Agente de reserva a nivel de sistema cuando ninguna regla de enrutamiento coincide. |
| `defaultWorkdir` | string | `""` | Directorio de trabajo predeterminado para tareas de agente. Debe existir en disco. |
| `claudePath` | string | `"claude"` | Ruta al binario `claude` CLI. Por defecto busca `claude` en `$PATH`. |
| `defaultProvider` | string | `"claude"` | Nombre del proveedor a usar cuando no hay una sobreescritura a nivel de agente. |
| `log` | bool | `false` | Flag heredado para habilitar el registro en archivo. Se prefiere `logging.level`. |
| `maxPromptLen` | int | `102400` | Longitud máxima del prompt en bytes (100 KB). Las solicitudes que exceden esto son rechazadas. |
| `configVersion` | int | `0` | Versión del esquema de configuración. Usado para auto-migración. No configurar manualmente. |
| `encryptionKey` | string | `""` | Clave AES para cifrado a nivel de campo de datos sensibles. Admite `$ENV_VAR`. |
| `streamToChannels` | bool | `false` | Transmite el estado de tareas en vivo a los canales de mensajería conectados (Discord, Telegram, etc.). |
| `cronNotify` | bool\|null | `null` (true) | `false` suprime todas las notificaciones de finalización de trabajos cron. `null` o `true` las habilita. |
| `cronReplayHours` | int | `2` | Cuántas horas retrotraer en busca de trabajos cron perdidos al iniciar el daemon. |
| `diskBudgetGB` | float64 | `1.0` | Espacio en disco libre mínimo en GB. Los trabajos cron son rechazados por debajo de este nivel. |
| `diskWarnMB` | int | `500` | Umbral de advertencia de disco libre en MB. Registra un WARN pero los trabajos continúan. |
| `diskBlockMB` | int | `200` | Umbral de bloqueo de disco libre en MB. Los trabajos se omiten con estado `skipped_disk_full`. |

### Sobreescritura de Directorios

Por defecto todos los directorios se encuentran bajo `~/.tetora/`. Sobreescribir solo si se necesita una estructura no estándar.

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | Directorio para archivos de conocimiento del workspace. |
| `agentsDir` | string | `~/.tetora/agents/` | Directorio que contiene los archivos SOUL.md de cada agente. |
| `workspaceDir` | string | `~/.tetora/workspace/` | Directorio para reglas, memoria, skills, borradores, etc. |
| `runtimeDir` | string | `~/.tetora/runtime/` | Directorio para sesiones, salidas, logs, caché. |
| `vaultDir` | string | `~/.tetora/vault/` | Directorio para el vault de secretos cifrados. |
| `historyDB` | string | `history.db` | Ruta de la base de datos SQLite para el historial de trabajos. Relativa al directorio de configuración. |
| `jobsFile` | string | `jobs.json` | Ruta al archivo de definición de trabajos cron. Relativa al directorio de configuración. |

### Directorios Permitidos Globalmente

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| Campo | Tipo | Descripción |
|---|---|---|
| `allowedDirs` | string[] | Directorios que el agente puede leer y escribir. Aplicado globalmente; se puede restringir por agente. |
| `defaultAddDirs` | string[] | Directorios inyectados como `--add-dir` para cada tarea (contexto de solo lectura). |
| `allowedIPs` | string[] | Direcciones IP o rangos CIDR autorizados para llamar a la API. Vacío = permitir todos. Ejemplo: `["192.168.1.0/24", "10.0.0.1"]`. |

---

## Proveedores

Los proveedores definen cómo Tetora ejecuta las tareas de los agentes. Se pueden configurar múltiples proveedores y seleccionarse por agente.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `type` | string | requerido | Tipo de proveedor. Uno de: `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"`. |
| `path` | string | `""` | Ruta del binario. Usado por los tipos `claude-cli` y `claude-code`. Recurre a `claudePath` si está vacío. |
| `baseUrl` | string | `""` | URL base de la API. Requerida para `openai-compatible`. |
| `apiKey` | string | `""` | Clave API. Admite `$ENV_VAR`. Requerida para `claude-api`; opcional para `openai-compatible`. |
| `model` | string | `""` | Modelo predeterminado para este proveedor. Sobreescribe `defaultModel` para tareas que usen este proveedor. |
| `maxTokens` | int | `8192` | Máximo de tokens de salida (usado por `claude-api`). |
| `firstTokenTimeout` | string | `"60s"` | Tiempo máximo de espera para el primer token de respuesta antes de agotar el tiempo (stream SSE). |

**Tipos de proveedor:**
- `claude-cli` — ejecuta el binario `claude` como subproceso (predeterminado, mayor compatibilidad)
- `claude-api` — llama directamente a la API de Anthropic vía HTTP (requiere `ANTHROPIC_API_KEY`)
- `openai-compatible` — cualquier API REST compatible con OpenAI (OpenAI, Ollama, Groq, etc.)
- `claude-code` — usa el modo CLI de Claude Code

---

## Agentes

Los agentes definen personas con nombre, con su propio modelo, archivo soul y acceso a herramientas.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `soulFile` | string | requerido | Ruta al archivo de personalidad SOUL.md del agente, relativa a `agentsDir`. |
| `model` | string | `defaultModel` | Modelo a usar para este agente. |
| `description` | string | `""` | Descripción legible por humanos. También es usada por el clasificador LLM para el enrutamiento. |
| `keywords` | string[] | `[]` | Palabras clave que activan el enrutamiento a este agente en el smart dispatch. |
| `provider` | string | `defaultProvider` | Nombre del proveedor (clave en el mapa `providers`). |
| `permissionMode` | string | `defaultPermissionMode` | Modo de permisos Claude para este agente. |
| `allowedDirs` | string[] | `allowedDirs` | Rutas del sistema de archivos a las que este agente puede acceder. Sobreescribe la configuración global. |
| `docker` | bool\|null | `null` | Sobreescritura del sandbox Docker por agente. `null` = heredar el `docker.enabled` global. |
| `fallbackProviders` | string[] | `[]` | Lista ordenada de proveedores de reserva si el primario falla. |
| `trustLevel` | string | `"auto"` | Nivel de confianza: `"observe"` (solo lectura), `"suggest"` (proponer pero no aplicar), `"auto"` (autonomía total). |
| `tools` | AgentToolPolicy | `{}` | Política de acceso a herramientas. Ver [Tool Policy](#tool-policy). |
| `toolProfile` | string | `"standard"` | Perfil de herramientas con nombre: `"minimal"`, `"standard"`, `"full"`. |
| `workspace` | WorkspaceConfig | `{}` | Configuración de aislamiento del workspace. |

---

## Smart Dispatch

Smart Dispatch enruta automáticamente las tareas entrantes al agente más apropiado según reglas, palabras clave y clasificación LLM.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el enrutamiento smart dispatch. |
| `coordinator` | string | primer agente | Agente usado para la clasificación de tareas basada en LLM. |
| `defaultAgent` | string | primer agente | Agente de reserva cuando ninguna regla coincide. |
| `classifyBudget` | float64 | `0.1` | Presupuesto de costo (USD) para la llamada LLM de clasificación. |
| `classifyTimeout` | string | `"30s"` | Tiempo de espera para la llamada de clasificación. |
| `review` | bool | `false` | Ejecutar un agente de revisión sobre la salida tras la finalización de la tarea. |
| `reviewLoop` | bool | `false` | Habilitar el ciclo de reintentos Dev↔QA: revisión → retroalimentación → reintento (hasta `maxRetries`). |
| `maxRetries` | int | `3` | Máximo de intentos de QA en el ciclo de revisión. |
| `reviewAgent` | string | coordinator | Agente responsable de revisar la salida. Configurar con un agente de QA estricto para revisión adversarial. |
| `reviewBudget` | float64 | `0.2` | Presupuesto de costo (USD) para la llamada LLM de revisión. |
| `fallback` | string | `"smart"` | Estrategia de reserva: `"smart"` (enrutamiento LLM) o `"coordinator"` (siempre el agente predeterminado). |
| `rules` | RoutingRule[] | `[]` | Reglas de enrutamiento por palabras clave/regex evaluadas antes de la clasificación LLM. |
| `bindings` | RoutingBinding[] | `[]` | Vinculaciones canal/usuario/guild → agente (mayor prioridad, evaluadas primero). |

### `rules` — `RoutingRule`

| Campo | Tipo | Descripción |
|---|---|---|
| `agent` | string | Nombre del agente destino. |
| `keywords` | string[] | Palabras clave sin distinción de mayúsculas. Cualquier coincidencia enruta a este agente. |
| `patterns` | string[] | Patrones regex de Go. Cualquier coincidencia enruta a este agente. |

### `bindings` — `RoutingBinding`

| Campo | Tipo | Descripción |
|---|---|---|
| `channel` | string | Plataforma: `"telegram"`, `"discord"`, `"slack"`, etc. |
| `userId` | string | ID de usuario en esa plataforma. |
| `channelId` | string | ID del canal o chat. |
| `guildId` | string | ID del guild/servidor (solo Discord). |
| `agent` | string | Nombre del agente destino. |

---

## Session

Controla cómo se mantiene y compacta el contexto de conversación a través de interacciones de múltiples turnos.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `contextMessages` | int | `20` | Número máximo de mensajes recientes a inyectar como contexto en una nueva tarea. |
| `compactAfter` | int | `30` | Compactar cuando el conteo de mensajes supere este valor. Obsoleto: usar `compaction.maxMessages`. |
| `compactKeep` | int | `10` | Conservar los últimos N mensajes tras la compactación. Obsoleto: usar `compaction.compactTo`. |
| `compactTokens` | int | `200000` | Compactar cuando el total de tokens de entrada supere este umbral. |

### `session.compaction` — `CompactionConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar la compactación automática de sesiones. |
| `maxMessages` | int | `50` | Activar compactación cuando la sesión supere este número de mensajes. |
| `compactTo` | int | `10` | Número de mensajes recientes a conservar tras la compactación. |
| `model` | string | `"haiku"` | Modelo LLM a usar para generar el resumen de compactación. |
| `maxCost` | float64 | `0.02` | Costo máximo por llamada de compactación (USD). |
| `provider` | string | `defaultProvider` | Proveedor a usar para la llamada de resumen de compactación. |

---

## Task Board

El task board integrado hace seguimiento de elementos de trabajo y puede enviarlos automáticamente a los agentes.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el task board. |
| `maxRetries` | int | `3` | Máximo de intentos por tarea antes de marcarla como fallida. |
| `requireReview` | bool | `false` | Control de calidad: la tarea debe pasar revisión antes de ser marcada como completada. |
| `defaultWorkflow` | string | `""` | Nombre del workflow a ejecutar para todas las tareas enviadas automáticamente. Vacío = sin workflow. |
| `gitCommit` | bool | `false` | Hacer commit automático cuando una tarea se marca como completada. |
| `gitPush` | bool | `false` | Push automático tras el commit (requiere `gitCommit: true`). |
| `gitPR` | bool | `false` | Crear automáticamente un PR de GitHub tras el push (requiere `gitPush: true`). |
| `gitWorktree` | bool | `false` | Usar git worktrees para aislar las tareas (elimina conflictos de archivos entre tareas concurrentes). |
| `idleAnalyze` | bool | `false` | Ejecutar análisis automáticamente cuando el board esté inactivo. |
| `problemScan` | bool | `false` | Analizar la salida de tareas en busca de problemas latentes tras la finalización. |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el sondeo y envío automático de tareas en cola. |
| `interval` | string | `"5m"` | Con qué frecuencia buscar tareas listas. |
| `maxConcurrentTasks` | int | `3` | Máximo de tareas enviadas por ciclo de análisis. |
| `defaultModel` | string | `""` | Sobreescribir el modelo para tareas enviadas automáticamente. |
| `maxBudget` | float64 | `0` | Costo máximo por tarea (USD). `0` = sin límite. |
| `defaultAgent` | string | `""` | Agente de reserva para tareas sin asignar. |
| `backlogAgent` | string | `""` | Agente para la clasificación del backlog. |
| `reviewAgent` | string | `""` | Agente para revisar tareas completadas. |
| `escalateAssignee` | string | `""` | Asignar tareas rechazadas en revisión a este usuario. |
| `stuckThreshold` | string | `"2h"` | Las tareas en "doing" por más tiempo que esto se resetean a "todo". |
| `backlogTriageInterval` | string | `"1h"` | Con qué frecuencia ejecutar la clasificación del backlog. |
| `reviewLoop` | bool | `false` | Habilitar el ciclo automatizado Dev↔QA para tareas enviadas. |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | Plantilla de nombre de rama. Variables: `{type}`, `{agent}`, `{description}`. |
| `types` | string[] | `["feat","fix","refactor","chore"]` | Prefijos de tipo de rama permitidos. |
| `defaultType` | string | `"feat"` | Tipo de reserva cuando no se especifica ninguno. |
| `autoMerge` | bool | `false` | Fusionar automáticamente a main cuando la tarea está lista (solo cuando `gitWorktree: true`). |

---

## Presión de Slots

Controla cómo se comporta el sistema al acercarse al límite de slots `maxConcurrent`. Las sesiones interactivas (iniciadas por humanos) obtienen slots reservados; las tareas en segundo plano esperan.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar la gestión de presión de slots. |
| `reservedSlots` | int | `2` | Slots reservados para sesiones interactivas. Las tareas en segundo plano no pueden usarlos. |
| `warnThreshold` | int | `3` | Advertir al usuario cuando haya menos de esta cantidad de slots disponibles. |
| `nonInteractiveTimeout` | string | `"5m"` | Cuánto tiempo espera una tarea en segundo plano por un slot antes de agotar el tiempo. |
| `pollInterval` | string | `"2s"` | Con qué frecuencia las tareas en segundo plano verifican la disponibilidad de un slot. |
| `monitorEnabled` | bool | `false` | Habilitar alertas proactivas de presión de slots mediante canales de notificación. |
| `monitorInterval` | string | `"30s"` | Con qué frecuencia verificar y emitir alertas de presión. |

---

## Workflows

Los workflows se definen como archivos YAML en un directorio. `workflowDir` apunta a ese directorio; las variables proporcionan valores de plantilla predeterminados.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | Directorio donde se almacenan los archivos YAML de workflows. |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | Disparadores automáticos de workflow ante eventos del sistema. |

---

## Integraciones

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el bot de Telegram. |
| `botToken` | string | `""` | Token del bot de Telegram obtenido de @BotFather. Admite `$ENV_VAR`. |
| `chatID` | int64 | `0` | ID del chat o grupo de Telegram al que enviar notificaciones. |
| `pollTimeout` | int | `30` | Tiempo de espera del long-polling en segundos para recibir mensajes. |

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el bot de Discord. |
| `botToken` | string | `""` | Token del bot de Discord. Admite `$ENV_VAR`. |
| `guildID` | string | `""` | Restringir a un servidor de Discord específico (guild). |
| `channelIDs` | string[] | `[]` | IDs de canal donde el bot responde a todos los mensajes (sin necesidad de mención `@`). |
| `mentionChannelIDs` | string[] | `[]` | IDs de canal donde el bot solo responde cuando se le menciona con `@`. |
| `notifyChannelID` | string | `""` | Canal para notificaciones de finalización de tareas (crea un hilo por tarea). |
| `showProgress` | bool | `true` | Mostrar mensajes de transmisión en vivo "Working..." en Discord. |
| `webhooks` | map[string]string | `{}` | URLs de webhook con nombre para notificaciones solo de salida. |
| `routes` | map[string]DiscordRouteConfig | `{}` | Mapa de ID de canal a nombre de agente para enrutamiento por canal. |

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el bot de Slack. |
| `botToken` | string | `""` | Token OAuth del bot de Slack (`xoxb-...`). Admite `$ENV_VAR`. |
| `signingSecret` | string | `""` | Secreto de firma de Slack para verificación de solicitudes. Admite `$ENV_VAR`. |
| `appToken` | string | `""` | Token a nivel de aplicación de Slack para Socket Mode (`xapp-...`). Opcional. Admite `$ENV_VAR`. |
| `defaultChannel` | string | `""` | ID del canal predeterminado para notificaciones salientes. |

### Webhooks de Salida

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `url` | string | requerido | URL del endpoint del webhook. |
| `headers` | map[string]string | `{}` | Cabeceras HTTP a incluir. Los valores admiten `$ENV_VAR`. |
| `events` | string[] | todos | Eventos a enviar: `"success"`, `"error"`, `"timeout"`, `"all"`. Vacío = todos. |

### Webhooks de Entrada

Los webhooks de entrada permiten a servicios externos disparar tareas de Tetora mediante HTTP POST.

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

### Canales de Notificación

Canales de notificación con nombre para enrutar eventos de tareas a diferentes endpoints de Slack/Discord.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `name` | string | `""` | Referencia con nombre usada en el campo `channel` del trabajo (ej., `"discord:alerts"`). |
| `type` | string | requerido | `"slack"` o `"discord"`. |
| `webhookUrl` | string | requerido | URL del webhook. Admite `$ENV_VAR`. |
| `events` | string[] | todos | Filtrar por tipo de evento: `"all"`, `"error"`, `"success"`. |
| `minPriority` | string | todos | Prioridad mínima: `"critical"`, `"high"`, `"normal"`, `"low"`. |

---

## Store (Marketplace de Plantillas)

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el store de plantillas. |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | URL del registro remoto para explorar e instalar plantillas. |
| `authToken` | string | `""` | Token de autenticación para el registro. Admite `$ENV_VAR`. |

---

## Costos y Alertas

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | Límite de gasto diario en USD. `0` = sin límite. |
| `weeklyLimit` | float64 | `0` | Límite de gasto semanal en USD. `0` = sin límite. |
| `dailyTokenLimit` | int | `0` | Cuota total diaria de tokens (entrada + salida). `0` = sin cuota. |
| `action` | string | `"warn"` | Acción al superar el límite: `"warn"` (registrar y notificar) o `"pause"` (bloquear nuevas tareas). |

### `estimate` — `EstimateConfig`

Estimación de costo antes de ejecutar una tarea.

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | Solicitar confirmación cuando el costo estimado supere este valor en USD. |
| `defaultOutputTokens` | int | `500` | Estimación de tokens de salida de reserva cuando el uso real es desconocido. |

### `budgets` — `BudgetConfig`

Presupuestos de costo a nivel de agente y de equipo.

---

## Registro

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `level` | string | `"info"` | Nivel de registro: `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `format` | string | `"text"` | Formato de registro: `"text"` (legible por humanos) o `"json"` (estructurado). |
| `file` | string | `runtime/logs/tetora.log` | Ruta del archivo de registro. Relativa al directorio runtime. |
| `maxSizeMB` | int | `50` | Tamaño máximo del archivo de registro en MB antes de rotar. |
| `maxFiles` | int | `5` | Número de archivos de registro rotados a conservar. |

---

## Seguridad

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar la autenticación HTTP Basic en el dashboard. |
| `username` | string | `"admin"` | Nombre de usuario para autenticación básica. |
| `password` | string | `""` | Contraseña para autenticación básica. Admite `$ENV_VAR`. |
| `token` | string | `""` | Alternativa: token estático pasado como cookie. |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| Campo | Tipo | Descripción |
|---|---|---|
| `certFile` | string | Ruta al archivo PEM del certificado TLS. Habilita HTTPS cuando se establece (junto con `keyFile`). |
| `keyFile` | string | Ruta al archivo PEM de la clave privada TLS. |

### `rateLimit` — `RateLimitConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar la limitación de tasa de solicitudes por IP. |
| `maxPerMin` | int | `60` | Máximo de solicitudes API por minuto por IP. |

### `securityAlert` — `SecurityAlertConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar alertas de seguridad ante fallos de autenticación repetidos. |
| `failThreshold` | int | `10` | Número de fallos en la ventana antes de alertar. |
| `failWindowMin` | int | `5` | Ventana deslizante en minutos. |

### `approvalGates` — `ApprovalGateConfig`

Requiere aprobación humana antes de que ciertas herramientas se ejecuten.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar las compuertas de aprobación. |
| `timeout` | int | `120` | Segundos a esperar la aprobación antes de cancelar. |
| `tools` | string[] | `[]` | Nombres de herramientas que requieren aprobación antes de ejecutarse. |
| `autoApproveTools` | string[] | `[]` | Herramientas pre-aprobadas al iniciar (nunca solicitan confirmación). |

---

## Fiabilidad

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `true` | Habilitar el circuit breaker para la conmutación por error del proveedor. |
| `failThreshold` | int | `5` | Fallos consecutivos antes de abrir el circuito. |
| `successThreshold` | int | `2` | Éxitos en estado semi-abierto antes de cerrar. |
| `openTimeout` | string | `"30s"` | Duración en estado abierto antes de intentar de nuevo (semi-abierto). |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

Lista ordenada global de proveedores de reserva si el proveedor predeterminado falla.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar la monitorización del heartbeat del agente. |
| `interval` | string | `"30s"` | Con qué frecuencia verificar tareas en ejecución en busca de bloqueos. |
| `stallThreshold` | string | `"5m"` | Sin salida durante esta duración = tarea bloqueada. |
| `timeoutWarnRatio` | float64 | `0.8` | Advertir cuando el tiempo transcurrido supere esta proporción del tiempo de espera de la tarea. |
| `autoCancel` | bool | `false` | Cancelar automáticamente tareas bloqueadas durante más de `2x stallThreshold`. |
| `notifyOnStall` | bool | `true` | Enviar una notificación cuando se detecte una tarea bloqueada. |

### `retention` — `RetentionConfig`

Controla la limpieza automática de datos antiguos.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `history` | int | `90` | Días para conservar el historial de ejecuciones de trabajos. |
| `sessions` | int | `30` | Días para conservar los datos de sesión. |
| `auditLog` | int | `365` | Días para conservar las entradas del log de auditoría. |
| `logs` | int | `14` | Días para conservar los archivos de log. |
| `workflows` | int | `90` | Días para conservar los registros de ejecuciones de workflows. |
| `reflections` | int | `60` | Días para conservar los registros de reflexiones. |
| `sla` | int | `90` | Días para conservar los registros de verificaciones SLA. |
| `trustEvents` | int | `90` | Días para conservar los registros de eventos de confianza. |
| `handoffs` | int | `60` | Días para conservar los registros de handoffs/mensajes entre agentes. |
| `queue` | int | `7` | Días para conservar los elementos de la cola offline. |
| `versions` | int | `180` | Días para conservar los snapshots de versiones de configuración. |
| `outputs` | int | `30` | Días para conservar los archivos de salida del agente. |
| `uploads` | int | `7` | Días para conservar los archivos subidos. |
| `memory` | int | `30` | Días antes de que las entradas de memoria obsoletas sean archivadas. |
| `claudeSessions` | int | `3` | Días para conservar los artefactos de sesión del Claude CLI. |
| `piiPatterns` | string[] | `[]` | Patrones regex para la redacción de PII en el contenido almacenado. |

---

## Horas Silenciosas y Resumen Diario

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar las horas silenciosas. Las notificaciones se suprimen durante esta ventana. |
| `start` | string | `""` | Inicio del periodo silencioso (hora local, formato `"HH:MM"`). |
| `end` | string | `""` | Fin del periodo silencioso (hora local). |
| `tz` | string | local | Zona horaria, ej. `"Asia/Taipei"`, `"UTC"`. |
| `digest` | bool | `false` | Enviar un resumen de las notificaciones suprimidas cuando terminen las horas silenciosas. |

### `digest` — `DigestConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar el resumen diario programado. |
| `time` | string | `"08:00"` | Hora a la que enviar el resumen (`"HH:MM"`). |
| `tz` | string | local | Zona horaria. |

---

## Herramientas

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `maxIterations` | int | `10` | Máximo de iteraciones de llamadas a herramientas por tarea. |
| `timeout` | int | `120` | Tiempo de espera global del motor de herramientas en segundos. |
| `toolOutputLimit` | int | `10240` | Máximo de caracteres por salida de herramienta (se trunca más allá de esto). |
| `toolTimeout` | int | `30` | Tiempo de espera de ejecución por herramienta en segundos. |
| `defaultProfile` | string | `"standard"` | Nombre del perfil de herramientas predeterminado. |
| `builtin` | map[string]bool | `{}` | Habilitar/deshabilitar herramientas integradas individuales por nombre. |
| `profiles` | map[string]ToolProfile | `{}` | Perfiles de herramientas personalizados. |
| `trustOverride` | map[string]string | `{}` | Sobreescribir el nivel de confianza por nombre de herramienta. |

### `tools.webSearch` — `WebSearchConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `provider` | string | `""` | Proveedor de búsqueda: `"brave"`, `"tavily"`, `"searxng"`. |
| `apiKey` | string | `""` | Clave API para el proveedor. Admite `$ENV_VAR`. |
| `baseURL` | string | `""` | Endpoint personalizado (para searxng auto-alojado). |
| `maxResults` | int | `5` | Máximo de resultados de búsqueda a devolver. |

### `tools.vision` — `VisionConfig`

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `provider` | string | `""` | Proveedor de visión: `"anthropic"`, `"openai"`, `"google"`. |
| `apiKey` | string | `""` | Clave API. Admite `$ENV_VAR`. |
| `model` | string | `""` | Nombre del modelo para el proveedor de visión. |
| `maxImageSize` | int | `5242880` | Tamaño máximo de imagen en bytes (predeterminado 5 MB). |
| `baseURL` | string | `""` | Endpoint API personalizado. |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

Configuraciones de servidor MCP con nombre. Cada clave es un nombre de configuración MCP; el valor es la configuración MCP JSON completa. Tetora escribe estas en archivos temporales y las pasa al binario claude mediante `--mcp-config`.

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

Definiciones simplificadas de servidor MCP gestionadas directamente por Tetora.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `command` | string | requerido | Comando ejecutable. |
| `args` | string[] | `[]` | Argumentos del comando. |
| `env` | map[string]string | `{}` | Variables de entorno para el proceso. Los valores admiten `$ENV_VAR`. |
| `enabled` | bool | `true` | Si este servidor MCP está activo. |

---

## Presupuesto de Prompt

Controla los presupuestos máximos de caracteres para cada sección del system prompt. Ajustar cuando los prompts se truncan inesperadamente.

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `soulMax` | int | `8000` | Máximo de caracteres para el prompt de personalidad/soul del agente. |
| `rulesMax` | int | `4000` | Máximo de caracteres para las reglas del workspace. |
| `knowledgeMax` | int | `8000` | Máximo de caracteres para el contenido de la base de conocimiento. |
| `skillsMax` | int | `4000` | Máximo de caracteres para las skills inyectadas. |
| `maxSkillsPerTask` | int | `3` | Número máximo de skills inyectadas por tarea. |
| `contextMax` | int | `16000` | Máximo de caracteres para el contexto de sesión. |
| `totalMax` | int | `40000` | Límite máximo del tamaño total del system prompt (todas las secciones combinadas). |

---

## Comunicación entre Agentes

Controla el envío de sub-agentes anidados (herramienta agent_dispatch).

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

| Campo | Tipo | Predeterminado | Descripción |
|---|---|---|---|
| `enabled` | bool | `false` | Habilitar la herramienta `agent_dispatch` para llamadas de sub-agentes anidados. |
| `maxConcurrent` | int | `3` | Máximo de llamadas concurrentes a `agent_dispatch` globalmente. |
| `defaultTimeout` | int | `900` | Tiempo de espera predeterminado del sub-agente en segundos. |
| `maxDepth` | int | `3` | Profundidad máxima de anidamiento para sub-agentes. |
| `maxChildrenPerTask` | int | `5` | Máximo de agentes hijos concurrentes por tarea padre. |

---

## Ejemplos

### Configuración Mínima

Una configuración mínima para comenzar con el proveedor Claude CLI:

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

### Configuración Multi-Agente con Smart Dispatch

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

### Configuración Completa (Todas las Secciones Principales)

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
