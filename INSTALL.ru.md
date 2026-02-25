# Установка Tetora

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.ja.md">日本語</a> | <a href="INSTALL.ko.md">한국어</a> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.it.md">Italiano</a> | <strong>Русский</strong>
</p>

---

## Требования

| Требование | Подробности |
|---|---|
| **Операционная система** | macOS, Linux или Windows (WSL) |
| **Терминал** | Любой эмулятор терминала |
| **sqlite3** | Должен быть доступен в `PATH` |
| **Провайдер ИИ** | См. Путь 1 или Путь 2 ниже |

### Установка sqlite3

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

**Windows (WSL):** Установите внутри вашего WSL-дистрибутива с помощью команд Linux выше.

---

## Скачать Tetora

Перейдите на [страницу Releases](https://github.com/TakumaLee/Tetora/releases/latest) и скачайте бинарный файл для вашей платформы:

| Платформа | Файл |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | Используйте Linux-бинарник внутри WSL |

**Установка бинарного файла:**
```bash
# Замените имя файла на загруженное вами
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# Убедитесь, что ~/.tetora/bin есть в PATH
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # или ~/.bashrc
source ~/.zshrc
```

**Или используйте однострочный установщик (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## Путь 1: Claude Pro ($20/месяц) — Рекомендуется для начинающих

Этот путь использует **Claude Code CLI** в качестве бэкенда ИИ. Требуется активная подписка Claude Pro ($20/месяц на [claude.ai](https://claude.ai)).

> **Почему этот путь?** Не нужно управлять API-ключами, никаких неожиданных счетов. Ваша подписка Pro покрывает всё использование Tetora через Claude Code.

> [!IMPORTANT]
> **Предварительные требования:** Для этого пути необходима активная подписка Claude Pro ($20/месяц). Если вы ещё не подписались, сначала посетите [claude.ai/upgrade](https://claude.ai/upgrade).

### Шаг 1: Установка Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

Если у вас не установлен Node.js:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)

Проверка установки:
```bash
claude --version
```

Войдите с вашей учётной записью Claude Pro:
```bash
claude
# Следуйте процессу входа в браузере
```

### Шаг 2: Запуск tetora init

```bash
tetora init
```

Мастер настройки проведёт вас через:
1. **Выбор языка** — выберите предпочтительный язык
2. **Выбор канала сообщений** — Telegram, Discord, Slack или Нет
3. **Выбор провайдера ИИ** — выберите **«Claude Code CLI»**
   - Мастер автоматически определит расположение вашего бинарника `claude`
   - Нажмите Enter для принятия обнаруженного пути
4. **Выбор доступа к директориям** — какие папки Tetora может читать/записывать
5. **Создание первой роли агента** — задайте имя и личность

### Шаг 3: Проверка и запуск

```bash
# Проверить корректность настройки
tetora doctor

# Запустить демон
tetora serve
```

Открыть веб-панель:
```bash
tetora dashboard
```

---

## Путь 2: API-ключ

Этот путь использует прямой API-ключ. Поддерживаемые провайдеры:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **Любой совместимый с OpenAI эндпоинт** — Ollama, LM Studio, Azure OpenAI и т.д.

> **Примечание о расходах:** Использование API тарифицируется за каждый токен. Ознакомьтесь с ценообразованием провайдера перед активацией.

### Шаг 1: Получение API-ключа

**Claude API:**
1. Перейдите на [console.anthropic.com](https://console.anthropic.com)
2. Создайте учётную запись или войдите
3. Перейдите в **API Keys** → **Create Key**
4. Скопируйте ключ (начинается с `sk-ant-...`)

**OpenAI:**
1. Перейдите на [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Нажмите **Create new secret key**
3. Скопируйте ключ (начинается с `sk-...`)

**Совместимый с OpenAI эндпоинт (например, Ollama):**
```bash
# Запустить локальный сервер Ollama
ollama serve
# Эндпоинт по умолчанию: http://localhost:11434/v1
# Для локальных моделей API-ключ не требуется
```

### Шаг 2: Запуск tetora init

```bash
tetora init
```

Мастер проведёт вас через:
1. **Выбор языка**
2. **Выбор канала сообщений**
3. **Выбор провайдера ИИ:**
   - Выберите **«Claude API Key»** для Anthropic Claude
   - Выберите **«Совместимый с OpenAI эндпоинт»** для OpenAI или локальных моделей
4. **Ввод API-ключа** (или URL эндпоинта для локальных моделей)
5. **Выбор доступа к директориям**
6. **Создание первой роли агента**

### Шаг 3: Проверка и запуск

```bash
tetora doctor
tetora serve
```

---

## Веб-мастер настройки (для не-разработчиков)

Если вы предпочитаете графический интерфейс, используйте веб-мастер:

```bash
tetora setup --web
```

Откроется окно браузера по адресу `http://localhost:7474` с 4-шаговым мастером настройки.

---

## Полезные команды после установки

| Команда | Описание |
|---|---|
| `tetora doctor` | Проверки состояния — запустите при проблемах |
| `tetora serve` | Запустить демон (боты + HTTP API + запланированные задачи) |
| `tetora dashboard` | Открыть веб-панель |
| `tetora status` | Быстрый обзор состояния |
| `tetora init` | Перезапустить мастер настройки |

---

## Устранение неполадок

### `tetora: command not found`

Убедитесь, что `~/.tetora/bin` есть в вашем PATH:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

Установите sqlite3 для вашей платформы (см. Требования выше).

### `tetora doctor` сообщает об ошибках провайдера

- **Путь Claude Code CLI:** Выполните `which claude` и обновите `claudePath` в `~/.tetora/config.json`
- **Недействительный API-ключ:** Проверьте ключ в консоли вашего провайдера
- **Модель не найдена:** Убедитесь, что название модели соответствует вашему уровню подписки

### Проблемы входа в Claude Code

```bash
claude logout
claude
```

---

## Сборка из исходного кода

Требуется Go 1.25+:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## Следующие шаги

- Прочитайте [README](README.md) для полной документации функций
- Сообщество: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- Сообщить о проблеме: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
