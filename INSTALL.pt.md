# Instalar Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.zh-CN.md">简体中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.es.md">Español</a> | <strong>Português</strong> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## Requisitos

| Requisito | Detalhes |
|---|---|
| **Sistema operacional** | macOS, Linux ou Windows (WSL) |
| **Terminal** | Qualquer emulador de terminal |
| **sqlite3** | Deve estar disponível no `PATH` |
| **Provedor de IA** | Ver Caminho 1 ou Caminho 2 abaixo |

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

**Windows (WSL):** Instale dentro da sua distribuição WSL usando os comandos Linux acima.

---

## Baixar Tetora

Acesse a [página de Releases](https://github.com/TakumaLee/Tetora/releases/latest) e baixe o binário para sua plataforma:

| Plataforma | Arquivo |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Use o binário Linux dentro do WSL |

**Instalar o binário:**
```bash
# Substitua o nome do arquivo pelo que você baixou
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Certifique-se de que ~/.tetora/bin está no seu PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # ou ~/.bashrc
source ~/.zshrc
```

**Ou use o instalador de uma linha (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Caminho 1: Claude Pro ($20/mês) — Recomendado para iniciantes

Este caminho usa o **Claude Code CLI** como backend de IA. Requer uma assinatura ativa do Claude Pro ($20/mês em [claude.ai](https://claude.ai)).

> **Por que este caminho?** Sem chaves de API para gerenciar, sem surpresas de cobrança. Sua assinatura Pro cobre todo o uso do Tetora via Claude Code.

### Passo 1: Instalar o Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

Se você não tem Node.js instalado:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)

Verificar a instalação:
```bash
claude --version
```

Fazer login com sua conta Claude Pro:
```bash
claude
# Siga o fluxo de login no navegador
```

### Passo 2: Executar tetora init

```bash
tetora init
```

O assistente de configuração irá guiá-lo por:
1. **Escolher um idioma** — selecione seu idioma preferido
2. **Escolher um canal de mensagens** — Telegram, Discord, Slack ou Nenhum
3. **Escolher um provedor de IA** — selecione **"Claude Code CLI"**
   - O assistente detecta automaticamente a localização do seu binário `claude`
   - Pressione Enter para aceitar o caminho detectado
4. **Escolher acesso a diretórios** — quais pastas o Tetora pode ler/escrever
5. **Criar seu primeiro papel de agente** — dê a ele um nome e personalidade

### Passo 3: Verificar e iniciar

```bash
# Verificar se tudo está configurado corretamente
tetora doctor

# Iniciar o daemon
tetora serve
```

Abrir o painel web:
```bash
tetora dashboard
```

---

## Caminho 2: Chave de API

Este caminho usa uma chave de API direta. Provedores suportados:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Qualquer endpoint compatível com OpenAI** — Ollama, LM Studio, Azure OpenAI, etc.

> **Nota sobre custos:** O uso da API é cobrado por token. Verifique os preços do seu provedor antes de ativar.

### Passo 1: Obter sua chave de API

**Claude API:**
1. Acesse [console.anthropic.com](https://console.anthropic.com)
2. Crie uma conta ou faça login
3. Navegue para **API Keys** → **Create Key**
4. Copie a chave (começa com `sk-ant-...`)

**OpenAI:**
1. Acesse [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Clique em **Create new secret key**
3. Copie a chave (começa com `sk-...`)

**Endpoint compatível com OpenAI (ex. Ollama):**
```bash
# Iniciar um servidor Ollama local
ollama serve
# Endpoint padrão: http://localhost:11434/v1
# Nenhuma chave de API necessária para modelos locais
```

### Passo 2: Executar tetora init

```bash
tetora init
```

O assistente irá guiá-lo:
1. **Escolher um idioma**
2. **Escolher um canal de mensagens**
3. **Escolher um provedor de IA:**
   - Selecione **"Claude API Key"** para Anthropic Claude
   - Selecione **"Endpoint compatível com OpenAI"** para OpenAI ou modelos locais
4. **Inserir sua chave de API** (ou URL do endpoint para modelos locais)
5. **Escolher acesso a diretórios**
6. **Criar seu primeiro papel de agente**

### Passo 3: Verificar e iniciar

```bash
tetora doctor
tetora serve
```

---

## Assistente de configuração web (para não-engenheiros)

Se você prefere uma interface gráfica, use o assistente web:

```bash
tetora setup --web
```

Isso abre uma janela do navegador em `http://localhost:7474` com um assistente de 4 etapas.

---

## Comandos úteis após a instalação

| Comando | Descrição |
|---|---|
| `tetora doctor` | Verificações de saúde — execute se algo parecer errado |
| `tetora serve` | Iniciar o daemon (bots + API HTTP + tarefas agendadas) |
| `tetora dashboard` | Abrir o painel web |
| `tetora status` | Visão geral rápida do status |
| `tetora init` | Executar novamente o assistente de configuração |

---

## Solução de problemas

### `tetora: command not found`

Certifique-se de que `~/.tetora/bin` está no seu PATH:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Instale sqlite3 para sua plataforma (ver Requisitos acima).

### `tetora doctor` reporta erros de provedor

- **Caminho do Claude Code CLI:** Execute `which claude` e atualize `claudePath` em `~/.tetora/config.json`
- **Chave de API inválida:** Verifique sua chave no console do seu provedor
- **Modelo não encontrado:** Verifique se o nome do modelo corresponde ao seu nível de assinatura

### Problemas de login no Claude Code

```bash
claude logout
claude
```

---

## Compilar a partir do código-fonte

Requer Go 1.25+:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## Próximos passos

- Leia o [README](README.md) para documentação completa de recursos
- Comunidade: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Reportar problemas: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
