---
title: "Integración de Claude Code Hooks"
lang: "es"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Integración de Claude Code Hooks

## Descripción General

Los Claude Code Hooks son un sistema de eventos integrado en Claude Code que dispara comandos de shell en puntos clave durante una sesión. Tetora se registra como receptor de hooks para poder observar cada sesión de agente en ejecución en tiempo real, sin sondeos, sin tmux y sin inyectar scripts intermediarios.

**Lo que los hooks habilitan:**

- Seguimiento de progreso en tiempo real en el dashboard (llamadas a herramientas, estado de sesión, lista de workers en vivo)
- Monitoreo de costos y tokens mediante el puente de la statusline
- Auditoría del uso de herramientas (qué herramientas se ejecutaron, en qué sesión, en qué directorio)
- Detección de finalización de sesión y actualizaciones automáticas del estado de tareas
- Compuerta de plan mode: retiene `ExitPlanMode` hasta que un humano apruebe el plan en el dashboard
- Enrutamiento de preguntas interactivas: `AskUserQuestion` se redirige al puente MCP para que las preguntas aparezcan en tu plataforma de chat en lugar de bloquear el terminal

Los hooks son la ruta de integración recomendada a partir de Tetora v2.0. El enfoque anterior basado en tmux (v1.x) sigue funcionando, pero no admite las funciones exclusivas de hooks como la compuerta de plan y el enrutamiento de preguntas.

---

## Arquitectura

```
Sesión de Claude Code
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Compuerta de plan: long-poll hasta aprobación
  │   (AskUserQuestion)               └─► Denegar: redirigir al puente MCP
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► Actualizar estado del worker
  │                                   └─► Detectar escrituras en archivos de plan
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► Marcar worker como completado
  │                                   └─► Disparar finalización de tarea
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► Reenviar a Discord/Telegram
```

El comando de hook es una pequeña llamada curl inyectada en el `~/.claude/settings.json` de Claude Code. Cada evento se publica en `POST /api/hooks/event` en el daemon de Tetora en ejecución.

---

## Configuración

### Instalar hooks

Con el daemon de Tetora en ejecución:

```bash
tetora hooks install
```

Esto escribe entradas en `~/.claude/settings.json` y genera la configuración del puente MCP en `~/.tetora/mcp/bridge.json`.

Ejemplo de lo que se escribe en `~/.claude/settings.json`:

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

### Verificar estado

```bash
tetora hooks status
```

La salida muestra qué hooks están instalados, cuántas reglas de Tetora están registradas y el conteo total de eventos recibidos desde que el daemon inició.

También puedes verificar desde el dashboard: **Engineering Details → Hooks** muestra el mismo estado más un feed de eventos en vivo.

### Eliminar hooks

```bash
tetora hooks remove
```

Elimina todas las entradas de Tetora de `~/.claude/settings.json`. Los hooks existentes que no sean de Tetora se conservan.

---

## Eventos de Hook

### PostToolUse

Se dispara después de que cada llamada a herramienta se completa. Tetora usa este evento para:

- Rastrear qué herramientas está usando un agente (`Bash`, `Write`, `Edit`, `Read`, etc.)
- Actualizar `lastTool` y `toolCount` del worker en la lista de workers en vivo
- Detectar cuando un agente escribe en un archivo de plan (dispara la actualización del caché del plan)

### Stop

Se dispara cuando una sesión de Claude Code termina (finalización natural o cancelación). Tetora usa este evento para:

- Marcar el worker como `done` en la lista de workers en vivo
- Publicar un evento SSE de finalización en el dashboard
- Disparar actualizaciones de estado de tareas para las tareas del taskboard

### Notification

Se dispara cuando Claude Code envía una notificación (ej., permiso requerido, pausa prolongada). Tetora las reenvía a Discord/Telegram y las publica en el stream SSE del dashboard.

### PreToolUse: ExitPlanMode (compuerta de plan)

Cuando un agente está a punto de salir del plan mode, Tetora intercepta el evento con un long-poll (tiempo de espera: 600 segundos). El contenido del plan se almacena en caché y se muestra en el dashboard bajo la vista de detalle de la sesión.

Un humano puede aprobar o rechazar el plan desde el dashboard. Si se aprueba, el hook regresa y Claude Code procede. Si se rechaza (o si el tiempo de espera expira), la salida es bloqueada y Claude Code permanece en plan mode.

### PreToolUse: AskUserQuestion (enrutamiento de preguntas)

Cuando Claude Code intenta hacerle una pregunta al usuario de forma interactiva, Tetora lo intercepta y deniega el comportamiento predeterminado. La pregunta se enruta a través del puente MCP, apareciendo en tu plataforma de chat configurada (Discord, Telegram, etc.) para que puedas responder sin estar frente al terminal.

---

## Seguimiento de Progreso en Tiempo Real

Una vez instalados los hooks, el panel **Workers** del dashboard muestra las sesiones en vivo:

| Campo | Origen |
|---|---|
| ID de sesión | `session_id` en el evento de hook |
| Estado | `working` / `idle` / `done` |
| Última herramienta | Nombre de herramienta del `PostToolUse` más reciente |
| Directorio de trabajo | `cwd` del evento de hook |
| Conteo de herramientas | Conteo acumulado de `PostToolUse` |
| Costo / tokens | Puente de statusline (`POST /api/hooks/usage`) |
| Origen | Tarea vinculada o trabajo cron si fue enviado por Tetora |

Los datos de costo y tokens provienen del script de statusline de Claude Code, que publica en `/api/hooks/usage` a un intervalo configurable. El script de statusline es independiente de los hooks — lee la salida de la barra de estado de Claude Code y la reenvía a Tetora.

---

## Monitoreo de Costos

El endpoint de uso (`POST /api/hooks/usage`) recibe:

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

Estos datos son visibles en el panel Workers del dashboard y se agregan en los gráficos de costo diario. Las alertas de presupuesto se disparan cuando el costo de una sesión supera el presupuesto configurado por rol o global.

---

## Solución de Problemas

### Los hooks no se disparan

**Verificar que el daemon está en ejecución:**
```bash
tetora status
```

**Verificar que los hooks están instalados:**
```bash
tetora hooks status
```

**Verificar settings.json directamente:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

Si falta la clave de hooks, volver a ejecutar `tetora hooks install`.

**Verificar que el daemon puede recibir eventos de hook:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# Respuesta esperada: {"ok":true}
```

Si el daemon no está escuchando en el puerto esperado, verificar `listenAddr` en `config.json`.

### Errores de permisos en settings.json

El `settings.json` de Claude Code está en `~/.claude/settings.json`. Si el archivo es propiedad de otro usuario o tiene permisos restrictivos:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### El panel Workers del dashboard está vacío

1. Confirmar que los hooks están instalados y el daemon está en ejecución.
2. Iniciar una sesión de Claude Code manualmente y ejecutar una herramienta (ej. `ls`).
3. Verificar el panel Workers del dashboard — la sesión debería aparecer en segundos.
4. Si no aparece, verificar los logs del daemon: `tetora logs -f | grep hooks`

### La compuerta de plan no aparece

La compuerta de plan solo se activa cuando Claude Code intenta llamar a `ExitPlanMode`. Esto solo ocurre en sesiones de plan mode (iniciadas con `--plan` o configuradas mediante `permissionMode: "plan"` en la configuración del rol). Las sesiones interactivas con `acceptEdits` no usan el plan mode.

### Las preguntas no se enrutan al chat

El hook de denegación `AskUserQuestion` requiere que el puente MCP esté configurado. Ejecutar `tetora hooks install` nuevamente — regenera la configuración del puente. Luego agregar el puente a la configuración MCP de Claude Code:

```bash
cat ~/.tetora/mcp/bridge.json
```

Agregar ese archivo como servidor MCP en `~/.claude/settings.json` bajo `mcpServers`.

---

## Migración desde tmux (v1.x)

En Tetora v1.x, los agentes se ejecutaban dentro de paneles tmux y Tetora los monitoreaba leyendo la salida del panel. En v2.0, los agentes se ejecutan como procesos Claude Code independientes y Tetora los observa mediante hooks.

**Si estás actualizando desde v1.x:**

1. Ejecutar `tetora hooks install` una vez después de actualizar.
2. Eliminar cualquier configuración de gestión de sesiones tmux de `config.json` (las claves `tmux.*` ahora son ignoradas).
3. El historial de sesiones existente se conserva en `history.db` — no se necesita migración.
4. El comando `tetora session list` y la pestaña Sessions en el dashboard continúan funcionando como antes.

El puente de terminal tmux (`discord_terminal.go`) sigue disponible para acceso interactivo al terminal vía Discord. Esto es independiente de la ejecución de agentes — permite enviar pulsaciones de teclas a una sesión de terminal en ejecución. Los hooks y el puente de terminal son complementarios, no mutuamente excluyentes.
