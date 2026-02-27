<p align="center">
  <img src="assets/banner.png" alt="Tetora -- AI 에이전트 오케스트레이터" width="800">
</p>

<p align="center">
  <strong>멀티 에이전트 아키텍처를 갖춘 셀프 호스팅 AI 어시스턴트 플랫폼.</strong>
</p>

[English](README.md) | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md) | **한국어** | [Bahasa Indonesia](README.id.md) | [ภาษาไทย](README.th.md) | [Filipino](README.fil.md) | [Español](README.es.md) | [Français](README.fr.md) | [Deutsch](README.de.md)

Tetora는 외부 의존성 없이 단일 Go 바이너리로 실행됩니다. 이미 사용 중인 AI 프로바이더에 연결하고, 팀이 활동하는 메시징 플랫폼과 통합하며, 모든 데이터를 자체 하드웨어에 보관합니다.

---

## Tetora란

Tetora는 여러 에이전트 역할을 정의할 수 있는 AI 에이전트 오케스트레이터입니다. 각 역할은 고유한 성격, 시스템 프롬프트, 모델, 도구 접근 권한을 가지며, 채팅 플랫폼, HTTP API 또는 커맨드라인을 통해 상호작용할 수 있습니다.

**핵심 기능:**

- **멀티 에이전트 역할** -- 개별 성격, 예산, 도구 권한을 가진 고유한 에이전트를 정의
- **멀티 프로바이더** -- Claude API, OpenAI, Gemini 등; 자유롭게 교체하거나 조합 가능
- **멀티 플랫폼** -- Telegram, Discord, Slack, Google Chat, LINE, Matrix, Teams, Signal, WhatsApp, iMessage
- **크론 작업** -- 승인 게이트와 알림을 포함한 반복 작업 예약
- **지식 베이스** -- 에이전트에 문서를 제공하여 근거 있는 응답 생성
- **영구 메모리** -- 에이전트가 세션 간 컨텍스트를 기억; 통합 메모리 계층과 정리 기능
- **MCP 지원** -- Model Context Protocol 서버를 도구 제공자로 연결
- **스킬과 워크플로우** -- 조합 가능한 스킬 팩과 다단계 워크플로우 파이프라인
- **웹훅** -- 외부 시스템에서 에이전트 동작을 트리거
- **비용 관리** -- 역할별 및 전역 예산과 자동 모델 다운그레이드
- **데이터 보존** -- 테이블별 설정 가능한 정리 정책, 전체 내보내기 및 삭제
- **플러그인** -- 외부 플러그인 프로세스를 통한 기능 확장
- **스마트 리마인더, 습관, 목표, 연락처, 재무 추적, 브리핑 등**

---

## 빠른 시작

### 엔지니어용

```bash
# 최신 릴리스 설치
. <(curl -fsSL https://raw.githubusercontent.com/TakumaLee/Tetora/main/install.sh)

# 설정 마법사 실행
tetora init

# 모든 설정이 올바른지 확인
tetora doctor

# 데몬 시작
tetora serve
```

### 비엔지니어용

1. [릴리스 페이지](https://github.com/TakumaLee/Tetora/releases/latest)로 이동합니다
2. 사용 중인 플랫폼에 맞는 바이너리를 다운로드합니다 (예: Apple Silicon Mac의 경우 `tetora-darwin-arm64`)
3. PATH에 포함된 디렉토리로 이동하고 `tetora`로 이름을 변경하거나, `~/.tetora/bin/`에 배치합니다
4. 터미널을 열고 다음을 실행합니다:
   ```
   tetora init
   tetora doctor
   tetora serve
   ```

---

## 에이전트

모든 Tetora 에이전트는 단순한 챗봇 그 이상입니다 -- 고유한 정체성을 가집니다. 각 에이전트(**역할**이라 부름)는 **소울 파일**로 정의됩니다: 에이전트에게 성격, 전문성, 커뮤니케이션 스타일, 행동 지침을 부여하는 마크다운 문서입니다.

### 역할 정의

역할은 `config.json`의 `roles` 키 아래에 선언합니다:

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

### 소울 파일

소울 파일은 에이전트에게 *자신이 누구인지* 알려줍니다. 워크스페이스 디렉토리(기본값: `~/.tetora/workspace/`)에 배치합니다:

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

### 시작하기

`tetora init`은 첫 번째 역할을 생성하는 과정을 안내하며, 기본 소울 파일을 자동으로 생성합니다. 언제든지 편집할 수 있으며, 변경 사항은 다음 세션에서 적용됩니다.

---

## 소스에서 빌드

```bash
git clone https://github.com/TakumaLee/Tetora.git
cd tetora
make install
```

이 명령은 바이너리를 빌드하고 `~/.tetora/bin/tetora`에 설치합니다. `~/.tetora/bin`이 `PATH`에 포함되어 있는지 확인하세요.

테스트 스위트를 실행하려면:

```bash
make test
```

---

## 요구 사항

| 요구 사항 | 세부 정보 |
|---|---|
| **sqlite3** | `PATH`에서 사용 가능해야 합니다. 모든 영구 저장소에 사용됩니다. |
| **AI 프로바이더 API 키** | 최소 하나 필요: Claude API, OpenAI, Gemini, 또는 OpenAI 호환 엔드포인트. |
| **Go 1.25+** | 소스에서 빌드하는 경우에만 필요합니다. |

---

## 지원 플랫폼

| 플랫폼 | 아키텍처 | 상태 |
|---|---|---|
| macOS | amd64, arm64 | 안정 |
| Linux | amd64, arm64 | 안정 |
| Windows | amd64 | 베타 |

---

## 아키텍처

모든 런타임 데이터는 `~/.tetora/` 아래에 저장됩니다:

```
~/.tetora/
  config.json        메인 설정 (프로바이더, 역할, 통합)
  jobs.json          크론 작업 정의
  history.db         SQLite 데이터베이스 (히스토리, 메모리, 세션, 임베딩, ...)
  sessions/          에이전트별 세션 파일
  knowledge/         지식 베이스 문서
  logs/              구조화된 로그 파일
  outputs/           생성된 출력 파일
  uploads/           임시 업로드 저장소
  bin/               설치된 바이너리
```

설정은 `$ENV_VAR` 참조를 지원하는 일반 JSON을 사용하므로, 비밀 값을 하드코딩할 필요가 없습니다. 설정 마법사(`tetora init`)가 대화형으로 작동하는 `config.json`을 생성합니다.

핫 리로드가 지원됩니다: 실행 중인 데몬에 `SIGHUP`을 전송하면 다운타임 없이 `config.json`을 다시 로드합니다.

---

## 워크플로우

Tetora에는 다단계, 다중 에이전트 작업을 조율하는 내장 워크플로우 엔진이 포함되어 있습니다. JSON으로 파이프라인을 정의하면 에이전트들이 자동으로 협력합니다.

**[워크플로우 전체 문서](docs/workflow.ko.md)** — 단계 유형, 변수, 트리거, CLI 및 API 참조.

빠른 예시:

```bash
# 워크플로우 유효성 검사 및 가져오기
tetora workflow create examples/workflow-basic.json

# 실행
tetora workflow run research-and-summarize --var topic="LLM safety"

# 결과 확인
tetora workflow status <run-id>
```

바로 사용할 수 있는 워크플로우 JSON 파일은 [`examples/`](examples/)를 참조하세요.

---

## CLI 레퍼런스

| 명령어 | 설명 |
|---|---|
| `tetora init` | 대화형 설정 마법사 |
| `tetora doctor` | 상태 점검 및 진단 |
| `tetora serve` | 데몬 시작 (챗봇 + HTTP API + 크론) |
| `tetora run --file tasks.json` | JSON 파일에서 작업 디스패치 (CLI 모드) |
| `tetora dispatch "Summarize this"` | 데몬을 통해 임시 작업 실행 |
| `tetora route "Review code security"` | 스마트 디스패치 -- 최적의 역할에 자동 라우팅 |
| `tetora status` | 데몬, 작업, 비용의 빠른 개요 |
| `tetora job list` | 모든 크론 작업 목록 |
| `tetora job trigger <name>` | 크론 작업 수동 트리거 |
| `tetora role list` | 모든 설정된 역할 목록 |
| `tetora role show <name>` | 역할 세부 정보 및 소울 미리보기 |
| `tetora history list` | 최근 실행 히스토리 표시 |
| `tetora history cost` | 비용 요약 표시 |
| `tetora session list` | 최근 세션 목록 |
| `tetora memory list` | 에이전트 메모리 항목 목록 |
| `tetora knowledge list` | 지식 베이스 문서 목록 |
| `tetora skill list` | 사용 가능한 스킬 목록 |
| `tetora workflow list` | 설정된 워크플로우 목록 |
| `tetora mcp list` | MCP 서버 연결 목록 |
| `tetora budget show` | 예산 상태 표시 |
| `tetora config show` | 현재 설정 표시 |
| `tetora config validate` | config.json 유효성 검사 |
| `tetora backup` | 백업 아카이브 생성 |
| `tetora restore <file>` | 백업 아카이브에서 복원 |
| `tetora dashboard` | 브라우저에서 웹 대시보드 열기 |
| `tetora logs` | 데몬 로그 보기 (`-f`로 실시간 추적, `--json`으로 구조화된 출력) |
| `tetora data status` | 데이터 보존 상태 표시 |
| `tetora service install` | launchd 서비스로 설치 (macOS) |
| `tetora completion <shell>` | 셸 자동완성 생성 (bash, zsh, fish) |
| `tetora version` | 버전 표시 |

전체 명령어 레퍼런스는 `tetora help`를 실행하세요.

---

## 기여

기여를 환영합니다. 큰 변경 사항의 경우 풀 리퀘스트를 제출하기 전에 이슈를 열어 논의해 주세요.

- **이슈**: [github.com/TakumaLee/Tetora/issues](https://github.com/TakumaLee/Tetora/issues)
- **토론**: [github.com/TakumaLee/Tetora/discussions](https://github.com/TakumaLee/Tetora/discussions)

이 프로젝트는 AGPL-3.0 라이선스가 적용됩니다. 이 라이선스는 파생 작업 및 네트워크를 통해 접근 가능한 배포 또한 동일한 라이선스 하에 오픈 소스로 공개할 것을 요구합니다. 기여하기 전에 라이선스를 검토해 주세요.

---

## 라이선스

[AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html)

Copyright (c) Tetora contributors.
