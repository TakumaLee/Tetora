---
title: "Integración MCP (Model Context Protocol)"
lang: "es"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# Integración MCP (Model Context Protocol)

Tetora incluye un servidor MCP integrado que permite a los agentes de IA (Claude Code, etc.) interactuar con las APIs de Tetora a través del protocolo MCP estándar.

## Arquitectura

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (cliente)               (proceso puente)             (localhost:8991)
```

El servidor MCP es un **puente stdio JSON-RPC 2.0** — lee las solicitudes desde stdin, las delega a la API HTTP de Tetora y escribe las respuestas en stdout. Claude Code lo lanza como proceso hijo.

## Configuración

### 1. Agregar el servidor MCP a la configuración de Claude Code

Agregar lo siguiente a `~/.claude/settings.json`:

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

Reemplazar la ruta con la ubicación real del binario `tetora`. Encontrarla con:

```bash
which tetora
# o
ls ~/.tetora/bin/tetora
```

### 2. Asegurarse de que el daemon de Tetora está en ejecución

El puente MCP delega a la API HTTP de Tetora, por lo que el daemon debe estar en ejecución:

```bash
tetora start
```

### 3. Verificar

Reiniciar Claude Code. Las herramientas MCP aparecerán como herramientas disponibles con el prefijo `tetora_`.

## Herramientas Disponibles

### Gestión de Tareas

| Herramienta | Descripción |
|------|-------------|
| `tetora_taskboard_list` | Listar tickets del board kanban. Filtros opcionales: `project`, `assignee`, `priority`. |
| `tetora_taskboard_update` | Actualizar una tarea (estado, asignado, prioridad, título). Requiere `id`. |
| `tetora_taskboard_comment` | Agregar un comentario a una tarea. Requiere `id` y `comment`. |

### Memoria

| Herramienta | Descripción |
|------|-------------|
| `tetora_memory_get` | Leer una entrada de memoria. Requiere `agent` y `key`. |
| `tetora_memory_set` | Escribir una entrada de memoria. Requiere `agent`, `key` y `value`. |
| `tetora_memory_search` | Listar todas las entradas de memoria. Filtro opcional: `role`. |

### Dispatch

| Herramienta | Descripción |
|------|-------------|
| `tetora_dispatch` | Enviar una tarea a otro agente. Crea una nueva sesión de Claude Code. Requiere `prompt`. Opcional: `agent`, `workdir`, `model`. |

### Conocimiento

| Herramienta | Descripción |
|------|-------------|
| `tetora_knowledge_search` | Buscar en la base de conocimiento compartida. Requiere `q`. Opcional: `limit`. |

### Notificaciones

| Herramienta | Descripción |
|------|-------------|
| `tetora_notify` | Enviar una notificación al usuario vía Discord/Telegram. Requiere `message`. Opcional: `level` (info/warn/error). |
| `tetora_ask_user` | Hacer una pregunta al usuario vía Discord y esperar la respuesta (hasta 6 minutos). Requiere `question`. Opcional: `options` (botones de respuesta rápida, máximo 4). |

## Detalle de Herramientas

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

Todos los parámetros son opcionales. Devuelve un array JSON de tareas.

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

Solo `id` es requerido. Los demás campos se actualizan solo si se proporcionan. Valores de estado: `todo`, `in_progress`, `review`, `done`.

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

Solo `prompt` es requerido. Si se omite `agent`, el smart dispatch de Tetora enruta al mejor agente.

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

Esta es una **llamada bloqueante** — espera hasta 6 minutos a que el usuario responda vía Discord. El usuario ve la pregunta con botones opcionales de respuesta rápida y también puede escribir una respuesta personalizada.

## Comandos CLI

### Gestión de Servidores MCP Externos

Tetora también puede actuar como **host** MCP, conectándose a servidores MCP externos:

```bash
# Listar servidores MCP configurados
tetora mcp list

# Mostrar configuración completa de un servidor
tetora mcp show <name>

# Agregar un nuevo servidor MCP
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# Eliminar la configuración de un servidor
tetora mcp remove <name>

# Probar la conexión al servidor
tetora mcp test <name>
```

### Ejecutar el Puente MCP

```bash
# Iniciar el servidor del puente MCP (normalmente lanzado por Claude Code, no manualmente)
tetora mcp-server
```

En la primera ejecución, genera `~/.tetora/mcp/bridge.json` con la ruta correcta del binario.

## Configuración

Ajustes relacionados con MCP en `config.json`:

| Campo | Tipo | Predeterminado | Descripción |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | Mapa de configuraciones de servidores MCP externos (nombre → {command, args, env}). |

El servidor del puente lee `listenAddr` y `apiToken` de la configuración principal para conectarse al daemon.

## Autenticación

Si `apiToken` está configurado en `config.json`, el puente MCP incluye automáticamente `Authorization: Bearer <token>` en todas las solicitudes HTTP al daemon. No se necesita autenticación adicional a nivel MCP.

## Solución de Problemas

**Las herramientas no aparecen en Claude Code:**
- Verificar que la ruta del binario en `settings.json` es correcta
- Asegurarse de que el daemon de Tetora está en ejecución (`tetora start`)
- Revisar los logs de Claude Code en busca de errores de conexión MCP

**Errores "HTTP 401":**
- El `apiToken` en `config.json` debe coincidir. El puente lo lee automáticamente.

**Errores "connection refused":**
- El daemon no está en ejecución, o `listenAddr` no coincide. Predeterminado: `127.0.0.1:8991`.

**`tetora_ask_user` agota el tiempo de espera:**
- El usuario tiene 6 minutos para responder vía Discord. Asegurarse de que el bot de Discord está conectado.
