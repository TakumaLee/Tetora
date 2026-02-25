# 安装 Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <strong>简体中文</strong> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## 系统要求

| 要求 | 说明 |
|---|---|
| **操作系统** | macOS、Linux 或 Windows（WSL） |
| **终端** | 任意终端均可 |
| **sqlite3** | 需在 `PATH` 中可执行 |
| **AI 提供商** | 见下方路径 1 或路径 2 |

### 安装 sqlite3

**macOS：**
```bash
brew install sqlite3
```

**Ubuntu / Debian：**
```bash
sudo apt install sqlite3
```

**Fedora / RHEL：**
```bash
sudo dnf install sqlite
```

**Windows（WSL）：** 在 WSL 发行版中使用上方 Linux 命令安装。

---

## 下载 Tetora

前往 [Releases 页面](https://github.com/TakumaLee/Tetora/releases/latest) 下载对应平台的可执行文件：

| 平台 | 文件名 |
|---|---|
| macOS（Apple Silicon） | `tetora-darwin-arm64` |
| macOS（Intel） | `tetora-darwin-amd64` |
| Linux（x86_64） | `tetora-linux-amd64` |
| Linux（ARM64） | `tetora-linux-arm64` |
| Windows（WSL） | 在 WSL 中使用 Linux 版本 |

**安装可执行文件：**
```bash
# 将文件名替换为你下载的版本
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# 确认 ~/.tetora/bin 已加入 PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # 或 ~/.bashrc
source ~/.zshrc
```

**或使用一键安装命令（macOS / Linux）：**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## 路径 1：Claude Pro（月费 $20）— 新手推荐

此路径使用 **Claude Code CLI** 作为 AI 后端，需要有效的 Claude Pro 订阅（月费 $20，[claude.ai](https://claude.ai)）。

> **为何选择此路径？** 无需管理 API 密钥，不用担心用量计费。Pro 订阅涵盖通过 Claude Code 使用 Tetora 的全部费用。

### 步骤 1：安装 Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

如果尚未安装 Node.js：
- **macOS：** `brew install node`
- **Linux：** `sudo apt install nodejs npm`（Ubuntu/Debian）

确认安装成功：
```bash
claude --version
```

使用 Claude Pro 账号登录：
```bash
claude
# 按照浏览器登录流程操作
```

### 步骤 2：运行 tetora init

```bash
tetora init
```

设置向导将引导你完成以下步骤：
1. **选择语言** — 选择你偏好的语言
2. **选择消息频道** — Telegram、Discord、Slack 或无（仅 HTTP API）
3. **选择 AI 提供商** — 选择 **「Claude Code CLI」**
   - 向导会自动检测 `claude` 可执行文件位置
   - 按 Enter 接受检测到的路径
4. **选择目录访问权限** — Tetora 可读写的文件夹
5. **创建第一个 Agent 角色** — 设置名称和个性

### 步骤 3：验证并启动

```bash
# 确认配置正确
tetora doctor

# 启动守护进程
tetora serve
```

打开网页仪表板：
```bash
tetora dashboard
```

---

## 路径 2：API 密钥

此路径直接使用 API 密钥，支持以下提供商：

- **Claude API**（Anthropic）— [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **任何 OpenAI 兼容端点** — Ollama、LM Studio、Azure OpenAI 等

> **费用提示：** API 使用量按 token 计费，启用前请确认提供商的定价方式。

### 步骤 1：获取 API 密钥

**Claude API：**
1. 前往 [console.anthropic.com](https://console.anthropic.com)
2. 创建账号或登录
3. 前往 **API Keys** → **Create Key**
4. 复制密钥（格式：`sk-ant-...`）

**OpenAI：**
1. 前往 [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. 点击 **Create new secret key**
3. 复制密钥（格式：`sk-...`）

**OpenAI 兼容端点（如 Ollama）：**
```bash
# 启动本地 Ollama 服务器
ollama serve
# 默认端点：http://localhost:11434/v1
# 本地模型无需 API 密钥
```

### 步骤 2：运行 tetora init

```bash
tetora init
```

设置向导将引导你：
1. **选择语言**
2. **选择消息频道**
3. **选择 AI 提供商：**
   - 选择 **「Claude API Key」** 使用 Anthropic Claude
   - 选择 **「OpenAI 兼容端点」** 使用 OpenAI 或本地模型
4. **输入 API 密钥**（或本地模型的端点 URL）
5. **选择目录访问权限**
6. **创建第一个 Agent 角色**

### 步骤 3：验证并启动

```bash
tetora doctor
tetora serve
```

---

## 网页设置向导（非工程师）

如果偏好图形化设置界面，可使用网页向导：

```bash
tetora setup --web
```

这会在 `http://localhost:7474` 打开浏览器，显示 4 步安装向导。

---

## 安装后常用命令

| 命令 | 说明 |
|---|---|
| `tetora doctor` | 健康检查 — 有问题时先运行此命令 |
| `tetora serve` | 启动守护进程（Bot + HTTP API + 定时任务） |
| `tetora dashboard` | 打开网页仪表板 |
| `tetora status` | 快速查看状态 |
| `tetora init` | 重新运行设置向导 |

---

## 故障排除

### `tetora: command not found`

确认 `~/.tetora/bin` 已加入 PATH：
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

按照上方「系统要求」安装 sqlite3。

### `tetora doctor` 报告提供商错误

- **Claude Code CLI 路径：** 运行 `which claude` 并更新 `~/.tetora/config.json` 中的 `claudePath`
- **API 密钥无效：** 在提供商控制台确认密钥是否正确
- **找不到模型：** 确认模型名称与你的订阅方案一致

### Claude Code 登录问题

```bash
claude logout
claude
```

---

## 从源码编译

需要 Go 1.25+：

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## 后续步骤

- 阅读 [README](README.md) 了解完整功能说明
- 社区讨论：[github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- 报告问题：[github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
