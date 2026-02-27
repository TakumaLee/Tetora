# 워크플로우

## 개요

워크플로우는 Tetora의 다단계 태스크 오케스트레이션 시스템입니다. JSON으로 단계 시퀀스를 정의하고, 여러 agent를 협력시켜 복잡한 태스크를 자동화할 수 있습니다.

**사용 사례:**

- 여러 agent가 순차적으로 또는 병렬로 처리해야 하는 태스크
- 조건 분기와 오류 재시도 로직이 있는 프로세스
- cron 스케줄, 이벤트, webhook으로 트리거되는 자동화 작업
- 실행 이력과 비용 추적이 필요한 공식적인 프로세스

## 빠른 시작

### 1. 워크플로우 JSON 작성

`my-workflow.json`을 생성합니다:

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

### 2. 가져오기 및 유효성 검사

```bash
# JSON 구조 유효성 검사
tetora workflow validate my-workflow.json

# ~/.tetora/workflows/ 에 가져오기
tetora workflow create my-workflow.json
```

### 3. 실행

```bash
# 워크플로우 실행
tetora workflow run research-and-summarize

# 변수 재정의
tetora workflow run research-and-summarize --var topic="LLM safety"

# 드라이런 (LLM 호출 없음, 비용 추정만)
tetora workflow run research-and-summarize --dry-run
```

### 4. 결과 확인

```bash
# 실행 이력 목록 조회
tetora workflow runs research-and-summarize

# 특정 실행의 상세 상태 확인
tetora workflow status <run-id>
```

## 워크플로우 JSON 구조

### 최상위 필드

| 필드 | 타입 | 필수 | 설명 |
|------|------|:----:|------|
| `name` | string | 예 | 워크플로우 이름. 영숫자, `-`, `_`만 허용 (예: `my-workflow`) |
| `description` | string | | 설명 |
| `steps` | WorkflowStep[] | 예 | 최소 1개의 단계 필요 |
| `variables` | map[string]string | | 기본값이 있는 입력 변수 (`""` = 필수 변수) |
| `timeout` | string | | 전체 타임아웃 (Go duration 형식, 예: `"30m"`, `"1h"`) |
| `onSuccess` | string | | 성공 시 알림 템플릿 |
| `onFailure` | string | | 실패 시 알림 템플릿 |

### WorkflowStep 필드

| 필드 | 타입 | 설명 |
|------|------|------|
| `id` | string | **필수** — 고유한 단계 식별자 |
| `type` | string | 단계 타입. 기본값은 `"dispatch"`. 아래 타입 참조 |
| `agent` | string | 이 단계를 실행할 agent 역할 |
| `prompt` | string | agent에 대한 지시 (`{{}}` 템플릿 지원) |
| `skill` | string | 스킬 이름 (type=skill인 경우) |
| `skillArgs` | string[] | 스킬 인수 (템플릿 지원) |
| `dependsOn` | string[] | 선행 단계 ID (DAG 의존성) |
| `model` | string | LLM 모델 재정의 |
| `provider` | string | 프로바이더 재정의 |
| `timeout` | string | 단계별 타임아웃 |
| `budget` | number | 비용 한도 (USD) |
| `permissionMode` | string | 권한 모드 |
| `if` | string | 조건식 (type=condition) |
| `then` | string | 조건이 true일 때 이동할 단계 ID |
| `else` | string | 조건이 false일 때 이동할 단계 ID |
| `handoffFrom` | string | 소스 단계 ID (type=handoff) |
| `parallel` | WorkflowStep[] | 병렬로 실행할 하위 단계 (type=parallel) |
| `retryMax` | int | 최대 재시도 횟수 (`onError: "retry"` 필요) |
| `retryDelay` | string | 재시도 간격, 예: `"10s"` |
| `onError` | string | 오류 처리: `"stop"` (기본값), `"skip"`, `"retry"` |
| `toolName` | string | 도구 이름 (type=tool_call) |
| `toolInput` | map[string]string | 도구 입력 파라미터 (`{{var}}` 확장 지원) |
| `delay` | string | 대기 시간 (type=delay), 예: `"30s"`, `"5m"` |
| `notifyMsg` | string | 알림 메시지 (type=notify, 템플릿 지원) |
| `notifyTo` | string | 알림 채널 힌트 (예: `"telegram"`) |

## 단계 타입

### dispatch (기본값)

지정한 agent에게 프롬프트를 전송하여 실행합니다. 가장 일반적인 단계 타입이며, `type`을 생략했을 때 사용됩니다.

```json
{
  "id": "draft",
  "agent": "kohaku",
  "prompt": "Write an article about {{topic}}",
  "model": "claude-sonnet-4-20250514",
  "timeout": "10m"
}
```

**필수:** `prompt`
**선택:** `agent`, `model`, `provider`, `timeout`, `budget`, `permissionMode`

### skill

등록된 스킬을 실행합니다.

```json
{
  "id": "search",
  "type": "skill",
  "skill": "web-search",
  "skillArgs": ["{{topic}}", "--depth", "3"]
}
```

**필수:** `skill`
**선택:** `skillArgs`

### condition

조건식을 평가하여 분기를 결정합니다. true이면 `then`으로, false이면 `else`로 이동합니다. 선택되지 않은 분기는 건너뜀(skipped)으로 표시됩니다.

```json
{
  "id": "check-type",
  "type": "condition",
  "if": "{{type}} == 'technical'",
  "then": "tech-research",
  "else": "creative-draft"
}
```

**필수:** `if`, `then`
**선택:** `else`

지원 연산자:
- `==` — 같음 (예: `{{type}} == 'technical'`)
- `!=` — 같지 않음
- 참/거짓 검사 — 비어있지 않고 `"false"` 또는 `"0"`이 아니면 true

### parallel

여러 하위 단계를 동시에 실행하고 모두 완료될 때까지 기다립니다. 하위 단계의 출력은 `\n---\n`으로 결합됩니다.

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

**필수:** `parallel` (최소 1개의 하위 단계)

개별 하위 단계의 결과는 `{{steps.search-papers.output}}`으로 참조할 수 있습니다.

### handoff

한 단계의 출력을 다른 agent에게 전달하여 추가 처리를 수행합니다. 소스 단계의 전체 출력이 수신 agent의 컨텍스트가 됩니다.

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

**필수:** `handoffFrom`, `agent`
**선택:** `prompt` (수신 agent에 대한 지시)

### tool_call

도구 레지스트리에 등록된 도구를 호출합니다.

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

**필수:** `toolName`
**선택:** `toolInput` (`{{var}}` 확장 지원)

### delay

지정한 시간 동안 대기한 후 다음 단계로 진행합니다.

```json
{
  "id": "wait",
  "type": "delay",
  "delay": "30s"
}
```

**필수:** `delay` (Go duration 형식: `"30s"`, `"5m"`, `"1h"`)

### notify

알림 메시지를 전송합니다. 메시지는 SSE 이벤트(type=`workflow_notify`)로 발행되므로 외부 컨슈머가 Telegram, Slack 등을 트리거할 수 있습니다.

```json
{
  "id": "notify-done",
  "type": "notify",
  "notifyMsg": "Task complete: {{steps.review.output}}",
  "notifyTo": "telegram"
}
```

**필수:** `notifyMsg`
**선택:** `notifyTo` (채널 힌트)

## 변수와 템플릿

워크플로우는 단계 실행 전에 확장되는 `{{}}` 템플릿 문법을 지원합니다.

### 입력 변수

```
{{varName}}
```

`variables`의 기본값 또는 `--var key=value` 재정의 값에서 해석됩니다.

### 단계 결과

```
{{steps.ID.output}}    — 단계 출력 텍스트
{{steps.ID.status}}    — 단계 상태 (success/error/skipped/timeout)
{{steps.ID.error}}     — 단계 오류 메시지
```

### 환경 변수

```
{{env.KEY}}            — 시스템 환경 변수
```

### 예시

```json
{
  "id": "summarize",
  "agent": "kohaku",
  "prompt": "Topic: {{topic}}\nResearch results: {{steps.research.output}}\n\nPlease write a summary.",
  "dependsOn": ["research"]
}
```

## 의존성 및 흐름 제어

### dependsOn — DAG 의존성

`dependsOn`을 사용하여 실행 순서를 정의합니다. 시스템이 단계들을 DAG(유향 비순환 그래프)로 자동 정렬합니다.

```json
{
  "id": "step-c",
  "dependsOn": ["step-a", "step-b"],
  "prompt": "..."
}
```

- `step-c`는 `step-a`와 `step-b`가 모두 완료될 때까지 대기합니다
- `dependsOn`이 없는 단계는 즉시 시작됩니다 (병렬 실행될 수 있음)
- 순환 의존성은 감지되어 거부됩니다

### 조건 분기

`condition` 단계의 `then`/`else`에 따라 어느 다운스트림 단계가 실행될지 결정됩니다:

```
classify (condition)
  ├── then → tech-research
  └── else → creative-draft
```

선택되지 않은 분기의 단계는 `skipped`로 표시됩니다. 다운스트림 단계는 `dependsOn`을 기반으로 정상적으로 평가됩니다.

## 오류 처리

### onError 전략

각 단계에 `onError`를 설정할 수 있습니다:

| 값 | 동작 |
|----|------|
| `"stop"` | **기본값** — 실패 시 워크플로우를 중단. 나머지 단계는 건너뜀으로 표시됨 |
| `"skip"` | 실패한 단계를 건너뜀으로 표시하고 계속 진행 |
| `"retry"` | `retryMax` + `retryDelay`에 따라 재시도. 모든 재시도 실패 시 오류로 처리 |

### 재시도 설정

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

- `retryMax`: 최대 재시도 횟수 (최초 시도 제외)
- `retryDelay`: 재시도 사이의 대기 시간. 기본값은 5초
- `onError: "retry"`인 경우에만 유효

## 트리거

트리거를 사용하면 워크플로우를 자동으로 실행할 수 있습니다. `config.json`의 `workflowTriggers` 배열에서 설정합니다.

### WorkflowTriggerConfig 구조

| 필드 | 타입 | 설명 |
|------|------|------|
| `name` | string | 트리거 이름 |
| `workflowName` | string | 실행할 워크플로우 |
| `enabled` | bool | 활성화 여부 (기본값: true) |
| `trigger` | TriggerSpec | 트리거 조건 |
| `variables` | map[string]string | 워크플로우 변수 재정의 |
| `cooldown` | string | 쿨다운 기간 (예: `"5m"`, `"1h"`) |

### TriggerSpec 구조

| 필드 | 타입 | 설명 |
|------|------|------|
| `type` | string | `"cron"`, `"event"`, 또는 `"webhook"` |
| `cron` | string | cron 표현식 (5개 필드: 분 시 일 월 요일) |
| `tz` | string | 타임존 (예: `"Asia/Taipei"`). cron 전용 |
| `event` | string | SSE 이벤트 타입. `*` 접미사 와일드카드 지원 (예: `"deploy_*"`) |
| `webhook` | string | webhook 경로 접미사 |

### Cron 트리거

30초마다 확인하며 분당 최대 1회 실행됩니다 (중복 제거 적용).

```json
{
  "name": "daily-briefing",
  "workflowName": "research-and-summarize",
  "trigger": {"type": "cron", "cron": "0 8 * * *", "tz": "Asia/Taipei"},
  "variables": {"topic": "AI industry news"},
  "cooldown": "12h"
}
```

### Event 트리거

SSE `_triggers` 채널을 수신하여 이벤트 타입을 매칭합니다. `*` 접미사 와일드카드를 지원합니다.

```json
{
  "name": "on-deploy",
  "workflowName": "content-pipeline",
  "trigger": {"type": "event", "event": "deploy_*"},
  "variables": {"type": "technical"}
}
```

Event 트리거는 자동으로 추가 변수를 주입합니다: `event_type`, `task_id`, `session_id`, 및 `event_` 접두사가 붙은 이벤트 데이터 필드.

### Webhook 트리거

HTTP POST로 트리거됩니다:

```json
{
  "name": "external-hook",
  "workflowName": "content-pipeline",
  "trigger": {"type": "webhook", "webhook": "content-request"}
}
```

사용 방법:

```bash
curl -X POST http://localhost:PORT/api/triggers/webhook/external-hook \
  -H "Content-Type: application/json" \
  -d '{"topic": "new feature"}'
```

POST 본문의 JSON 키-값 쌍은 추가 워크플로우 변수로 주입됩니다.

### 쿨다운

모든 트리거는 `cooldown`을 지원하여 짧은 시간 내 반복 실행을 방지합니다. 쿨다운 중의 트리거는 자동으로 무시됩니다.

### 트리거 메타 변수

시스템은 각 트리거 실행 시 다음 변수를 자동으로 주입합니다:

- `_trigger_name` — 트리거 이름
- `_trigger_type` — 트리거 타입 (cron/event/webhook)
- `_trigger_time` — 트리거 시각 (RFC3339)

## 실행 모드

### live (기본값)

LLM을 호출하고, 이력을 기록하며, SSE 이벤트를 발행하는 전체 실행 모드입니다.

```bash
tetora workflow run my-workflow
```

### dry-run

LLM 호출 없이 각 단계의 비용을 추정합니다. condition 단계는 정상적으로 평가되고, dispatch/skill/handoff 단계는 비용 추정값을 반환합니다.

```bash
tetora workflow run my-workflow --dry-run
```

### shadow

LLM 호출은 정상적으로 실행하지만 태스크 이력이나 세션 로그에는 기록하지 않습니다. 테스트 목적에 적합합니다.

```bash
tetora workflow run my-workflow --shadow
```

## CLI 레퍼런스

```
tetora workflow <command> [options]
```

| 명령 | 설명 |
|------|------|
| `list` | 저장된 모든 워크플로우 목록 조회 |
| `show <name>` | 워크플로우 정의 표시 (요약 + JSON) |
| `validate <name\|file>` | 워크플로우 유효성 검사 (이름 또는 JSON 파일 경로 허용) |
| `create <file>` | JSON 파일에서 워크플로우 가져오기 (먼저 유효성 검사 실행) |
| `delete <name>` | 워크플로우 삭제 |
| `run <name> [flags]` | 워크플로우 실행 |
| `runs [name]` | 실행 이력 목록 조회 (이름으로 필터링 가능) |
| `status <run-id>` | 실행의 상세 상태 표시 (JSON 출력) |
| `messages <run-id>` | 실행의 agent 메시지와 handoff 레코드 표시 |
| `history <name>` | 워크플로우 버전 이력 표시 |
| `rollback <name> <version-id>` | 특정 버전으로 복원 |
| `diff <version1> <version2>` | 두 버전 비교 |

### run 명령 플래그

| 플래그 | 설명 |
|--------|------|
| `--var key=value` | 워크플로우 변수 재정의 (여러 번 사용 가능) |
| `--dry-run` | 드라이런 모드 (LLM 호출 없음) |
| `--shadow` | 섀도우 모드 (이력 기록 없음) |

### 별칭

- `list` = `ls`
- `delete` = `rm`
- `messages` = `msgs`

## HTTP API 레퍼런스

### 워크플로우 CRUD

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/workflows` | 모든 워크플로우 목록 조회 |
| POST | `/workflows` | 워크플로우 생성 (본문: Workflow JSON) |
| GET | `/workflows/{name}` | 단일 워크플로우 정의 조회 |
| DELETE | `/workflows/{name}` | 워크플로우 삭제 |
| POST | `/workflows/{name}/validate` | 워크플로우 유효성 검사 |
| POST | `/workflows/{name}/run` | 워크플로우 실행 (비동기, `202 Accepted` 반환) |
| GET | `/workflows/{name}/runs` | 워크플로우 실행 이력 조회 |

#### POST /workflows/{name}/run 본문

```json
{
  "variables": {
    "topic": "AI agents"
  }
}
```

### 워크플로우 실행

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/workflow-runs` | 모든 실행 레코드 목록 조회 (`?workflow=name`으로 필터링 가능) |
| GET | `/workflow-runs/{id}` | 실행 상세 조회 (handoff + agent 메시지 포함) |

### 트리거

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/api/triggers` | 모든 트리거 상태 목록 조회 |
| POST | `/api/triggers/{name}/fire` | 트리거 수동 실행 |
| GET | `/api/triggers/{name}/runs` | 트리거 실행 이력 조회 (`?limit=N` 추가 가능) |
| POST | `/api/triggers/webhook/{id}` | webhook 트리거 (본문: JSON 키-값 변수) |

## 버전 관리

`create` 또는 수정 시마다 버전 스냅샷이 자동으로 생성됩니다.

```bash
# 버전 이력 확인
tetora workflow history my-workflow

# 특정 버전으로 복원
tetora workflow rollback my-workflow <version-id>

# 두 버전 비교
tetora workflow diff <version-id-1> <version-id-2>
```

## 유효성 검사 규칙

시스템은 `create`와 `run` 전에 모두 유효성 검사를 수행합니다:

- `name`은 필수이며 영숫자, `-`, `_`만 허용
- 최소 1개의 단계 필요
- 단계 ID는 고유해야 함
- `dependsOn`의 참조 대상은 기존 단계 ID여야 함
- 단계는 자기 자신에 의존할 수 없음
- 순환 의존성은 거부됨 (DAG 사이클 감지)
- 단계 타입별 필수 필드 (예: dispatch는 `prompt`, condition은 `if` + `then` 필요)
- `timeout`, `retryDelay`, `delay`는 유효한 Go duration 형식이어야 함
- `onError`는 `stop`, `skip`, `retry`만 허용
- condition의 `then`/`else`는 기존 단계 ID를 참조해야 함
- handoff의 `handoffFrom`은 기존 단계 ID를 참조해야 함
