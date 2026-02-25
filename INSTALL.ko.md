# Tetora 설치

<p align="center">
  <a href="INSTALL.md">English</a> | <a href="INSTALL.zh-TW.md">繁體中文</a> | <a href="INSTALL.ja.md">日本語</a> | <strong>한국어</strong> | <a href="INSTALL.es.md">Español</a> | <a href="INSTALL.fr.md">Français</a> | <a href="INSTALL.de.md">Deutsch</a> | <a href="INSTALL.pt.md">Português</a> | <a href="INSTALL.it.md">Italiano</a> | <a href="INSTALL.ru.md">Русский</a>
</p>

---

## 시스템 요구사항

| 요구사항 | 상세 |
|---|---|
| **운영 체제** | macOS, Linux 또는 Windows (WSL) |
| **터미널** | 모든 터미널 에뮬레이터 가능 |
| **sqlite3** | `PATH`에서 실행 가능해야 함 |
| **AI 공급자** | 아래 경로 1 또는 경로 2 참조 |

### sqlite3 설치

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

**Windows (WSL):** WSL 배포판 내에서 위의 Linux 명령을 사용하여 설치하세요.

---

## Tetora 다운로드

[Releases 페이지](https://github.com/TakumaLee/Tetora/releases/latest)에서 플랫폼에 맞는 바이너리를 다운로드하세요:

| 플랫폼 | 파일명 |
|---|---|
| macOS (Apple Silicon) | `tetora-darwin-arm64` |
| macOS (Intel) | `tetora-darwin-amd64` |
| Linux (x86_64) | `tetora-linux-amd64` |
| Linux (ARM64) | `tetora-linux-arm64` |
| Windows (WSL) | WSL 내에서 Linux 바이너리 사용 |

**바이너리 설치:**
```bash
# 다운로드한 파일명으로 변경하세요
chmod +x tetora-darwin-arm64
mv tetora-darwin-arm64 ~/.tetora/bin/tetora

# ~/.tetora/bin이 PATH에 포함되어 있는지 확인
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc  # 또는 ~/.bashrc
source ~/.zshrc
```

**또는 원라인 인스톨러 사용 (macOS / Linux):**
```bash
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)
```

---

## 경로 1: Claude Pro (월 $20) — 초보자 추천

이 경로는 **Claude Code CLI**를 AI 백엔드로 사용합니다. Claude Pro 구독(월 $20, [claude.ai](https://claude.ai))이 필요합니다.

> **이 경로를 선택하는 이유:** API 키 관리 불필요, 사용량 청구 걱정 없음. Pro 구독으로 Claude Code를 통한 Tetora 사용이 모두 커버됩니다.

> [!IMPORTANT]
> **사전 요구사항:** 이 경로는 Claude Pro 구독(월 $20)이 필요합니다. 아직 구독하지 않은 경우 먼저 [claude.ai/upgrade](https://claude.ai/upgrade)를 방문하세요.

### 1단계: Claude Code CLI 설치

```bash
npm install -g @anthropic-ai/claude-code
```

Node.js가 설치되지 않은 경우:
- **macOS:** `brew install node`
- **Linux:** `sudo apt install nodejs npm` (Ubuntu/Debian)

설치 확인:
```bash
claude --version
```

Claude Pro 계정으로 로그인:
```bash
claude
# 브라우저 기반 로그인 흐름을 따르세요
```

### 2단계: tetora init 실행

```bash
tetora init
```

설정 마법사가 다음 단계를 안내합니다:
1. **언어 선택** — 선호하는 언어 선택
2. **메시징 채널 선택** — Telegram, Discord, Slack 또는 없음
3. **AI 공급자 선택** — **"Claude Code CLI"** 선택
   - 마법사가 `claude` 바이너리 위치를 자동 감지합니다
   - Enter를 눌러 감지된 경로를 수락합니다
4. **디렉토리 액세스 선택** — Tetora가 읽고 쓸 수 있는 폴더
5. **첫 번째 에이전트 역할 생성** — 이름과 개성 설정

### 3단계: 확인 및 시작

```bash
# 설정이 올바른지 확인
tetora doctor

# 데몬 시작
tetora serve
```

웹 대시보드 열기:
```bash
tetora dashboard
```

---

## 경로 2: API 키

이 경로는 API 키를 직접 사용합니다. 지원 공급자:

- **Claude API** (Anthropic) — [console.anthropic.com](https://console.anthropic.com)
- **OpenAI API** — [platform.openai.com](https://platform.openai.com)
- **OpenAI 호환 엔드포인트** — Ollama, LM Studio, Azure OpenAI 등

> **비용 참고:** API 사용량은 토큰당 청구됩니다. 활성화 전에 공급자의 요금을 확인하세요.

### 1단계: API 키 발급

**Claude API:**
1. [console.anthropic.com](https://console.anthropic.com) 방문
2. 계정 생성 또는 로그인
3. **API Keys** → **Create Key** 이동
4. 키 복사 (형식: `sk-ant-...`)

**OpenAI:**
1. [platform.openai.com/api-keys](https://platform.openai.com/api-keys) 방문
2. **Create new secret key** 클릭
3. 키 복사 (형식: `sk-...`)

**OpenAI 호환 엔드포인트 (예: Ollama):**
```bash
# 로컬 Ollama 서버 시작
ollama serve
# 기본 엔드포인트: http://localhost:11434/v1
# 로컬 모델은 API 키 불필요
```

### 2단계: tetora init 실행

```bash
tetora init
```

설정 마법사가 안내합니다:
1. **언어 선택**
2. **메시징 채널 선택**
3. **AI 공급자 선택:**
   - Anthropic Claude: **"Claude API Key"** 선택
   - OpenAI 또는 로컬 모델: **"OpenAI 호환 엔드포인트"** 선택
4. **API 키 입력** (또는 로컬 모델의 엔드포인트 URL)
5. **디렉토리 액세스 선택**
6. **첫 번째 에이전트 역할 생성**

### 3단계: 확인 및 시작

```bash
tetora doctor
tetora serve
```

---

## 웹 설정 마법사 (비개발자용)

그래픽 설정 화면을 선호하는 경우 웹 마법사를 사용하세요:

```bash
tetora setup --web
```

`http://localhost:7474`에서 브라우저가 열리고 4단계 설정 마법사가 표시됩니다.

---

## 설치 후 자주 사용하는 명령어

| 명령어 | 설명 |
|---|---|
| `tetora doctor` | 헬스 체크 — 문제 발생 시 먼저 실행 |
| `tetora serve` | 데몬 시작 (봇 + HTTP API + 예약 작업) |
| `tetora dashboard` | 웹 대시보드 열기 |
| `tetora status` | 빠른 상태 확인 |
| `tetora init` | 설정 마법사 재실행 |

---

## 문제 해결

### `tetora: command not found`

`~/.tetora/bin`이 PATH에 포함되어 있는지 확인:
```bash
echo 'export PATH="$HOME/.tetora/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### `sqlite3: command not found`

위의 "시스템 요구사항"에 따라 sqlite3를 설치하세요.

### `tetora doctor`에서 공급자 오류 보고

- **Claude Code CLI 경로:** `which claude`를 실행하고 `~/.tetora/config.json`의 `claudePath` 업데이트
- **API 키 무효:** 공급자 콘솔에서 키 확인
- **모델 없음:** 모델 이름이 구독 플랜과 일치하는지 확인

### Claude Code 로그인 문제

```bash
claude logout
claude
```

---

## 소스에서 빌드

Go 1.25+ 필요:

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

---

## 다음 단계

- 전체 기능 문서는 [README](README.ko.md) 참조
- 커뮤니티: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)
- 문제 보고: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
