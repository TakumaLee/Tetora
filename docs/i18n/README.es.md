<p align="center">
  <img src="assets/banner.png" alt="Tetora — Orquestador de Agentes IA" width="800">
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Bahasa Indonesia](README.id.md) | [ภาษาไทย](README.th.md) | [Filipino](README.fil.md) | **Español** | [Français](README.fr.md) | [Deutsch](README.de.md)

<p align="center">
  <strong>Plataforma de asistente IA autoalojada con arquitectura multi-agente.</strong>
</p>

Tetora se ejecuta como un solo binario de Go sin dependencias externas. Se conecta a los proveedores de IA que ya utilizas, se integra con las plataformas de mensajería en las que trabaja tu equipo y mantiene todos los datos en tu propio hardware.

---

## Qué es Tetora

Tetora es un orquestador de agentes IA que te permite definir múltiples roles de agente -- cada uno con su propia personalidad, prompt de sistema, modelo y acceso a herramientas -- e interactuar con ellos a través de plataformas de chat, APIs HTTP o la línea de comandos.

**Capacidades principales:**

- **Roles multi-agente** -- define agentes distintos con personalidades, presupuestos y permisos de herramientas separados
- **Multi-proveedor** -- Claude API, OpenAI, Gemini y más; intercambia o combina libremente
- **Multi-plataforma** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **Cron jobs** -- programa tareas recurrentes con puertas de aprobación y notificaciones
- **Base de conocimiento** -- alimenta documentos a los agentes para respuestas fundamentadas
- **Memoria persistente** -- los agentes recuerdan el contexto entre sesiones; capa de memoria unificada con consolidación
- **Soporte MCP** -- conecta servidores Model Context Protocol como proveedores de herramientas
- **Skills y workflows** -- paquetes de habilidades componibles y pipelines de flujo de trabajo multi-paso
- **Panel web** -- centro de mando CEO con métricas ROI, oficina de píxeles y feed de actividad en vivo
- **Motor de workflows** -- ejecución de pipeline basada en DAG con ramas condicionales, pasos paralelos, lógica de reintentos y enrutamiento dinámico de modelos (Sonnet para tareas rutinarias, Opus para complejas)
- **Marketplace de plantillas** -- pestaña Store para explorar, importar y exportar plantillas de workflow
- **Despacho automático en tablero** -- tablero Kanban con asignación automática de tareas, slots concurrentes configurables y sistema de presión de slots que reserva capacidad para sesiones interactivas
- **GitLab MR + GitHub PR** -- creación automática de PR/MR tras completar workflows; detección automática del host remoto
- **Compactación de sesiones** -- compresión automática de contexto basada en tokens y conteo de mensajes para mantener sesiones dentro de los límites del modelo
- **Service Worker PWA** -- panel con capacidad offline y caché inteligente
- **Estado parcialmente completado** -- las tareas que completan pero fallan en el post-procesamiento (git merge, revisión) entran en un estado intermedio recuperable en lugar de perderse
- **Webhooks** -- activa acciones de agentes desde sistemas externos
- **Gobernanza de costos** -- presupuestos por rol y globales con degradación automática de modelo
- **Retención de datos** -- políticas de limpieza configurables por tabla, con exportación y purga completas
- **Plugins** -- extiende la funcionalidad mediante procesos de plugins externos
- **Recordatorios inteligentes, hábitos, metas, contactos, seguimiento financiero, briefings y más**

---

## Inicio Rápido

### Para ingenieros

```bash
# Instalar la última versión
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# Ejecutar el asistente de configuración
tetora init

# Verificar que todo esté configurado correctamente
tetora doctor

# Iniciar el daemon
tetora serve
```

### Para no ingenieros

1. Ve a la [página de Releases](https://github.com/TakumaLee/Tetora/releases/latest)
2. Descarga el binario para tu plataforma (p. ej. `tetora-darwin-arm64` para Mac con Apple Silicon)
3. Muévelo a un directorio en tu PATH y renómbralo a `tetora`, o colócalo en `~/.tetora/bin/`
4. Abre una terminal y ejecuta:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## Agentes

Cada agente de Tetora es más que un chatbot -- tiene una identidad. Cada agente (llamado **rol**) se define mediante un **archivo de alma (soul file)**: un documento Markdown que otorga al agente su personalidad, experiencia, estilo de comunicación y pautas de comportamiento.

### Definir un rol

Los roles se declaran en `config.json` bajo la clave `roles`:

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

### Archivos de alma (Soul files)

Un archivo de alma le dice al agente *quién es*. Colócalo en el directorio de workspace (`~/.tetora/workspace/` por defecto):

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

### Primeros pasos

`tetora init` te guía para crear tu primer rol y genera automáticamente un archivo de alma inicial. Puedes editarlo en cualquier momento -- los cambios surten efecto en la próxima sesión.

---

## Panel de Control

Tetora incluye un panel web integrado en `http://localhost:8991/dashboard`. Está organizado en cuatro zonas:

| Zona | Contenido |
|------|----------|
| **Centro de Mando** | Resumen ejecutivo (tarjetas ROI), sprites de equipo en píxeles, oficina Agent World expandible |
| **Operaciones** | Barra de ops compacta, scorecard de agentes + feed de actividad en vivo (lado a lado), tareas en ejecución |
| **Perspectivas** | Gráfico de tendencias de 7 días, gráficos históricos de rendimiento y costos de tareas |
| **Detalles de Ingeniería** | Panel de costos, cron jobs, sesiones, salud del proveedor, confianza, SLA, historial de versiones, enrutamiento, memoria y más (plegable) |

El editor de agentes incluye un **selector de modelos con reconocimiento de proveedor** con cambio de un clic entre modelos en la nube y locales (Ollama). Un **interruptor global de modo de inferencia** permite cambiar todos los agentes entre nube y local con un solo botón. Cada tarjeta de agente muestra una insignia Cloud/Local y un menú desplegable de cambio rápido.

Hay múltiples temas disponibles (Glass, Clean, Material, Boardroom, Retro). La oficina de píxeles Agent World se puede personalizar con decoraciones y controles de zoom.

```bash
# Abrir el panel en tu navegador predeterminado
tetora dashboard
```

---

## Comandos de Discord

Tetora responde a comandos con prefijo `!` en Discord:

| Comando | Descripción |
|---------|-------------|
| `!model` | Mostrar todos los agentes agrupados por Cloud / Local |
| `!model pick [agent]` | Selector de modelo interactivo (botones + desplegables) |
| `!model <model> [agent]` | Establecer modelo directamente (detección automática del proveedor) |
| `!local [agent]` | Cambiar a modelos locales (Ollama) |
| `!cloud [agent]` | Restaurar modelos en la nube |
| `!mode` | Resumen del modo de inferencia con botones de alternancia |
| `!chat <agent>` | Bloquear canal a un agente específico |
| `!end` | Desbloquear canal, reanudar despacho inteligente |
| `!new` | Iniciar nueva sesión |
| `!ask <prompt>` | Pregunta única |
| `!cancel` | Cancelar todas las tareas en ejecución |
| `!approve [tool\|reset]` | Gestionar herramientas de aprobación automática |
| `!status` / `!cost` / `!jobs` | Resumen de operaciones |
| `!help` | Mostrar referencia de comandos |
| `@Tetora <text>` | Despacho inteligente al mejor agente |

**[Referencia Completa de Comandos de Discord](docs/discord-commands.md)** -- cambio de modelos, alternancia remoto/local, configuración de proveedores y más.

---

## Compilar desde el Código Fuente

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

Esto compila el binario y lo instala en `~/.tetora/bin/tetora`. Asegúrate de que `~/.tetora/bin` esté en tu `PATH`.

Para ejecutar las pruebas:

```bash
make test
```

---

## Requisitos

| Requisito | Detalles |
|---|---|
| **sqlite3** | Debe estar disponible en el `PATH`. Se utiliza para todo el almacenamiento persistente. |
| **Clave API de proveedor IA** | Al menos una: Claude API, OpenAI, Gemini o cualquier endpoint compatible con OpenAI. |
| **Go 1.25+** | Solo necesario si compilas desde el código fuente. |

---

## Plataformas Soportadas

| Plataforma | Arquitecturas | Estado |
|---|---|---|
| macOS | amd64, arm64 | Estable |
| Linux | amd64, arm64 | Estable |
| Windows | amd64 | Beta |

---

## Arquitectura

Todos los datos de ejecución se almacenan en `~/.tetora/`:

```
~/.tetora/
  config.json        Configuración principal (proveedores, roles, integraciones)
  jobs.json          Definiciones de cron jobs
  history.db         Base de datos SQLite (historial, memoria, sesiones, embeddings, ...)
  bin/               Binario instalado
  agents/            Archivos de alma por agente (agents/{name}/SOUL.md)
  workspace/
    rules/           Reglas de gobernanza, auto-inyectadas en todos los prompts de agentes
    memory/          Observaciones compartidas, lectura/escritura por cualquier agente
    knowledge/       Material de referencia (auto-inyectado hasta 50 KB)
    skills/          Procedimientos reutilizables, cargados por coincidencia de prompts
    tasks/           Archivos de tareas y listas de pendientes
  runtime/
    sessions/        Archivos de sesión por agente
    outputs/         Archivos de salida generados
    logs/            Archivos de log estructurados
    cache/           Caché temporal
```

La configuración utiliza JSON plano con soporte para referencias `$ENV_VAR`, para que los secretos nunca necesiten estar codificados directamente. El asistente de configuración (`tetora init`) genera un `config.json` funcional de forma interactiva.

Se soporta la recarga en caliente: envía `SIGHUP` al daemon en ejecución para recargar `config.json` sin tiempo de inactividad.

---

## Workflows

Tetora incluye un motor de workflows integrado para orquestar tareas de múltiples pasos y múltiples agentes. Define tu pipeline en JSON y deja que los agentes colaboren automáticamente.

**[Documentación Completa de Workflows](docs/workflow.es.md)** — tipos de pasos, variables, disparadores, referencia de CLI y API.

Ejemplo rápido:

```bash
# Validar e importar un workflow
tetora workflow create examples/workflow-basic.json

# Ejecutarlo
tetora workflow run research-and-summarize --var topic="LLM safety"

# Consultar los resultados
tetora workflow status <run-id>
```

Consulta [`examples/`](examples/) para archivos JSON de workflow listos para usar.

---

## Referencia de CLI

| Comando | Descripción |
|---|---|
| `tetora init` | Asistente de configuración interactivo |
| `tetora doctor` | Verificaciones de salud y diagnósticos |
| `tetora serve` | Iniciar daemon (chat bots + HTTP API + cron) |
| `tetora run --file tasks.json` | Despachar tareas desde un archivo JSON (modo CLI) |
| `tetora dispatch "Summarize this"` | Ejecutar una tarea ad-hoc a través del daemon |
| `tetora route "Review code security"` | Despacho inteligente -- enruta automáticamente al mejor rol |
| `tetora status` | Resumen rápido del daemon, jobs y costos |
| `tetora job list` | Listar todos los cron jobs |
| `tetora job trigger <name>` | Activar manualmente un cron job |
| `tetora role list` | Listar todos los roles configurados |
| `tetora role show <name>` | Mostrar detalles del rol y vista previa del alma |
| `tetora history list` | Mostrar historial de ejecución reciente |
| `tetora history cost` | Mostrar resumen de costos |
| `tetora session list` | Listar sesiones recientes |
| `tetora memory list` | Listar entradas de memoria del agente |
| `tetora knowledge list` | Listar documentos de la base de conocimiento |
| `tetora skill list` | Listar skills disponibles |
| `tetora workflow list` | Listar workflows configurados |
| `tetora workflow run <name>` | Ejecutar un workflow (pasar `--var key=value` para variables) |
| `tetora workflow status <run-id>` | Mostrar el estado de una ejecución de workflow |
| `tetora workflow export <name>` | Exportar un workflow a un archivo JSON compartible |
| `tetora workflow create <file>` | Validar e importar un workflow desde un archivo JSON |
| `tetora mcp list` | Listar conexiones de servidores MCP |
| `tetora budget show` | Mostrar estado del presupuesto |
| `tetora config show` | Mostrar configuración actual |
| `tetora config validate` | Validar config.json |
| `tetora backup` | Crear un archivo de respaldo |
| `tetora restore <file>` | Restaurar desde un archivo de respaldo |
| `tetora dashboard` | Abrir el panel web en un navegador |
| `tetora logs` | Ver logs del daemon (`-f` para seguir, `--json` para salida estructurada) |
| `tetora health` | Salud en tiempo de ejecución (daemon, workers, tablero de tareas, disco) |
| `tetora drain` | Apagado elegante: detener nuevas tareas, esperar agentes en ejecución |
| `tetora data status` | Mostrar estado de retención de datos |
| `tetora security scan` | Escaneo de seguridad y línea base |
| `tetora prompt list` | Gestionar plantillas de prompts |
| `tetora project add` | Añadir un proyecto al workspace |
| `tetora guide` | Guía de incorporación interactiva |
| `tetora upgrade` | Actualizar a la última versión |
| `tetora service install` | Instalar como servicio launchd (macOS) |
| `tetora completion <shell>` | Generar completados de shell (bash, zsh, fish) |
| `tetora version` | Mostrar versión |

Ejecuta `tetora help` para la referencia completa de comandos.

---

## Contribuir

Las contribuciones son bienvenidas. Por favor abre un issue para discutir cambios mayores antes de enviar un pull request.

- **Issues**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **Discusiones**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

Este proyecto está licenciado bajo AGPL-3.0, que requiere que las obras derivadas y los despliegues accesibles por red también sean de código abierto bajo la misma licencia. Por favor revisa la licencia antes de contribuir.

---

## Licencia

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Tetora contributors.
