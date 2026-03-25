---
title: "설정 레퍼런스"
lang: "ko"
order: 1
description: "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings."
---
# 설정 레퍼런스

## 개요

Tetora는 `~/.tetora/config.json` 에 위치한 단일 JSON 파일로 설정합니다.

**주요 동작 방식:**

- **`$ENV_VAR` 치환** — `$`로 시작하는 문자열 값은 시작 시 해당 환경 변수로 대체됩니다. 시크릿(API 키, 토큰 등)을 하드코딩하는 대신 이 방식을 사용하세요.
- **핫 리로드** — 데몬에 `SIGHUP`을 보내면 설정을 다시 읽습니다. 잘못된 설정은 거부되며 기존 설정이 유지됩니다. 데몬은 크래시하지 않습니다.
- **상대 경로** — `jobsFile`, `historyDB`, `defaultWorkdir`, 디렉터리 필드는 설정 파일의 디렉터리(`~/.tetora/`) 기준으로 해석됩니다.
- **하위 호환성** — 구형 `"roles"` 키는 `"agents"`의 별칭입니다. `smartDispatch` 내부의 구형 `"defaultRole"` 키는 `"defaultAgent"`의 별칭입니다.

---

## 최상위 필드

### 핵심 설정

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `listenAddr` | string | `"127.0.0.1:8991"` | API 및 대시보드의 HTTP 수신 주소. 형식: `host:port`. |
| `apiToken` | string | `""` | 모든 API 요청에 필요한 Bearer 토큰. 비어 있으면 인증 없음(프로덕션에는 권장하지 않음). `$ENV_VAR` 지원. |
| `maxConcurrent` | int | `8` | 최대 동시 agent 작업 수. 20 초과 시 시작 경고가 표시됩니다. |
| `defaultModel` | string | `"sonnet"` | 기본 Claude 모델 이름. agent별로 오버라이드하지 않으면 provider에 전달됩니다. |
| `defaultTimeout` | string | `"1h"` | 기본 작업 타임아웃. Go duration 형식: `"15m"`, `"1h"`, `"30s"`. |
| `defaultBudget` | float64 | `0` | 작업당 기본 비용 예산(USD). `0`은 제한 없음. |
| `defaultPermissionMode` | string | `"acceptEdits"` | 기본 Claude 권한 모드. 주요 값: `"acceptEdits"`, `"default"`. |
| `defaultAgent` | string | `""` | 어떤 라우팅 규칙도 일치하지 않을 때의 시스템 전체 대체 agent 이름. |
| `defaultWorkdir` | string | `""` | agent 작업의 기본 작업 디렉터리. 디스크에 존재해야 합니다. |
| `claudePath` | string | `"claude"` | `claude` CLI 바이너리 경로. 기본값은 `$PATH`에서 `claude`를 찾습니다. |
| `defaultProvider` | string | `"claude"` | agent 레벨 오버라이드가 없을 때 사용할 provider 이름. |
| `log` | bool | `false` | 파일 로깅을 활성화하는 레거시 플래그. 대신 `logging.level`을 사용하세요. |
| `maxPromptLen` | int | `102400` | 최대 프롬프트 길이(바이트, 100 KB). 이 값을 초과하는 요청은 거부됩니다. |
| `configVersion` | int | `0` | 설정 스키마 버전. 자동 마이그레이션에 사용됩니다. 수동으로 설정하지 마세요. |
| `encryptionKey` | string | `""` | 민감한 데이터의 필드 레벨 암호화를 위한 AES 키. `$ENV_VAR` 지원. |
| `streamToChannels` | bool | `false` | 연결된 메시징 채널(Discord, Telegram 등)에 라이브 작업 상태를 스트리밍합니다. |
| `cronNotify` | bool\|null | `null` (true) | `false`로 설정하면 모든 cron 작업 완료 알림이 억제됩니다. `null` 또는 `true`이면 활성화됩니다. |
| `cronReplayHours` | int | `2` | 데몬 시작 시 놓친 cron 작업을 되돌아볼 시간(시). |
| `diskBudgetGB` | float64 | `1.0` | 최소 여유 디스크 공간(GB). 이 수준 이하에서는 cron 작업이 거부됩니다. |
| `diskWarnMB` | int | `500` | 여유 디스크 경고 임계값(MB). WARN을 로깅하지만 작업은 계속됩니다. |
| `diskBlockMB` | int | `200` | 여유 디스크 차단 임계값(MB). 작업이 `skipped_disk_full` 상태로 건너뜁니다. |

### 디렉터리 오버라이드

기본적으로 모든 디렉터리는 `~/.tetora/` 아래에 위치합니다. 비표준 레이아웃이 필요한 경우에만 오버라이드하세요.

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `knowledgeDir` | string | `~/.tetora/knowledge/` | 워크스페이스 지식 파일 디렉터리. |
| `agentsDir` | string | `~/.tetora/agents/` | agent별 SOUL.md 파일이 있는 디렉터리. |
| `workspaceDir` | string | `~/.tetora/workspace/` | rules, memory, skills, drafts 등을 위한 디렉터리. |
| `runtimeDir` | string | `~/.tetora/runtime/` | sessions, outputs, logs, cache 디렉터리. |
| `vaultDir` | string | `~/.tetora/vault/` | 암호화된 시크릿 vault 디렉터리. |
| `historyDB` | string | `history.db` | 작업 이력용 SQLite 데이터베이스 경로. 설정 디렉터리 기준 상대 경로. |
| `jobsFile` | string | `jobs.json` | cron 작업 정의 파일 경로. 설정 디렉터리 기준 상대 경로. |

### 전역 허용 디렉터리

```json
{
  "allowedDirs": ["/Users/me/projects", "/tmp"],
  "defaultAddDirs": ["/Users/me/shared-context"]
}
```

| 필드 | 타입 | 설명 |
|---|---|---|
| `allowedDirs` | string[] | agent가 읽고 쓸 수 있는 디렉터리. 전역으로 적용되며 agent별로 좁힐 수 있습니다. |
| `defaultAddDirs` | string[] | 모든 작업에 `--add-dir`로 주입되는 디렉터리(읽기 전용 컨텍스트). |
| `allowedIPs` | string[] | API 호출을 허용할 IP 주소 또는 CIDR 범위. 비어 있으면 전체 허용. 예: `["192.168.1.0/24", "10.0.0.1"]`. |

---

## Providers

Provider는 Tetora가 agent 작업을 실행하는 방식을 정의합니다. 여러 provider를 설정하고 agent별로 선택할 수 있습니다.

```json
{
  "defaultProvider": "claude",
  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    },
    "openai": {
      "type": "openai-compatible",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "$OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    "claude-api": {
      "type": "claude-api",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-5",
      "maxTokens": 8192,
      "firstTokenTimeout": "60s"
    }
  }
}
```

### `providers` — `ProviderConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `type` | string | 필수 | Provider 타입. `"claude-cli"`, `"openai-compatible"`, `"claude-api"`, `"claude-code"` 중 하나. |
| `path` | string | `""` | 바이너리 경로. `claude-cli` 및 `claude-code` 타입에서 사용. 비어 있으면 `claudePath`로 대체. |
| `baseUrl` | string | `""` | API 기본 URL. `openai-compatible`에 필수. |
| `apiKey` | string | `""` | API 키. `$ENV_VAR` 지원. `claude-api`에 필수; `openai-compatible`에는 선택 사항. |
| `model` | string | `""` | 이 provider의 기본 모델. 이 provider를 사용하는 작업에 대해 `defaultModel`을 오버라이드합니다. |
| `maxTokens` | int | `8192` | 최대 출력 토큰 수(`claude-api`에서 사용). |
| `firstTokenTimeout` | string | `"60s"` | 타임아웃 전 첫 번째 응답 토큰을 기다리는 시간(SSE 스트림). |

**Provider 타입:**
- `claude-cli` — `claude` 바이너리를 서브프로세스로 실행 (기본값, 가장 높은 호환성)
- `claude-api` — HTTP를 사용해 Anthropic API 직접 호출 (`ANTHROPIC_API_KEY` 필요)
- `openai-compatible` — OpenAI 호환 REST API (OpenAI, Ollama, Groq 등)
- `claude-code` — Claude Code CLI 모드 사용

---

## Agents

Agent는 고유한 모델, soul 파일, 도구 접근 권한을 가진 명명된 페르소나를 정의합니다.

```json
{
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "provider": "claude",
      "description": "Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"],
      "permissionMode": "acceptEdits",
      "allowedDirs": ["/Users/me/projects"],
      "trustLevel": "auto"
    }
  }
}
```

### `agents` — `AgentConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `soulFile` | string | 필수 | agent의 SOUL.md 퍼스낼리티 파일 경로. `agentsDir` 기준 상대 경로. |
| `model` | string | `defaultModel` | 이 agent에 사용할 모델. |
| `description` | string | `""` | 사람이 읽을 수 있는 설명. LLM 분류기에서 라우팅에도 사용됩니다. |
| `keywords` | string[] | `[]` | 스마트 디스패치에서 이 agent로의 라우팅을 트리거하는 키워드. |
| `provider` | string | `defaultProvider` | Provider 이름(`providers` 맵의 키). |
| `permissionMode` | string | `defaultPermissionMode` | 이 agent의 Claude 권한 모드. |
| `allowedDirs` | string[] | `allowedDirs` | 이 agent가 접근할 수 있는 파일시스템 경로. 전역 설정을 오버라이드합니다. |
| `docker` | bool\|null | `null` | agent별 Docker 샌드박스 오버라이드. `null` = 전역 `docker.enabled` 상속. |
| `fallbackProviders` | string[] | `[]` | 기본 provider 실패 시 순서대로 시도할 대체 provider 이름 목록. |
| `trustLevel` | string | `"auto"` | 신뢰 레벨: `"observe"` (읽기 전용), `"suggest"` (제안만, 적용 안 함), `"auto"` (완전 자율). |
| `tools` | AgentToolPolicy | `{}` | 도구 접근 정책. [Tool Policy](#tool-policy) 참조. |
| `toolProfile` | string | `"standard"` | 명명된 도구 프로필: `"minimal"`, `"standard"`, `"full"`. |
| `workspace` | WorkspaceConfig | `{}` | 워크스페이스 격리 설정. |

---

## Smart Dispatch

Smart Dispatch는 규칙, 키워드, LLM 분류를 기반으로 들어오는 작업을 가장 적합한 agent에게 자동으로 라우팅합니다.

```json
{
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "review": false,
    "reviewLoop": false,
    "maxRetries": 3,
    "fallback": "smart",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README"]
      }
    ],
    "bindings": [
      {
        "channel": "discord",
        "channelId": "123456789",
        "agent": "engineer"
      }
    ]
  }
}
```

### `smartDispatch` — `SmartDispatchConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 스마트 디스패치 라우팅 활성화. |
| `coordinator` | string | 첫 번째 agent | LLM 기반 작업 분류에 사용할 agent. |
| `defaultAgent` | string | 첫 번째 agent | 어떤 규칙도 일치하지 않을 때의 대체 agent. |
| `classifyBudget` | float64 | `0.1` | 분류 LLM 호출의 비용 예산(USD). |
| `classifyTimeout` | string | `"30s"` | 분류 호출 타임아웃. |
| `review` | bool | `false` | 작업 완료 후 출력에 리뷰 agent를 실행. |
| `reviewLoop` | bool | `false` | Dev↔QA 재시도 루프 활성화: 리뷰 → 피드백 → 재시도 (`maxRetries`까지). |
| `maxRetries` | int | `3` | 리뷰 루프의 최대 QA 재시도 횟수. |
| `reviewAgent` | string | coordinator | 출력 리뷰를 담당하는 agent. 적대적 리뷰를 위해 엄격한 QA agent로 설정하세요. |
| `reviewBudget` | float64 | `0.2` | 리뷰 LLM 호출의 비용 예산(USD). |
| `fallback` | string | `"smart"` | 대체 전략: `"smart"` (LLM 라우팅) 또는 `"coordinator"` (항상 기본 agent). |
| `rules` | RoutingRule[] | `[]` | LLM 분류 전에 평가되는 키워드/정규식 라우팅 규칙. |
| `bindings` | RoutingBinding[] | `[]` | 채널/사용자/길드 → agent 바인딩 (최우선, 가장 먼저 평가됨). |

### `rules` — `RoutingRule`

| 필드 | 타입 | 설명 |
|---|---|---|
| `agent` | string | 대상 agent 이름. |
| `keywords` | string[] | 대소문자 구분 없는 키워드. 하나라도 일치하면 이 agent로 라우팅됩니다. |
| `patterns` | string[] | Go 정규식 패턴. 하나라도 일치하면 이 agent로 라우팅됩니다. |

### `bindings` — `RoutingBinding`

| 필드 | 타입 | 설명 |
|---|---|---|
| `channel` | string | 플랫폼: `"telegram"`, `"discord"`, `"slack"` 등. |
| `userId` | string | 해당 플랫폼의 사용자 ID. |
| `channelId` | string | 채널 또는 채팅 ID. |
| `guildId` | string | 길드/서버 ID (Discord 전용). |
| `agent` | string | 대상 agent 이름. |

---

## Session

다중 턴 상호작용에서 대화 컨텍스트가 유지되고 압축되는 방식을 제어합니다.

```json
{
  "session": {
    "contextMessages": 20,
    "compactAfter": 30,
    "compactKeep": 10,
    "compactTokens": 200000,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku",
      "maxCost": 0.02,
      "provider": "claude"
    }
  }
}
```

### `session` — `SessionConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `contextMessages` | int | `20` | 새 작업에 컨텍스트로 주입할 최근 메시지의 최대 수. |
| `compactAfter` | int | `30` | 메시지 수가 이 값을 초과하면 압축. 지원 중단: `compaction.maxMessages` 사용. |
| `compactKeep` | int | `10` | 압축 후 유지할 최근 메시지 수. 지원 중단: `compaction.compactTo` 사용. |
| `compactTokens` | int | `200000` | 총 입력 토큰이 이 임계값을 초과하면 압축. |

### `session.compaction` — `CompactionConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 자동 session 압축 활성화. |
| `maxMessages` | int | `50` | session이 이 메시지 수를 초과하면 압축 트리거. |
| `compactTo` | int | `10` | 압축 후 유지할 최근 메시지 수. |
| `model` | string | `"haiku"` | 압축 요약 생성에 사용할 LLM 모델. |
| `maxCost` | float64 | `0.02` | 압축 호출당 최대 비용(USD). |
| `provider` | string | `defaultProvider` | 압축 요약 호출에 사용할 provider. |

---

## Task Board

내장 태스크 보드는 작업 항목을 추적하고 자동으로 agent에게 디스패치할 수 있습니다.

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": false,
    "defaultWorkflow": "",
    "gitCommit": false,
    "gitPush": false,
    "gitPR": false,
    "gitWorktree": false,
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": false
    },
    "autoDispatch": {
      "enabled": false,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "stuckThreshold": "2h",
      "reviewLoop": false
    }
  }
}
```

### `taskBoard` — `TaskBoardConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 태스크 보드 활성화. |
| `maxRetries` | int | `3` | 실패로 표시되기 전 작업당 최대 재시도 횟수. |
| `requireReview` | bool | `false` | 품질 게이트: 작업이 완료로 표시되기 전 리뷰를 통과해야 합니다. |
| `defaultWorkflow` | string | `""` | 모든 자동 디스패치 작업에 실행할 workflow 이름. 비어 있으면 workflow 없음. |
| `gitCommit` | bool | `false` | 작업이 완료로 표시될 때 자동 커밋. |
| `gitPush` | bool | `false` | 커밋 후 자동 푸시 (`gitCommit: true` 필요). |
| `gitPR` | bool | `false` | 푸시 후 GitHub PR 자동 생성 (`gitPush: true` 필요). |
| `gitWorktree` | bool | `false` | 작업 격리를 위해 git worktree 사용 (동시 작업 간 파일 충돌 방지). |
| `idleAnalyze` | bool | `false` | 보드가 유휴 상태일 때 자동으로 분석 실행. |
| `problemScan` | bool | `false` | 완료 후 작업 출력에서 잠재적 문제 스캔. |

### `taskBoard.autoDispatch` — `TaskBoardDispatchConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 대기 중인 작업의 자동 폴링 및 디스패치 활성화. |
| `interval` | string | `"5m"` | 준비된 작업을 스캔하는 간격. |
| `maxConcurrentTasks` | int | `3` | 스캔 사이클당 최대 디스패치 작업 수. |
| `defaultModel` | string | `""` | 자동 디스패치 작업의 모델 오버라이드. |
| `maxBudget` | float64 | `0` | 작업당 최대 비용(USD). `0` = 제한 없음. |
| `defaultAgent` | string | `""` | 미할당 작업의 대체 agent. |
| `backlogAgent` | string | `""` | 백로그 트리아지를 담당하는 agent. |
| `reviewAgent` | string | `""` | 완료된 작업을 리뷰하는 agent. |
| `escalateAssignee` | string | `""` | 리뷰 거절된 작업을 이 사용자에게 할당. |
| `stuckThreshold` | string | `"2h"` | "doing" 상태가 이 시간을 초과하면 "todo"로 재설정. |
| `backlogTriageInterval` | string | `"1h"` | 백로그 트리아지 실행 간격. |
| `reviewLoop` | bool | `false` | 디스패치된 작업에 대한 자동화된 Dev↔QA 루프 활성화. |

### `taskBoard.gitWorkflow` — `GitWorkflowConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `branchConvention` | string | `"{type}/{agent}-{description}"` | 브랜치 이름 템플릿. 변수: `{type}`, `{agent}`, `{description}`. |
| `types` | string[] | `["feat","fix","refactor","chore"]` | 허용되는 브랜치 타입 접두사. |
| `defaultType` | string | `"feat"` | 타입이 지정되지 않을 때의 대체 타입. |
| `autoMerge` | bool | `false` | 작업 완료 시 main으로 자동 머지 (`gitWorktree: true`일 때만). |

---

## Slot Pressure

`maxConcurrent` 슬롯 한도에 근접했을 때 시스템 동작을 제어합니다. 인터랙티브(사람이 시작한) 세션은 예약된 슬롯을 사용하고, 백그라운드 작업은 대기합니다.

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m",
    "monitorEnabled": false,
    "monitorInterval": "30s"
  }
}
```

### `slotPressure` — `SlotPressureConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 슬롯 압력 관리 활성화. |
| `reservedSlots` | int | `2` | 인터랙티브 세션을 위해 예약된 슬롯. 백그라운드 작업은 이 슬롯을 사용할 수 없습니다. |
| `warnThreshold` | int | `3` | 사용 가능한 슬롯이 이 수보다 적을 때 사용자에게 경고. |
| `nonInteractiveTimeout` | string | `"5m"` | 백그라운드 작업이 타임아웃 전 슬롯을 기다리는 시간. |
| `pollInterval` | string | `"2s"` | 백그라운드 작업이 여유 슬롯을 확인하는 간격. |
| `monitorEnabled` | bool | `false` | 알림 채널을 통한 사전 슬롯 압력 알림 활성화. |
| `monitorInterval` | string | `"30s"` | 압력 알림 확인 및 발송 간격. |

---

## Workflows

Workflow는 디렉터리 내 YAML 파일로 정의됩니다. `workflowDir`은 해당 디렉터리를 가리키고, variables는 기본 템플릿 값을 제공합니다.

```json
{
  "workflowDir": "~/.tetora/workspace/workflows/",
  "workflowTriggers": [
    {
      "event": "task.done",
      "workflow": "notify-slack",
      "filter": {"status": "done"}
    }
  ]
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `workflowDir` | string | `~/.tetora/workspace/workflows/` | workflow YAML 파일이 저장된 디렉터리. |
| `workflowTriggers` | WorkflowTriggerConfig[] | `[]` | 시스템 이벤트에 대한 자동 workflow 트리거. |

---

## 연동

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 123456789,
    "pollTimeout": 30
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | Telegram 봇 활성화. |
| `botToken` | string | `""` | @BotFather의 Telegram 봇 토큰. `$ENV_VAR` 지원. |
| `chatID` | int64 | `0` | 알림을 보낼 Telegram 채팅 또는 그룹 ID. |
| `pollTimeout` | int | `30` | 메시지 수신을 위한 롱폴 타임아웃(초). |

### Discord

```json
{
  "discord": {
    "enabled": true,
    "botToken": "$DISCORD_BOT_TOKEN",
    "guildID": "123456789",
    "channelIDs": ["111111111"],
    "mentionChannelIDs": ["222222222"],
    "notifyChannelID": "333333333",
    "showProgress": true,
    "routes": {
      "111111111": {"agent": "engineer"}
    }
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | Discord 봇 활성화. |
| `botToken` | string | `""` | Discord 봇 토큰. `$ENV_VAR` 지원. |
| `guildID` | string | `""` | 특정 Discord 서버(길드)로 제한. |
| `channelIDs` | string[] | `[]` | 봇이 모든 메시지에 응답하는 채널 ID (`@` 멘션 불필요). |
| `mentionChannelIDs` | string[] | `[]` | 봇이 `@` 멘션 시에만 응답하는 채널 ID. |
| `notifyChannelID` | string | `""` | 작업 완료 알림용 채널 (작업당 스레드 생성). |
| `showProgress` | bool | `true` | Discord에 라이브 "Working..." 스트리밍 메시지 표시. |
| `webhooks` | map[string]string | `{}` | 외부 발신 전용 알림을 위한 명명된 webhook URL. |
| `routes` | map[string]DiscordRouteConfig | `{}` | 채널별 라우팅을 위한 채널 ID → agent 이름 맵. |

### Slack

```json
{
  "slack": {
    "enabled": true,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET",
    "appToken": "$SLACK_APP_TOKEN",
    "defaultChannel": "C0123456789"
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | Slack 봇 활성화. |
| `botToken` | string | `""` | Slack 봇 OAuth 토큰 (`xoxb-...`). `$ENV_VAR` 지원. |
| `signingSecret` | string | `""` | 요청 검증을 위한 Slack 서명 시크릿. `$ENV_VAR` 지원. |
| `appToken` | string | `""` | Socket Mode용 Slack 앱 레벨 토큰 (`xapp-...`). 선택 사항. `$ENV_VAR` 지원. |
| `defaultChannel` | string | `""` | 외부 발신 알림의 기본 채널 ID. |

### 외부 발신 Webhooks

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/tetora",
      "headers": {"Authorization": "$WEBHOOK_TOKEN"},
      "events": ["success", "error"]
    }
  ]
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `url` | string | 필수 | Webhook 엔드포인트 URL. |
| `headers` | map[string]string | `{}` | 포함할 HTTP 헤더. 값은 `$ENV_VAR` 지원. |
| `events` | string[] | 전체 | 전송할 이벤트: `"success"`, `"error"`, `"timeout"`, `"all"`. 비어 있으면 전체. |

### 수신 Webhooks

수신 webhook을 사용하면 외부 서비스가 HTTP POST를 통해 Tetora 작업을 트리거할 수 있습니다.

```json
{
  "incomingWebhooks": {
    "github": {
      "secret": "$GITHUB_WEBHOOK_SECRET",
      "agent": "engineer",
      "prompt": "A GitHub event occurred: {{.Body}}"
    }
  }
}
```

### 알림 채널

작업 이벤트를 다른 Slack/Discord 엔드포인트로 라우팅하기 위한 명명된 알림 채널.

```json
{
  "notifications": [
    {
      "name": "alerts",
      "type": "slack",
      "webhookUrl": "$SLACK_ALERTS_WEBHOOK",
      "events": ["error"],
      "minPriority": "high"
    }
  ]
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `name` | string | `""` | 작업 `channel` 필드에서 사용되는 명명된 참조 (예: `"discord:alerts"`). |
| `type` | string | 필수 | `"slack"` 또는 `"discord"`. |
| `webhookUrl` | string | 필수 | Webhook URL. `$ENV_VAR` 지원. |
| `events` | string[] | 전체 | 이벤트 타입 필터: `"all"`, `"error"`, `"success"`. |
| `minPriority` | string | 전체 | 최소 우선순위: `"critical"`, `"high"`, `"normal"`, `"low"`. |

---

## Store (템플릿 마켓플레이스)

```json
{
  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1",
    "authToken": "$TETORA_STORE_TOKEN"
  }
}
```

### `store` — `StoreConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 템플릿 스토어 활성화. |
| `registryUrl` | string | `"https://registry.tetora.dev/v1"` | 템플릿 검색 및 설치를 위한 원격 레지스트리 URL. |
| `authToken` | string | `""` | 레지스트리 인증 토큰. `$ENV_VAR` 지원. |

---

## 비용 및 알림

### `costAlert` — `CostAlertConfig`

```json
{
  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "dailyTokenLimit": 1000000,
    "action": "warn"
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `dailyLimit` | float64 | `0` | 일일 지출 한도(USD). `0` = 제한 없음. |
| `weeklyLimit` | float64 | `0` | 주간 지출 한도(USD). `0` = 제한 없음. |
| `dailyTokenLimit` | int | `0` | 일일 총 토큰 한도 (입력 + 출력). `0` = 제한 없음. |
| `action` | string | `"warn"` | 한도 초과 시 동작: `"warn"` (로그 및 알림) 또는 `"pause"` (새 작업 차단). |

### `estimate` — `EstimateConfig`

작업 실행 전 사전 비용 추정.

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `confirmThreshold` | float64 | `1.00` | 추정 비용이 이 USD 값을 초과하면 확인 요청. |
| `defaultOutputTokens` | int | `500` | 실제 사용량을 알 수 없을 때의 대체 출력 토큰 추정값. |

### `budgets` — `BudgetConfig`

Agent 레벨 및 팀 레벨 비용 예산.

---

## 로깅

```json
{
  "logging": {
    "level": "info",
    "format": "text",
    "file": "~/.tetora/runtime/logs/tetora.log",
    "maxSizeMB": 50,
    "maxFiles": 5
  }
}
```

### `logging` — `LoggingConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `level` | string | `"info"` | 로그 레벨: `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `format` | string | `"text"` | 로그 형식: `"text"` (사람이 읽기 쉬운) 또는 `"json"` (구조화됨). |
| `file` | string | `runtime/logs/tetora.log` | 로그 파일 경로. runtime 디렉터리 기준 상대 경로. |
| `maxSizeMB` | int | `50` | 로테이션 전 최대 로그 파일 크기(MB). |
| `maxFiles` | int | `5` | 유지할 로테이션된 로그 파일 수. |

---

## 보안

### `dashboardAuth` — `DashboardAuthConfig`

```json
{
  "dashboardAuth": {
    "enabled": true,
    "username": "admin",
    "password": "$DASHBOARD_PASSWORD"
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 대시보드에 HTTP Basic Auth 활성화. |
| `username` | string | `"admin"` | Basic auth 사용자 이름. |
| `password` | string | `""` | Basic auth 비밀번호. `$ENV_VAR` 지원. |
| `token` | string | `""` | 대안: 쿠키로 전달되는 정적 토큰. |

### `tls` — `TLSConfig`

```json
{
  "tls": {
    "certFile": "/etc/tetora/cert.pem",
    "keyFile": "/etc/tetora/key.pem"
  }
}
```

| 필드 | 타입 | 설명 |
|---|---|---|
| `certFile` | string | TLS 인증서 PEM 파일 경로. 설정 시 (`keyFile`과 함께) HTTPS를 활성화합니다. |
| `keyFile` | string | TLS 개인 키 PEM 파일 경로. |

### `rateLimit` — `RateLimitConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | IP별 요청 속도 제한 활성화. |
| `maxPerMin` | int | `60` | IP당 분당 최대 API 요청 수. |

### `securityAlert` — `SecurityAlertConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 반복적인 인증 실패 시 보안 알림 활성화. |
| `failThreshold` | int | `10` | 알림 발송 전 창 내 실패 횟수. |
| `failWindowMin` | int | `5` | 슬라이딩 윈도우(분). |

### `approvalGates` — `ApprovalGateConfig`

특정 도구 실행 전 사람의 승인을 요구합니다.

```json
{
  "approvalGates": {
    "enabled": true,
    "timeout": 120,
    "tools": ["bash", "write_file"],
    "autoApproveTools": ["read_file"]
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 승인 게이트 활성화. |
| `timeout` | int | `120` | 취소 전 승인을 기다리는 시간(초). |
| `tools` | string[] | `[]` | 실행 전 승인이 필요한 도구 이름. |
| `autoApproveTools` | string[] | `[]` | 시작 시 사전 승인된 도구 (절대 프롬프트하지 않음). |

---

## 안정성

### `circuitBreaker` — `CircuitBreakerConfig`

```json
{
  "circuitBreaker": {
    "enabled": true,
    "failThreshold": 5,
    "successThreshold": 2,
    "openTimeout": "30s"
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `true` | provider 페일오버를 위한 회로 차단기 활성화. |
| `failThreshold` | int | `5` | 회로를 열기 전 연속 실패 횟수. |
| `successThreshold` | int | `2` | 닫기 전 반개방 상태에서의 성공 횟수. |
| `openTimeout` | string | `"30s"` | 재시도 전(반개방) 개방 상태 유지 시간. |

### `fallbackProviders`

```json
{
  "fallbackProviders": ["claude", "openai"]
}
```

기본 provider 실패 시의 전역 순서 대체 provider 목록.

### `heartbeat` — `HeartbeatConfig`

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": "30s",
    "stallThreshold": "5m",
    "timeoutWarnRatio": 0.8,
    "autoCancel": false,
    "notifyOnStall": true
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | agent 하트비트 모니터링 활성화. |
| `interval` | string | `"30s"` | 실행 중인 작업의 중단 여부 확인 간격. |
| `stallThreshold` | string | `"5m"` | 이 시간 동안 출력 없음 = 작업 중단됨. |
| `timeoutWarnRatio` | float64 | `0.8` | 경과 시간이 작업 타임아웃의 이 비율을 초과하면 경고. |
| `autoCancel` | bool | `false` | `2x stallThreshold`보다 오래 중단된 작업 자동 취소. |
| `notifyOnStall` | bool | `true` | 작업이 중단으로 감지되면 알림 전송. |

### `retention` — `RetentionConfig`

오래된 데이터의 자동 정리를 제어합니다.

```json
{
  "retention": {
    "history": 90,
    "sessions": 30,
    "auditLog": 365,
    "logs": 14,
    "workflows": 90,
    "outputs": 30
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `history` | int | `90` | 작업 실행 이력 보존 일수. |
| `sessions` | int | `30` | session 데이터 보존 일수. |
| `auditLog` | int | `365` | 감사 로그 항목 보존 일수. |
| `logs` | int | `14` | 로그 파일 보존 일수. |
| `workflows` | int | `90` | workflow 실행 기록 보존 일수. |
| `reflections` | int | `60` | 반성(reflection) 기록 보존 일수. |
| `sla` | int | `90` | SLA 확인 기록 보존 일수. |
| `trustEvents` | int | `90` | 신뢰 이벤트 기록 보존 일수. |
| `handoffs` | int | `60` | agent 핸드오프/메시지 기록 보존 일수. |
| `queue` | int | `7` | 오프라인 큐 항목 보존 일수. |
| `versions` | int | `180` | 설정 버전 스냅샷 보존 일수. |
| `outputs` | int | `30` | agent 출력 파일 보존 일수. |
| `uploads` | int | `7` | 업로드된 파일 보존 일수. |
| `memory` | int | `30` | 오래된 메모리 항목이 보관되기 전 일수. |
| `claudeSessions` | int | `3` | Claude CLI session 아티팩트 보존 일수. |
| `piiPatterns` | string[] | `[]` | 저장된 콘텐츠에서 PII 제거를 위한 정규식 패턴. |

---

## Quiet Hours 및 Digest

```json
{
  "quietHours": {
    "enabled": true,
    "start": "23:00",
    "end": "08:00",
    "tz": "Asia/Taipei",
    "digest": true
  },
  "digest": {
    "enabled": true,
    "time": "08:00",
    "tz": "Asia/Taipei"
  }
}
```

### `quietHours` — `QuietHoursConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | Quiet hours 활성화. 이 시간 동안 알림이 억제됩니다. |
| `start` | string | `""` | 조용한 시간 시작(현지 시간, `"HH:MM"` 형식). |
| `end` | string | `""` | 조용한 시간 종료(현지 시간). |
| `tz` | string | 현지 | 시간대. 예: `"Asia/Taipei"`, `"UTC"`. |
| `digest` | bool | `false` | quiet hours 종료 시 억제된 알림의 digest 전송. |

### `digest` — `DigestConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 예약된 일일 digest 활성화. |
| `time` | string | `"08:00"` | digest 전송 시각(`"HH:MM"`). |
| `tz` | string | 현지 | 시간대. |

---

## Tools

```json
{
  "tools": {
    "maxIterations": 10,
    "timeout": 120,
    "toolOutputLimit": 10240,
    "toolTimeout": 30,
    "defaultProfile": "standard",
    "builtin": {
      "bash": true,
      "web_search": false
    },
    "webSearch": {
      "provider": "brave",
      "apiKey": "$BRAVE_API_KEY",
      "maxResults": 5
    },
    "vision": {
      "provider": "anthropic",
      "apiKey": "$ANTHROPIC_API_KEY",
      "model": "claude-opus-4-5"
    }
  }
}
```

### `tools` — `ToolConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `maxIterations` | int | `10` | 작업당 최대 도구 호출 반복 횟수. |
| `timeout` | int | `120` | 전역 도구 엔진 타임아웃(초). |
| `toolOutputLimit` | int | `10240` | 도구 출력당 최대 문자 수 (이 값을 초과하면 잘립니다). |
| `toolTimeout` | int | `30` | 도구별 실행 타임아웃(초). |
| `defaultProfile` | string | `"standard"` | 기본 도구 프로필 이름. |
| `builtin` | map[string]bool | `{}` | 이름으로 내장 도구 개별 활성화/비활성화. |
| `profiles` | map[string]ToolProfile | `{}` | 사용자 정의 도구 프로필. |
| `trustOverride` | map[string]string | `{}` | 도구 이름별 신뢰 레벨 오버라이드. |

### `tools.webSearch` — `WebSearchConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `provider` | string | `""` | 검색 provider: `"brave"`, `"tavily"`, `"searxng"`. |
| `apiKey` | string | `""` | Provider의 API 키. `$ENV_VAR` 지원. |
| `baseURL` | string | `""` | 사용자 정의 엔드포인트 (자체 호스팅 searxng용). |
| `maxResults` | int | `5` | 반환할 최대 검색 결과 수. |

### `tools.vision` — `VisionConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `provider` | string | `""` | Vision provider: `"anthropic"`, `"openai"`, `"google"`. |
| `apiKey` | string | `""` | API 키. `$ENV_VAR` 지원. |
| `model` | string | `""` | Vision provider의 모델 이름. |
| `maxImageSize` | int | `5242880` | 최대 이미지 크기(바이트, 기본값 5 MB). |
| `baseURL` | string | `""` | 사용자 정의 API 엔드포인트. |

---

## MCP (Model Context Protocol)

### `mcpConfigs`

명명된 MCP 서버 설정. 각 키는 MCP 설정 이름이고, 값은 전체 MCP JSON 설정입니다. Tetora는 이를 임시 파일에 쓰고 `--mcp-config`를 통해 claude 바이너리에 전달합니다.

```json
{
  "mcpConfigs": {
    "playwright": {
      "mcpServers": {
        "playwright": {
          "command": "npx",
          "args": ["@playwright/mcp@latest"]
        }
      }
    }
  }
}
```

### `mcpServers`

Tetora가 직접 관리하는 간소화된 MCP 서버 정의.

```json
{
  "mcpServers": {
    "my-server": {
      "command": "python",
      "args": ["/path/to/server.py"],
      "env": {"API_KEY": "$MY_API_KEY"},
      "enabled": true
    }
  }
}
```

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `command` | string | 필수 | 실행 가능한 명령어. |
| `args` | string[] | `[]` | 명령어 인수. |
| `env` | map[string]string | `{}` | 프로세스의 환경 변수. 값은 `$ENV_VAR` 지원. |
| `enabled` | bool | `true` | 이 MCP 서버의 활성화 여부. |

---

## Prompt Budget

시스템 프롬프트의 각 섹션에 대한 최대 문자 예산을 제어합니다. 프롬프트가 예기치 않게 잘리는 경우 조정하세요.

```json
{
  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "skillsMax": 4000,
    "maxSkillsPerTask": 3,
    "contextMax": 16000,
    "totalMax": 40000
  }
}
```

### `promptBudget` — `PromptBudgetConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `soulMax` | int | `8000` | agent soul/퍼스낼리티 프롬프트의 최대 문자 수. |
| `rulesMax` | int | `4000` | 워크스페이스 규칙의 최대 문자 수. |
| `knowledgeMax` | int | `8000` | 지식 베이스 콘텐츠의 최대 문자 수. |
| `skillsMax` | int | `4000` | 주입된 skill의 최대 문자 수. |
| `maxSkillsPerTask` | int | `3` | 작업당 주입되는 최대 skill 수. |
| `contextMax` | int | `16000` | session 컨텍스트의 최대 문자 수. |
| `totalMax` | int | `40000` | 총 시스템 프롬프트 크기(전체 섹션 합산)의 하드 캡. |

---

## Agent 통신

중첩된 서브 agent 디스패치(agent_dispatch 도구)를 제어합니다.

```json
{
  "agentComm": {
    "enabled": true,
    "maxConcurrent": 3,
    "defaultTimeout": 900,
    "maxDepth": 3,
    "maxChildrenPerTask": 5
  }
}
```

### `agentComm` — `AgentCommConfig`

| 필드 | 타입 | 기본값 | 설명 |
|---|---|---|---|
| `enabled` | bool | `false` | 중첩된 서브 agent 호출을 위한 `agent_dispatch` 도구 활성화. |
| `maxConcurrent` | int | `3` | 전역 최대 동시 `agent_dispatch` 호출 수. |
| `defaultTimeout` | int | `900` | 기본 서브 agent 타임아웃(초). |
| `maxDepth` | int | `3` | 서브 agent의 최대 중첩 깊이. |
| `maxChildrenPerTask` | int | `5` | 부모 작업당 최대 동시 자식 agent 수. |

---

## 예제

### 최소 설정

Claude CLI provider로 시작하기 위한 최소 설정:

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 3,
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "General-purpose agent."
    }
  }
}
```

### Smart Dispatch가 포함된 멀티 Agent 설정

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",
  "defaultWorkdir": "~/workspace",
  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator. Handles planning, research, and coordination.",
      "keywords": ["plan", "research", "coordinate", "summarize"]
    },
    "engineer": {
      "soulFile": "team/engineer/SOUL.md",
      "model": "sonnet",
      "description": "Engineer. Handles coding, debugging, and infrastructure.",
      "keywords": ["code", "debug", "deploy"]
    },
    "creator": {
      "soulFile": "team/creator/SOUL.md",
      "model": "sonnet",
      "description": "Creator. Handles writing, documentation, and content.",
      "keywords": ["write", "blog", "translate"]
    }
  },
  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "classifyBudget": 0.1,
    "classifyTimeout": "30s",
    "rules": [
      {
        "agent": "engineer",
        "keywords": ["bug", "error", "deploy", "CI/CD", "docker"],
        "patterns": ["(?:fix|resolve)\\s+(?:bug|issue|error)"]
      },
      {
        "agent": "creator",
        "keywords": ["blog post", "documentation", "README", "translation"]
      }
    ]
  },
  "costAlert": {
    "dailyLimit": 10.0,
    "action": "warn"
  },
  "logging": {
    "level": "info",
    "format": "text"
  }
}
```

### 전체 설정 (주요 섹션 모두 포함)

```json
{
  "claudePath": "/usr/local/bin/claude",
  "maxConcurrent": 5,
  "defaultModel": "sonnet",
  "defaultTimeout": "30m",
  "defaultBudget": 2.0,
  "defaultPermissionMode": "acceptEdits",
  "listenAddr": "127.0.0.1:8991",
  "apiToken": "$TETORA_API_TOKEN",

  "providers": {
    "claude": {
      "type": "claude-cli",
      "path": "/usr/local/bin/claude"
    }
  },

  "agents": {
    "coordinator": {
      "soulFile": "SOUL.md",
      "model": "sonnet",
      "description": "Coordinator and general-purpose agent."
    }
  },

  "smartDispatch": {
    "enabled": true,
    "coordinator": "coordinator",
    "defaultAgent": "coordinator",
    "rules": []
  },

  "session": {
    "contextMessages": 20,
    "compaction": {
      "enabled": true,
      "maxMessages": 50,
      "compactTo": 10,
      "model": "haiku"
    }
  },

  "taskBoard": {
    "enabled": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3
    },
    "gitCommit": true,
    "gitPush": false
  },

  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  },

  "telegram": {
    "enabled": false,
    "botToken": "$TELEGRAM_BOT_TOKEN",
    "chatID": 0,
    "pollTimeout": 30
  },

  "discord": {
    "enabled": false,
    "botToken": "$DISCORD_BOT_TOKEN"
  },

  "slack": {
    "enabled": false,
    "botToken": "$SLACK_BOT_TOKEN",
    "signingSecret": "$SLACK_SIGNING_SECRET"
  },

  "store": {
    "enabled": true,
    "registryUrl": "https://registry.tetora.dev/v1"
  },

  "costAlert": {
    "dailyLimit": 10.0,
    "weeklyLimit": 50.0,
    "action": "warn"
  },

  "logging": {
    "level": "info",
    "format": "text",
    "maxSizeMB": 50,
    "maxFiles": 5
  },

  "retention": {
    "history": 90,
    "sessions": 30,
    "logs": 14
  },

  "heartbeat": {
    "enabled": true,
    "stallThreshold": "5m",
    "autoCancel": false
  },

  "dashboardAuth": {
    "enabled": false
  },

  "promptBudget": {
    "soulMax": 8000,
    "rulesMax": 4000,
    "knowledgeMax": 8000,
    "totalMax": 40000
  }
}
```
