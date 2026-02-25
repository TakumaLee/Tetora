# Instalar Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.zh-CN.md">简体中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <strong>Español</strong> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## Requisitos

| Requisito | Detalles |
|---|---|
| **Sistema operativo** | macOS, Linux o Windows (WSL) |
| **Terminal** | Cualquier emulador de terminal |
| **sqlite3** | Debe estar disponible en el `PATH` |
| **Proveedor de IA** | Ver Ruta 1 o Ruta 2 abajo |

### Instalar sqlite3

**macOS:**
```bash
brew install sqlite3
```

**Ubuntu / Debian:**
```bash
sudo apt install sqlite3
```

**Fedora / RHEL:**
```bash
sudo dnf install sqlite
```

**Windows (WSL):** Instale dentro de su distribución WSL usando los comandos de Linux anteriores.

---

## Descargar Tetora

Vaya a la [página de Releases](https://github.com/TakumaLee/Tetora/releases/latest) y descargue el binario para su plataforma:

| Plataforma | Archivo |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Use el binario Linux dentro de WSL |

**Instalar el binario:**
```bash
# Reemplace el nombre del archivo con el que descargó
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Asegúrese de que ~/.tetora/bin esté en su PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # o ~/.bashrc
source ~/.zshrc
```

**O use el instalador de una línea (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Ruta 1: Claude Pro ($20/mes) — Recomendado para principiantes

Esta ruta usa **Claude Code CLI** como backend de IA. Requiere una suscripción activa a Claude Pro ($20/mes en [claude.ai](https://claude.ai)).

> **¿Por qué esta ruta?** Sin claves API que gestionar, sin sorpresas en la facturación. Su suscripción Pro cubre todo el uso de Tetora a través de Claude Code.

### Paso 1: Instalar Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

Si no tiene Node.js instalado:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)

Verificar la instalación:
```bash
claude --version
```

Iniciar sesión con su cuenta Claude Pro:
```bash
claude
# Siga el flujo de inicio de sesión en el navegador
```

### Paso 2: Ejecutar tetora init

```bash
tetora init
```

El asistente de configuración le guiará a través de:
1. **Elegir un idioma** — seleccione su idioma preferido
2. **Elegir un canal de mensajería** — Telegram, Discord, Slack o Ninguno
3. **Elegir un proveedor de IA** — seleccione **"Claude Code CLI"**
   - El asistente detecta automáticamente la ubicación de su binario `claude`
   - Presione Enter para aceptar la ruta detectada
4. **Elegir acceso a directorios** — qué carpetas puede leer/escribir Tetora
5. **Crear su primer rol de agente** — asígnele un nombre y una personalidad

### Paso 3: Verificar e iniciar

```bash
# Verificar que todo está correctamente configurado
tetora doctor

# Iniciar el daemon
tetora serve
```

Abrir el panel web:
```bash
tetora dashboard
```

---

## Ruta 2: Clave API

Esta ruta usa una clave API directa. Proveedores soportados:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Cualquier endpoint compatible con OpenAI** — Ollama, LM Studio, Azure OpenAI, etc.

> **Nota sobre costos:** El uso de la API se factura por token. Verifique los precios de su proveedor antes de activar.

### Paso 1: Obtener su clave API

**Claude API:**
1. Vaya a [console.anthropic.com](https://console.anthropic.com)
2. Cree una cuenta o inicie sesión
3. Navegue a **API Keys** → **Create Key**
4. Copie la clave (comienza con `sk-ant-...`)

**OpenAI:**
1. Vaya a [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Haga clic en **Create new secret key**
3. Copie la clave (comienza con `sk-...`)

**Endpoint compatible con OpenAI (ej. Ollama):**
```bash
# Iniciar un servidor Ollama local
ollama serve
# Endpoint por defecto: http://localhost:11434/v1
# No se necesita clave API para modelos locales
```

### Paso 2: Ejecutar tetora init

```bash
tetora init
```

El asistente le guiará:
1. **Elegir un idioma**
2. **Elegir un canal de mensajería**
3. **Elegir un proveedor de IA:**
   - Seleccione **"Claude API Key"** para Anthropic Claude
   - Seleccione **"Endpoint compatible con OpenAI"** para OpenAI o modelos locales
4. **Ingresar su clave API** (o URL de endpoint para modelos locales)
5. **Elegir acceso a directorios**
6. **Crear su primer rol de agente**

### Paso 3: Verificar e iniciar

```bash
tetora doctor
tetora serve
```

---

## Asistente de configuración web (para no ingenieros)

Si prefiere una interfaz gráfica, use el asistente web:

```bash
tetora setup --web
```

Esto abre una ventana del navegador en `http://localhost:7474` con un asistente de 4 pasos.

---

## Comandos útiles después de la instalación

| Comando | Descripción |
|---|---|
| `tetora doctor` | Comprobaciones de salud — ejecutar si algo falla |
| `tetora serve` | Iniciar el daemon (bots + API HTTP + trabajos programados) |
| `tetora dashboard` | Abrir el panel web |
| `tetora status` | Vista rápida del estado |
| `tetora init` | Volver a ejecutar el asistente de configuración |

---

## Solución de problemas

### `tetora: command not found`

Asegúrese de que `~/.tetora/bin` esté en su PATH:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Instale sqlite3 para su plataforma (ver Requisitos arriba).

### `tetora doctor` reporta errores de proveedor

- **Ruta de Claude Code CLI:** Ejecute `which claude` y actualice `claudePath` en `~/.tetora/config.json`
- **Clave API inválida:** Verifique su clave en la consola de su proveedor
- **Modelo no encontrado:** Verifique que el nombre del modelo coincida con su nivel de suscripción

### Problemas de inicio de sesión en Claude Code

```bash
claude logout
claude
```

---

## Compilar desde el código fuente

Requiere Go 1.25+:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## Próximos pasos

- Lea el [README](README.es.md) para la documentación completa de características
- Comunidad: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Reportar problemas: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
