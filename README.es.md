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
  sessions/          Archivos de sesión por agente
  knowledge/         Documentos de la base de conocimiento
  logs/              Archivos de log estructurados
  outputs/           Archivos de salida generados
  uploads/           Almacenamiento temporal de uploads
  bin/               Binario instalado
```

La configuración utiliza JSON plano con soporte para referencias `$ENV_VAR`, para que los secretos nunca necesiten estar codificados directamente. El asistente de configuración (`tetora init`) genera un `config.json` funcional de forma interactiva.

Se soporta la recarga en caliente: envía `SIGHUP` al daemon en ejecución para recargar `config.json` sin tiempo de inactividad.

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
| `tetora mcp list` | Listar conexiones de servidores MCP |
| `tetora budget show` | Mostrar estado del presupuesto |
| `tetora config show` | Mostrar configuración actual |
| `tetora config validate` | Validar config.json |
| `tetora backup` | Crear un archivo de respaldo |
| `tetora restore <file>` | Restaurar desde un archivo de respaldo |
| `tetora dashboard` | Abrir el panel web en un navegador |
| `tetora logs` | Ver logs del daemon (`-f` para seguir, `--json` para salida estructurada) |
| `tetora data status` | Mostrar estado de retención de datos |
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
