---
title: "Guía del Taskboard y Auto-Dispatch"
lang: "es"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Guía del Taskboard y Auto-Dispatch

## Descripción General

El Taskboard es el sistema kanban integrado de Tetora para hacer seguimiento y ejecutar tareas automáticamente. Combina un almacén de tareas persistente (respaldado por SQLite) con un motor de auto-dispatch que monitorea las tareas listas y las delega a los agentes sin intervención manual.

Casos de uso típicos:

- Acumular un backlog de tareas de ingeniería y dejar que los agentes las trabajen durante la noche
- Enrutar tareas a agentes específicos según su especialidad (ej. `kokuyou` para backend, `kohaku` para contenido)
- Encadenar tareas con relaciones de dependencia para que los agentes continúen donde otros dejaron
- Integrar la ejecución de tareas con git: creación automática de ramas, commit, push y PR/MR

**Requisitos:** `taskBoard.enabled: true` en `config.json` y el daemon de Tetora en ejecución.

---

## Ciclo de Vida de una Tarea

Las tareas fluyen a través de los siguientes estados en este orden:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| Estado | Significado |
|---|---|
| `idea` | Concepto preliminar, aún sin refinar |
| `needs-thought` | Requiere análisis o diseño antes de la implementación |
| `backlog` | Definida y priorizada, pero aún no programada |
| `todo` | Lista para ejecutar — el auto-dispatch la tomará si tiene un asignado |
| `doing` | En ejecución actualmente |
| `review` | Ejecución finalizada, esperando revisión de calidad |
| `done` | Completada y revisada |
| `partial-done` | La ejecución fue exitosa pero el post-procesamiento falló (ej. conflicto de merge git). Recuperable. |
| `failed` | La ejecución falló o produjo salida vacía. Se reintentará hasta `maxRetries`. |

El auto-dispatch toma las tareas con `status=todo`. Si una tarea no tiene asignado, se asigna automáticamente a `defaultAgent` (predeterminado: `ruri`). Las tareas en `backlog` son clasificadas periódicamente por el `backlogAgent` configurado (predeterminado: `ruri`), que promueve las prometedoras a `todo`.

---

## Creación de Tareas

### CLI

```bash
# Tarea mínima (va al backlog, sin asignar)
tetora task create --title="Add rate limiting to API"

# Con todas las opciones
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# Listar tareas
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# Mostrar una tarea específica
tetora task show task-abc123
tetora task show task-abc123 --full   # incluye comentarios/hilo

# Mover una tarea manualmente
tetora task move task-abc123 --status=todo

# Asignar a un agente
tetora task assign task-abc123 --assignee=kokuyou

# Agregar un comentario (tipos: spec, context, log o system)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

Los IDs de tarea se generan automáticamente en el formato `task-<uuid>`. Puedes referenciar una tarea por su ID completo o un prefijo corto — el CLI sugerirá coincidencias.

### HTTP API

```bash
# Crear
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# Listar (filtrar por estado)
curl "http://localhost:8991/api/tasks?status=todo"

# Mover a un nuevo estado
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### Dashboard

Abrir la pestaña **Taskboard** en el dashboard (`http://localhost:8991/dashboard`). Las tareas se muestran en columnas kanban. Arrastrar las tarjetas entre columnas para cambiar el estado; hacer clic en una tarjeta para abrir el panel de detalle con comentarios y vista de diff.

---

## Auto-Dispatch

El auto-dispatch es el ciclo en segundo plano que toma las tareas en `todo` y las ejecuta a través de los agentes.

### Cómo funciona

1. Un ticker se dispara cada `interval` (predeterminado: `5m`).
2. El escáner verifica cuántas tareas están en ejecución actualmente. Si `activeCount >= maxConcurrentTasks`, el análisis se omite.
3. Para cada tarea en `todo` con un asignado, la tarea se envía a ese agente. Las tareas sin asignar se asignan automáticamente a `defaultAgent`.
4. Cuando una tarea finaliza, se dispara un re-análisis inmediato para que el siguiente lote comience sin esperar el intervalo completo.
5. Al iniciar el daemon, las tareas en `doing` huérfanas de un crash anterior se restauran a `done` (si hay evidencia de finalización) o se resetean a `todo` (si están verdaderamente huérfanas).

### Flujo de Dispatch

```
                          ┌─────────┐
                          │  idea   │  (entrada manual de concepto)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (requiere análisis)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  Clasificación (backlogAgent, pred.: ruri) periódica:     │
  │   • "ready"     → asignar agente → mover a todo           │
  │   • "decompose" → crear subtareas → padre a doing         │
  │   • "clarify"   → agregar comentario de pregunta → quedar │
  │                                                           │
  │  Ruta rápida: ya tiene asignado + sin deps bloqueantes    │
  │   → saltar clasificación LLM, promover directamente a todo│
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Auto-dispatch toma tareas en cada ciclo de análisis:     │
  │   • Tiene asignado      → enviar a ese agente             │
  │   • Sin asignado        → asignar defaultAgent, luego     │
  │   • Tiene workflow      → ejecutar por pipeline de        │
  │   • Tiene dependsOn     → esperar hasta que deps sean done│
  │   • Ejecución anterior  → reanudar desde checkpoint       │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  El agente ejecuta la tarea (prompt único o workflow DAG) │
  │                                                           │
  │  Guardia: stuckThreshold (predeterminado 2h)              │
  │   • Si el workflow sigue en ejecución → actualizar tiempo │
  │   • Si está verdaderamente bloqueado  → resetear a todo   │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
     éxito    parcial fallo   fallo
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │   Botón Reanudar      │  Reintento (hasta maxRetries)
           │   en el dashboard     │  o escalar
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ escalar  │
       └────────┘            │ a humano │
                             └──────────┘
```

### Detalles de la Clasificación

La clasificación se ejecuta cada `backlogTriageInterval` (predeterminado: `1h`) y la realiza el `backlogAgent` (predeterminado: `ruri`). El agente recibe cada tarea del backlog con sus comentarios y el roster de agentes disponibles, luego decide:

| Acción | Efecto |
|---|---|
| `ready` | Asigna un agente específico y promueve a `todo` |
| `decompose` | Crea subtareas (con asignados), el padre se mueve a `doing` |
| `clarify` | Agrega una pregunta como comentario, la tarea permanece en `backlog` |

**Ruta rápida**: las tareas que ya tienen un asignado y no tienen dependencias bloqueantes omiten completamente la clasificación LLM y se promueven a `todo` de inmediato.

### Asignación Automática

Cuando una tarea en `todo` no tiene asignado, el dispatcher la asigna automáticamente a `defaultAgent` (configurable, predeterminado: `ruri`). Esto evita que las tareas queden bloqueadas silenciosamente. El flujo típico:

1. Tarea creada sin asignado → entra a `backlog`
2. La clasificación promueve a `todo` (con o sin asignar un agente)
3. Si la clasificación no asignó → el dispatcher asigna `defaultAgent`
4. La tarea se ejecuta normalmente

### Configuración

Agregar a `config.json`:

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| Campo | Predeterminado | Descripción |
|---|---|---|
| `enabled` | `false` | Habilitar el ciclo de auto-dispatch |
| `interval` | `5m` | Con qué frecuencia buscar tareas listas |
| `maxConcurrentTasks` | `3` | Máximo de tareas ejecutándose simultáneamente |
| `defaultAgent` | `ruri` | Asignado automáticamente a tareas `todo` sin asignado antes del dispatch |
| `backlogAgent` | `ruri` | Agente que revisa y promueve las tareas del backlog |
| `reviewAgent` | `ruri` | Agente que revisa la salida de tareas completadas |
| `escalateAssignee` | `takuma` | A quién se asigna cuando la revisión automática requiere juicio humano |
| `stuckThreshold` | `2h` | Tiempo máximo que una tarea puede permanecer en `doing` antes de resetearse |
| `backlogTriageInterval` | `1h` | Intervalo mínimo entre ejecuciones de clasificación del backlog |
| `reviewLoop` | `false` | Habilitar el ciclo Dev↔QA (ejecutar → revisar → corregir, hasta `maxRetries`) |
| `maxBudget` | sin límite | Costo máximo por tarea en USD |
| `defaultModel` | — | Sobreescribir el modelo para todas las tareas enviadas automáticamente |

---

## Presión de Slots

La presión de slots evita que el auto-dispatch consuma todos los slots de concurrencia y prive a las sesiones interactivas (mensajes de chat humano, dispatches bajo demanda).

Habilitarlo en `config.json`:

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| Campo | Predeterminado | Descripción |
|---|---|---|
| `reservedSlots` | `2` | Slots reservados para uso interactivo. Las tareas no interactivas deben esperar si los slots disponibles caen a este nivel. |
| `warnThreshold` | `3` | La advertencia se dispara cuando los slots disponibles caen a este nivel. El mensaje "排程接近滿載" aparece en el dashboard y los canales de notificación. |
| `nonInteractiveTimeout` | `5m` | Cuánto tiempo espera una tarea no interactiva por un slot antes de ser cancelada. |

Las fuentes interactivas (chat humano, `tetora dispatch`, `tetora route`) siempre adquieren slots inmediatamente. Las fuentes en segundo plano (taskboard, cron) esperan si la presión es alta.

---

## Integración con Git

Cuando `gitCommit`, `gitPush` y `gitPR` están habilitados, el dispatcher ejecuta operaciones git después de que una tarea se complete exitosamente.

**El nombre de rama** está controlado por `gitWorkflow.branchConvention`:

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

La plantilla predeterminada `{type}/{agent}-{description}` produce ramas como `feat/kokuyou-add-rate-limiting`. La parte `{description}` se deriva del título de la tarea (en minúsculas, espacios reemplazados por guiones, truncado a 40 caracteres).

El campo `type` de una tarea establece el prefijo de rama. Si una tarea no tiene tipo, se usa `defaultType`.

**Auto PR/MR** es compatible tanto con GitHub (`gh`) como con GitLab (`glab`). El binario disponible en `PATH` se usa automáticamente.

---

## Modo Worktree

Cuando `gitWorktree: true`, cada tarea se ejecuta en un git worktree aislado en lugar del directorio de trabajo compartido. Esto elimina los conflictos de archivos cuando varias tareas se ejecutan concurrentemente en el mismo repositorio.

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← copia aislada para esta tarea
  task-def456/   ← copia aislada para esta tarea
```

Al completar la tarea:

- Si `autoMerge: true` (predeterminado), la rama del worktree se fusiona de vuelta a `main` y el worktree se elimina.
- Si la fusión falla, la tarea pasa al estado `partial-done`. El worktree se conserva para resolución manual.

Para recuperarse de `partial-done`:

```bash
# Inspeccionar qué ocurrió
tetora task show task-abc123 --full

# Fusionar la rama manualmente
git merge feat/kokuyou-add-rate-limiting

# Resolver conflictos en tu editor, luego hacer commit
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# Marcar la tarea como completada
tetora task move task-abc123 --status=done
```

---

## Integración con Workflows

Las tareas pueden ejecutarse a través de un pipeline de workflow en lugar de un único prompt de agente. Esto es útil cuando una tarea requiere múltiples pasos coordinados (ej. investigación → implementación → pruebas → documentación).

Asignar un workflow a una tarea:

```bash
# Establecer al crear la tarea
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# O actualizar una tarea existente
tetora task update task-abc123 --workflow=engineering-pipeline
```

Para deshabilitar el workflow predeterminado del board para una tarea específica:

```json
{ "workflow": "none" }
```

Un workflow predeterminado a nivel de board se aplica a todas las tareas enviadas automáticamente, salvo que se sobreescriba:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

El campo `workflowRunId` de la tarea la vincula a la ejecución específica del workflow, visible en la pestaña Workflows del dashboard.

---

## Vistas del Dashboard

Abrir el dashboard en `http://localhost:8991/dashboard` y navegar a la pestaña **Taskboard**.

**Board kanban** — columnas para cada estado. Las tarjetas muestran título, asignado, badge de prioridad y costo. Arrastrar para cambiar el estado.

**Panel de detalle de tarea** — hacer clic en cualquier tarjeta para abrirlo. Muestra:
- Descripción completa y todos los comentarios (entradas de tipo spec, context, log)
- Enlace de sesión (salta al terminal del worker en vivo si aún está en ejecución)
- Costo, duración, conteo de reintentos
- Enlace al workflow run si aplica

**Panel de revisión de diff** — cuando `requireReview: true`, las tareas completadas aparecen en una cola de revisión. Los revisores ven el diff de cambios y pueden aprobar o solicitar modificaciones.

---

## Consejos

**Tamaño de tareas.** Mantener las tareas en un alcance de 30 a 90 minutos. Las tareas demasiado grandes (refactorizaciones de varios días) tienden a agotar el tiempo de espera o producir salida vacía y quedar marcadas como fallidas. Dividirlas en subtareas usando el campo `parentId`.

**Límites de dispatch concurrente.** `maxConcurrentTasks: 3` es un valor predeterminado seguro. Subirlo más allá del número de conexiones API que permite tu proveedor causa contención y timeouts. Comenzar en 3, subir a 5 solo después de confirmar un comportamiento estable.

**Recuperación de partial-done.** Si una tarea entra a `partial-done`, el agente completó su trabajo exitosamente — solo el paso de merge git falló. Resolver el conflicto manualmente, luego mover la tarea a `done`. Los datos de costo y sesión se conservan.

**Uso de `dependsOn`.** Las tareas con dependencias no cumplidas son omitidas por el dispatcher hasta que todos los IDs de tarea listados alcancen el estado `done`. Los resultados de las tareas anteriores se inyectan automáticamente en el prompt de la tarea dependiente bajo "Previous Task Results".

**Clasificación del backlog.** El `backlogAgent` lee cada tarea del `backlog`, evalúa la viabilidad y prioridad, y promueve las tareas claras a `todo`. Escribe descripciones detalladas y criterios de aceptación en las tareas del `backlog` — el agente de clasificación los usa para decidir si promover o dejar una tarea para revisión humana.

**Reintentos y el ciclo de revisión.** Con `reviewLoop: false` (predeterminado), una tarea fallida se reintenta hasta `maxRetries` veces con los comentarios de log anteriores inyectados. Con `reviewLoop: true`, cada ejecución es revisada por el `reviewAgent` antes de considerarse completada — el agente recibe retroalimentación y lo intenta de nuevo si se encuentran problemas.
