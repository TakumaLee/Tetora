# Flujos de Trabajo

## Descripción General

Los workflows son el sistema de orquestación de tareas multi-paso de Tetora. Define una secuencia de pasos en JSON, permite que distintos agentes colaboren y automatiza tareas complejas.

**Casos de uso:**

- Tareas que requieren múltiples agentes trabajando de forma secuencial o en paralelo
- Procesos con ramificación condicional y lógica de reintento ante errores
- Trabajo automatizado disparado por cron, eventos o webhooks
- Procesos formales que necesitan historial de ejecución y seguimiento de costos

## Inicio Rápido

### 1. Escribir el JSON del workflow

Crea `my-workflow.json`:

```json
{
  "name": "research-and-summarize",
  "description": "Gather information and write a summary",
  "variables": {
    "topic": "AI agents"
  },
  "timeout": "30m",
  "steps": [
    {
      "id": "research",
      "agent": "hisui",
      "prompt": "Search and organize the latest developments in {{topic}}, listing 5 key points"
    },
    {
      "id": "summarize",
      "agent": "kohaku",
      "prompt": "Write a 300-word summary based on the following:\n{{steps.research.output}}",
      "dependsOn": ["research"]
    }
  ]
}
```

### 2. Importar y validar

```bash
# Validar la estructura JSON
tetora workflow validate my-workflow.json

# Importar a ~/.tetora/workflows/
tetora workflow create my-workflow.json
```

### 3. Ejecutar

```bash
# Ejecutar el workflow
tetora workflow run research-and-summarize

# Sobreescribir variables
tetora workflow run research-and-summarize --var topic="LLM safety"

# Dry-run (sin llamadas a LLM, solo estimación de costos)
tetora workflow run research-and-summarize --dry-run
```

### 4. Consultar resultados

```bash
# Listar el historial de ejecuciones
tetora workflow runs research-and-summarize

# Ver el estado detallado de una ejecución específica
tetora workflow status <run-id>
```

## Estructura del JSON del Workflow

### Campos de Nivel Superior

| Campo | Tipo | Requerido | Descripción |
|-------|------|:---------:|-------------|
| `name` | string | Sí | Nombre del workflow. Solo alfanuméricos, `-` y `_` (ej. `my-workflow`) |
| `description` | string | | Descripción |
| `steps` | WorkflowStep[] | Sí | Al menos un paso |
| `variables` | map[string]string | | Variables de entrada con valores por defecto (`""` vacío = requerido) |
| `timeout` | string | | Tiempo límite global en formato de duración de Go (ej. `"30m"`, `"1h"`) |
| `onSuccess` | string | | Plantilla de notificación al completarse con éxito |
| `onFailure` | string | | Plantilla de notificación al producirse un fallo |

### Campos de WorkflowStep

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `id` | string | **Requerido** — Identificador único del paso |
| `type` | string | Tipo de paso, por defecto `"dispatch"`. Ver tipos a continuación |
| `agent` | string | Rol del agente que ejecuta este paso |
| `prompt` | string | Instrucción para el agente (admite plantillas `{{}}`) |
| `skill` | string | Nombre del skill (para type=skill) |
| `skillArgs` | string[] | Argumentos del skill (admite plantillas) |
| `dependsOn` | string[] | IDs de pasos prerrequisito (dependencias DAG) |
| `model` | string | Sobreescritura del modelo LLM |
| `provider` | string | Sobreescritura del proveedor |
| `timeout` | string | Tiempo límite por paso |
| `budget` | number | Límite de costo (USD) |
| `permissionMode` | string | Modo de permisos |
| `if` | string | Expresión de condición (type=condition) |
| `then` | string | ID de paso al que saltar cuando la condición es verdadera |
| `else` | string | ID de paso al que saltar cuando la condición es falsa |
| `handoffFrom` | string | ID del paso de origen (type=handoff) |
| `parallel` | WorkflowStep[] | Sub-pasos a ejecutar en paralelo (type=parallel) |
| `retryMax` | int | Máximo de reintentos (requiere `onError: "retry"`) |
| `retryDelay` | string | Intervalo entre reintentos, ej. `"10s"` |
| `onError` | string | Manejo de errores: `"stop"` (por defecto), `"skip"`, `"retry"` |
| `toolName` | string | Nombre de la herramienta (type=tool_call) |
| `toolInput` | map[string]string | Parámetros de entrada de la herramienta (admite expansión `{{var}}`) |
| `delay` | string | Duración de espera (type=delay), ej. `"30s"`, `"5m"` |
| `notifyMsg` | string | Mensaje de notificación (type=notify, admite plantillas) |
| `notifyTo` | string | Canal de notificación sugerido (ej. `"telegram"`) |

## Tipos de Paso

### dispatch (por defecto)

Envía un prompt al agente especificado para su ejecución. Es el tipo de paso más común y se usa cuando `type` se omite.

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**Requerido:** `prompt`
**Opcional:** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

Ejecuta un skill registrado.

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**Requerido:** `skill`
**Opcional:** `skillArgs`

### condition

Evalúa una expresión de condición para determinar la rama. Si es verdadera, toma `then`; si es falsa, toma `else`. La rama no elegida se marca como omitida.

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**Requerido:** `if`, `then`
**Opcional:** `else`

Operadores soportados:
- `==` — igual (ej. `{{type}} == 'technical'`)
- `!=` — distinto
- Verificación de valor verdadero — no vacío y distinto de `"false"`/`"0"` se evalúa como verdadero

### parallel

Ejecuta múltiples sub-pasos de forma concurrente, esperando a que todos finalicen. Las salidas de los sub-pasos se unen con `\n---\n`.

```json
{
  "id": "gather",
  "type": "parallel",
  "parallel": [
    {"id": "search-papers", "agent": "hisui", "prompt": "Search for papers"},
    {"id": "search-code", "agent": "kokuyou", "prompt": "Search open-source projects"}
  ]
}
```

**Requerido:** `parallel` (al menos un sub-paso)

Los resultados individuales de cada sub-paso pueden referenciarse mediante `{{steps.search-papers.output}}`.

### handoff

Pasa la salida de un paso a otro agente para su procesamiento posterior. La salida completa del paso de origen se convierte en el contexto del agente receptor.

```json
{
  "id": "review",
  "type": "handoff",
  "agent": "ruri",
  "handoffFrom": "draft",
  "prompt": "Review and revise the article",
  "dependsOn": ["draft"]
}
```

**Requerido:** `handoffFrom`, `agent`
**Opcional:** `prompt` (instrucción para el agente receptor)

### tool_call

Invoca una herramienta registrada en el registro de herramientas.

```json
{
  "id": "fetch-data",
  "type": "tool_call",
  "toolName": "http-get",
  "toolInput": {
    "url": "https://api.example.com/data?q={{topic}}"
  }
}
```

**Requerido:** `toolName`
**Opcional:** `toolInput` (admite expansión `{{var}}`)

### delay

Espera una duración determinada antes de continuar.

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**Requerido:** `delay` (formato de duración de Go: `"30s"`, `"5m"`, `"1h"`)

### notify

Envía un mensaje de notificación. El mensaje se publica como un evento SSE (type=`workflow_notify`) para que los consumidores externos puedan disparar acciones en Telegram, Slack, etc.

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**Requerido:** `notifyMsg`
**Opcional:** `notifyTo` (canal sugerido)

## Variables y Plantillas

Los workflows admiten la sintaxis de plantillas `{{}}`, que se expande antes de la ejecución de cada paso.

### Variables de Entrada

```
{{varName}}
```

Se resuelven desde los valores por defecto de `variables` o desde las sobreescrituras `--var key=value`.

### Resultados de Pasos

```
{{steps.ID.output}}    — Texto de salida del paso
{{steps.ID.status}}    — Estado del paso (success/error/skipped/timeout)
{{steps.ID.error}}     — Mensaje de error del paso
```

### Variables de Entorno

```
{{env.KEY}}            — Variable de entorno del sistema
```

### Ejemplo

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## Dependencias y Control de Flujo

### dependsOn — Dependencias DAG

Usa `dependsOn` para definir el orden de ejecución. El sistema ordena automáticamente los pasos como un DAG (Grafo Acíclico Dirigido).

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c` espera a que tanto `step-a` como `step-b` finalicen
- Los pasos sin `dependsOn` comienzan de inmediato (posiblemente en paralelo)
- Las dependencias circulares se detectan y se rechazan

### Ramificación Condicional

Los campos `then`/`else` de un paso `condition` determinan qué pasos descendentes se ejecutan:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

El paso de la rama no elegida se marca como `skipped`. Los pasos descendentes siguen evaluándose normalmente según su `dependsOn`.

## Manejo de Errores

### Estrategias de onError

Cada paso puede configurar `onError`:

| Valor | Comportamiento |
|-------|----------------|
| `"stop"` | **Por defecto** — Aborta el workflow ante un fallo; los pasos restantes se marcan como omitidos |
| `"skip"` | Marca el paso fallido como omitido y continúa |
| `"retry"` | Reintenta según `retryMax` + `retryDelay`; si todos los reintentos fallan, se trata como error |

### Configuración de Reintentos

```json
{
  "id": "flaky-step",
  "agent": "hisui",
  "prompt": "...",
  "onError": "retry",
  "retryMax": 3,
  "retryDelay": "10s"
}
```

- `retryMax`: Número máximo de reintentos (sin contar el primer intento)
- `retryDelay`: Tiempo entre reintentos, por defecto 5 segundos
- Solo tiene efecto cuando `onError: "retry"`

## Triggers

Los triggers permiten la ejecución automática de workflows. Configúralos en `config.json` bajo el array `workflowTriggers`.

### Estructura de WorkflowTriggerConfig

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `name` | string | Nombre del trigger |
| `workflowName` | string | Workflow a ejecutar |
| `enabled` | bool | Si está habilitado (por defecto: true) |
| `trigger` | TriggerSpec | Condición del trigger |
| `variables` | map[string]string | Sobreescrituras de variables para el workflow |
| `cooldown` | string | Período de enfriamiento (ej. `"5m"`, `"1h"`) |

### Estructura de TriggerSpec

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `type` | string | `"cron"`, `"event"` o `"webhook"` |
| `cron` | string | Expresión cron (5 campos: min hora día mes díasemana) |
| `tz` | string | Zona horaria (ej. `"Asia/Taipei"`), solo para cron |
| `event` | string | Tipo de evento SSE, admite comodín con sufijo `*` (ej. `"deploy_*"`) |
| `webhook` | string | Sufijo de la ruta del webhook |

### Triggers Cron

Se comprueban cada 30 segundos y se disparan como máximo una vez por minuto (con deduplicación).

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Triggers de Evento

Escucha en el canal SSE `_triggers` y compara tipos de evento. Admite comodín con sufijo `*`.

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Los triggers de evento inyectan automáticamente variables adicionales: `event_type`, `task_id`, `session_id`, más los campos de datos del evento (con prefijo `event_`).

### Triggers Webhook

Se disparan mediante HTTP POST:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

Uso:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

Los pares clave-valor JSON del cuerpo del POST se inyectan como variables adicionales del workflow.

### Cooldown

Todos los triggers admiten `cooldown` para evitar disparos repetidos en un período corto. Los triggers que ocurren durante el cooldown se ignoran silenciosamente.

### Meta-Variables de Trigger

El sistema inyecta automáticamente estas variables en cada disparo:

- `_trigger_name` — Nombre del trigger
- `_trigger_type` — Tipo de trigger (cron/event/webhook)
- `_trigger_time` — Hora del disparo (RFC3339)

## Modos de Ejecución

### live (por defecto)

Ejecución completa: llama a los LLMs, registra el historial y publica eventos SSE.

```bash
tetora workflow run my-workflow
```

### dry-run

Sin llamadas a LLM; estima el costo de cada paso. Los pasos de condición se evalúan normalmente; los pasos dispatch/skill/handoff devuelven estimaciones de costo.

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

Ejecuta las llamadas a LLM normalmente pero no registra en el historial de tareas ni en los logs de sesión. Útil para pruebas.

```bash
tetora workflow run my-workflow --shadow
```

## Referencia de CLI

```
tetora workflow <command> [options]
```

| Comando | Descripción |
|---------|-------------|
| `list` | Listar todos los workflows guardados |
| `show <name>` | Mostrar la definición del workflow (resumen + JSON) |
| `validate <name\|file>` | Validar un workflow (acepta nombre o ruta de archivo JSON) |
| `create <file>` | Importar workflow desde un archivo JSON (valida primero) |
| `delete <name>` | Eliminar un workflow |
| `run <name> [flags]` | Ejecutar un workflow |
| `runs [name]` | Listar historial de ejecuciones (opcionalmente filtrar por nombre) |
| `status <run-id>` | Mostrar estado detallado de una ejecución (salida JSON) |
| `messages <run-id>` | Mostrar mensajes de agente y registros de handoff de una ejecución |
| `history <name>` | Mostrar historial de versiones del workflow |
| `rollback <name> <version-id>` | Restaurar a una versión específica |
| `diff <version1> <version2>` | Comparar dos versiones |

### Flags del Comando run

| Flag | Descripción |
|------|-------------|
| `--var key=value` | Sobreescribir una variable del workflow (se puede usar múltiples veces) |
| `--dry-run` | Modo dry-run (sin llamadas a LLM) |
| `--shadow` | Modo shadow (sin registro en historial) |

### Alias

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## Referencia de la API HTTP

### CRUD de Workflows

| Método | Ruta | Descripción |
|--------|------|-------------|
| GET | `/workflows` | Listar todos los workflows |
| POST | `/workflows` | Crear un workflow (cuerpo: JSON del Workflow) |
| GET | `/workflows/{name}` | Obtener la definición de un workflow |
| DELETE | `/workflows/{name}` | Eliminar un workflow |
| POST | `/workflows/{name}/validate` | Validar un workflow |
| POST | `/workflows/{name}/run` | Ejecutar un workflow (asíncrono, devuelve `202 Accepted`) |
| GET | `/workflows/{name}/runs` | Obtener el historial de ejecuciones de un workflow |

#### Cuerpo de POST /workflows/{name}/run

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### Ejecuciones de Workflows

| Método | Ruta | Descripción |
|--------|------|-------------|
| GET | `/workflow-runs` | Listar todos los registros de ejecución (añade `?workflow=name` para filtrar) |
| GET | `/workflow-runs/{id}` | Obtener detalles de una ejecución (incluye handoffs + mensajes de agente) |

### Triggers

| Método | Ruta | Descripción |
|--------|------|-------------|
| GET | `/api/triggers` | Listar el estado de todos los triggers |
| POST | `/api/triggers/{name}/fire` | Disparar manualmente un trigger |
| GET | `/api/triggers/{name}/runs` | Ver historial de ejecuciones del trigger (añade `?limit=N`) |
| POST | `/api/triggers/webhook/{id}` | Trigger webhook (cuerpo: variables JSON clave-valor) |

## Gestión de Versiones

Cada operación `create` o modificación crea automáticamente un snapshot de versión.

```bash
# Ver historial de versiones
tetora workflow history my-workflow

# Restaurar a una versión específica
tetora workflow rollback my-workflow <version-id>

# Comparar dos versiones
tetora workflow diff <version-id-1> <version-id-2>
```

## Reglas de Validación

El sistema valida antes de `create` y de `run`:

- `name` es requerido; solo se permiten alfanuméricos, `-` y `_`
- Se requiere al menos un paso
- Los IDs de paso deben ser únicos
- Las referencias en `dependsOn` deben apuntar a IDs de paso existentes
- Los pasos no pueden depender de sí mismos
- Las dependencias circulares se rechazan (detección de ciclos en el DAG)
- Campos requeridos por tipo de paso (ej. dispatch necesita `prompt`, condition necesita `if` + `then`)
- `timeout`, `retryDelay` y `delay` deben estar en formato de duración de Go válido
- `onError` solo acepta `stop`, `skip`, `retry`
- `then`/`else` en condition deben referenciar IDs de paso existentes
- `handoffFrom` en handoff debe referenciar un ID de paso existente
