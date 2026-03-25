---
title: "Guía de Solución de Problemas"
lang: "es"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# Guía de Solución de Problemas

Esta guía cubre los problemas más comunes al ejecutar Tetora. Para cada problema, la causa más probable se lista primero.

---

## tetora doctor

Siempre comenzar aquí. Ejecutar `tetora doctor` después de la instalación o cuando algo deje de funcionar:

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

Cada línea es una verificación. Una `✗` roja indica un fallo grave (el daemon no funcionará sin corregirlo). Una `~` amarilla indica una sugerencia (opcional pero recomendada).

Correcciones comunes para verificaciones fallidas:

| Verificación fallida | Corrección |
|---|---|
| `Config: not found` | Ejecutar `tetora init` |
| `Claude CLI: not found` | Establecer `claudePath` en `config.json` o instalar Claude Code |
| `sqlite3: not found` | `brew install sqlite3` (macOS) o `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | Crear `~/.tetora/agents/{name}/SOUL.md` o ejecutar `tetora init` |
| `Workspace: not found` | Ejecutar `tetora init` para crear la estructura de directorios |

---

## "session produced no output"

Una tarea se completa pero la salida está vacía. La tarea se marca automáticamente como `failed`.

**Causa 1: Ventana de contexto demasiado grande.** El prompt inyectado en la sesión superó el límite de contexto del modelo. Claude Code termina inmediatamente cuando no puede acomodar el contexto.

Corrección: Habilitar la compactación de sesiones en `config.json`:

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

O reducir la cantidad de contexto inyectado en la tarea (descripción más corta, menos comentarios de especificación, cadena `dependsOn` más pequeña).

**Causa 2: Fallo al iniciar el Claude Code CLI.** El binario en `claudePath` falla al arrancar — generalmente debido a una clave API incorrecta, problema de red o incompatibilidad de versiones.

Corrección: Ejecutar el binario de Claude Code manualmente para ver el error:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

Corregir el error reportado, luego reintentar la tarea:

```bash
tetora task move task-abc123 --status=todo
```

**Causa 3: Prompt vacío.** La tarea tiene un título pero no una descripción, y el título solo es demasiado ambiguo para que el agente actúe. La sesión se ejecuta, produce una salida que no satisface la verificación de vacío, y queda marcada.

Corrección: Agregar una descripción concreta:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## Errores "unauthorized" en el dashboard

El dashboard devuelve 401 o muestra una página en blanco al recargar.

**Causa 1: Service Worker almacenó en caché un token de autenticación antiguo.** El Service Worker de la PWA almacena en caché las respuestas incluyendo las cabeceras de autenticación. Después de reiniciar el daemon con un nuevo token, la versión en caché queda obsoleta.

Corrección: Forzar la recarga de la página. En Chrome/Safari:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

O abrir DevTools → Application → Service Workers → hacer clic en "Unregister", luego recargar.

**Causa 2: Falta de coincidencia en la cabecera Referer.** El middleware de autenticación del dashboard valida la cabecera `Referer`. Las solicitudes de extensiones de navegador, proxies o curl sin cabecera `Referer` son rechazadas.

Corrección: Acceder al dashboard directamente en `http://localhost:8991/dashboard`, no a través de un proxy. Si necesitas acceso a la API desde herramientas externas, usar un token API en lugar de la autenticación de sesión del navegador.

---

## El dashboard no se actualiza

El dashboard carga pero el feed de actividad, la lista de workers o el task board permanecen desactualizados.

**Causa: Incompatibilidad de versiones del Service Worker.** El Service Worker de la PWA sirve una versión en caché del JS/HTML del dashboard incluso después de una actualización con `make bump`.

Corrección:

1. Forzar recarga (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. Si eso no funciona, abrir DevTools → Application → Service Workers → hacer clic en "Update" o "Unregister"
3. Recargar la página

**Causa: Conexión SSE interrumpida.** El dashboard recibe actualizaciones en vivo a través de Server-Sent Events. Si la conexión se interrumpe (problema de red, suspensión del portátil), el feed deja de actualizarse.

Corrección: Recargar la página. La conexión SSE se restablece automáticamente al cargar la página.

---

## Advertencia "排程接近滿載"

Este mensaje aparece en Discord/Telegram o en el feed de notificaciones del dashboard.

Esta es la advertencia de presión de slots. Se dispara cuando los slots de concurrencia disponibles caen al nivel de `warnThreshold` o por debajo (predeterminado: 3). Significa que Tetora está operando cerca de su capacidad.

**Qué hacer:**

- Si esto es esperado (muchas tareas en ejecución): no se requiere acción. La advertencia es informativa.
- Si no tienes muchas tareas en ejecución: verificar si hay tareas bloqueadas en estado `doing`:

```bash
tetora task list --status=doing
```

- Si quieres aumentar la capacidad: incrementar `maxConcurrent` en `config.json` y ajustar `slotPressure.warnThreshold` en consecuencia.
- Si las sesiones interactivas están siendo retrasadas: aumentar `slotPressure.reservedSlots` para reservar más slots para uso interactivo.

---

## Tareas bloqueadas en "doing"

Una tarea muestra `status=doing` pero ningún agente está trabajando activamente en ella.

**Causa 1: El daemon se reinició a mitad de tarea.** La tarea estaba en ejecución cuando el daemon fue terminado. En el próximo arranque, Tetora verifica las tareas `doing` huérfanas y las restaura a `done` (si hay evidencia de costo/duración) o las resetea a `todo`.

Esto es automático — esperar al próximo arranque del daemon. Si el daemon ya está en ejecución y la tarea sigue bloqueada, el heartbeat o la detección de tareas bloqueadas lo resolverá dentro de `stuckThreshold` (predeterminado: 2h).

Para forzar un reset inmediato:

```bash
tetora task move task-abc123 --status=todo
```

**Causa 2: Detección de heartbeat/bloqueo.** El monitor de heartbeat (`heartbeat.go`) verifica las sesiones en ejecución. Si una sesión no produce salida durante el umbral de bloqueo, se cancela automáticamente y la tarea se mueve a `failed`.

Verificar los comentarios de la tarea en busca de comentarios de sistema `[auto-reset]` o `[stall-detected]`:

```bash
tetora task show task-abc123 --full
```

**Cancelar manualmente vía API:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Fallos de merge en worktree

Una tarea finaliza y pasa a `partial-done` con un comentario como `[worktree] merge failed`.

Esto significa que los cambios del agente en la rama de tarea entran en conflicto con `main`.

**Pasos de recuperación:**

```bash
# Ver los detalles de la tarea y qué rama fue creada
tetora task show task-abc123 --full

# Navegar al repositorio del proyecto
cd /path/to/your/repo

# Fusionar la rama manualmente
git merge feat/kokuyou-task-abc123

# Resolver los conflictos en tu editor, luego hacer commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# Marcar la tarea como completada
tetora task move task-abc123 --status=done
```

El directorio del worktree se conserva en `~/.tetora/runtime/worktrees/task-abc123/` hasta que lo limpies manualmente o muevas la tarea a `done`.

---

## Costos de tokens elevados

Las sesiones están usando más tokens de lo esperado.

**Causa 1: El contexto no está siendo compactado.** Sin compactación de sesiones, cada turno acumula el historial completo de la conversación. Las tareas de larga duración (muchas llamadas a herramientas) hacen crecer el contexto linealmente.

Corrección: Habilitar `sessionCompaction` (ver la sección "session produced no output" más arriba).

**Causa 2: Archivos de base de conocimiento o reglas de gran tamaño.** Los archivos en `workspace/rules/` y `workspace/knowledge/` se inyectan en cada prompt de agente. Si estos archivos son grandes, consumen tokens en cada llamada.

Corrección:
- Auditar `workspace/knowledge/` — mantener los archivos individuales por debajo de 50 KB.
- Mover el material de referencia que rara vez necesitas fuera de las rutas de auto-inyección.
- Ejecutar `tetora knowledge list` para ver qué se está inyectando y su tamaño.

**Causa 3: Enrutamiento de modelo incorrecto.** Se está usando un modelo costoso (Opus) para tareas rutinarias.

Corrección: Revisar `defaultModel` en la configuración del agente y establecer un modelo más económico para tareas masivas:

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

## Errores de timeout del proveedor

Las tareas fallan con errores de timeout como `context deadline exceeded` o `provider request timed out`.

**Causa 1: Timeout de tarea demasiado corto.** El timeout predeterminado puede ser demasiado breve para tareas complejas.

Corrección: Establecer un timeout más largo en la configuración del agente o por tarea:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

O aumentar la estimación del timeout del LLM agregando más detalle a la descripción de la tarea (Tetora usa la descripción para estimar el timeout mediante una llamada rápida al modelo).

**Causa 2: Limitación de tasa o contención de la API.** Demasiadas solicitudes concurrentes llegando al mismo proveedor.

Corrección: Reducir `maxConcurrentTasks` o agregar un `maxBudget` para limitar las tareas costosas:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump` interrumpió un workflow

Ejecutaste `make bump` mientras un workflow o tarea estaba en ejecución. El daemon se reinició a mitad de tarea.

El reinicio activa la lógica de recuperación de huérfanos de Tetora:

- Las tareas con evidencia de finalización (costo registrado, duración registrada) se restauran a `done`.
- Las tareas sin evidencia de finalización pero pasado el período de gracia (2 minutos) se resetean a `todo` para ser re-despachadas.
- Las tareas actualizadas dentro de los últimos 2 minutos se dejan intactas hasta el próximo análisis de tareas bloqueadas.

**Para verificar qué ocurrió:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

Revisar los comentarios de tareas en busca de entradas `[auto-restore]` o `[auto-reset]`.

**Si necesitas evitar bumps durante tareas activas** (aún no disponible como flag), verificar que no haya tareas en ejecución antes de hacer el bump:

```bash
tetora task list --status=doing
# Si está vacío, es seguro hacer el bump
make bump
```

---

## Errores de SQLite

Aparecen errores como `database is locked`, `SQLITE_BUSY` o `index.lock` en los logs.

**Causa 1: Falta el pragma de modo WAL.** Sin el modo WAL, SQLite usa bloqueo exclusivo de archivos, lo que causa `database is locked` bajo lecturas/escrituras concurrentes.

Todas las llamadas a la base de datos de Tetora pasan por `queryDB()` y `execDB()` que anteponen `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`. Si estás llamando a sqlite3 directamente en scripts, agregar estos pragmas:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**Causa 2: Archivo `index.lock` obsoleto.** Las operaciones git dejan `index.lock` si se interrumpen. El gestor de worktree verifica si hay bloqueos obsoletos antes de iniciar el trabajo git, pero un crash puede dejar uno atrás.

Corrección:

```bash
# Encontrar archivos de bloqueo obsoletos
find ~/.tetora/runtime/worktrees -name "index.lock"

# Eliminarlos (solo si no hay ninguna operación git activa)
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram no responde

Los mensajes al bot no producen respuesta.

**Causa 1: Configuración de canal incorrecta.** Discord tiene dos listas de canales: `channelIDs` (respuesta directa a todos los mensajes) y `mentionChannelIDs` (solo responde cuando se menciona con @). Si un canal no está en ninguna lista, los mensajes son ignorados.

Corrección: Verificar `config.json`:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**Causa 2: Token del bot vencido o incorrecto.** Los tokens de bot de Telegram no vencen, pero los tokens de Discord pueden invalidarse si el bot es expulsado del servidor o el token es regenerado.

Corrección: Recrear el token del bot en el portal de desarrolladores de Discord y actualizar `config.json`.

**Causa 3: El daemon no está en ejecución.** El gateway del bot solo está activo cuando `tetora serve` está en ejecución.

Corrección:

```bash
tetora status
tetora serve   # si no está en ejecución
```

---

## Errores de CLI glab / gh

La integración con git falla con errores de `glab` o `gh`.

**Error común: `gh: command not found`**

Corrección:
```bash
brew install gh      # macOS
gh auth login        # autenticarse
```

**Error común: `glab: You are not logged in`**

Corrección:
```bash
brew install glab    # macOS
glab auth login      # autenticarse con tu instancia de GitLab
```

**Error común: `remote: HTTP Basic: Access denied`**

Corrección: Verificar que tu clave SSH o credencial HTTPS está configurada para el host del repositorio. Para GitLab:

```bash
glab auth status
ssh -T git@gitlab.com   # probar conectividad SSH
```

Para GitHub:

```bash
gh auth status
ssh -T git@github.com
```

**La creación del PR/MR tiene éxito pero apunta a la rama base incorrecta**

Por defecto, los PRs apuntan a la rama predeterminada del repositorio (`main` o `master`). Si tu workflow usa una base diferente, establecerla explícitamente en la configuración git post-tarea o asegurarse de que la rama predeterminada del repositorio está configurada correctamente en la plataforma de alojamiento.
