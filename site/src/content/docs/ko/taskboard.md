---
title: "Taskboard & Auto-Dispatch 가이드"
lang: "ko"
order: 4
description: "Track tasks, priorities, and agent assignments with the built-in taskboard."
---
# Taskboard & Auto-Dispatch 가이드

## 개요

Taskboard는 작업을 추적하고 자동으로 실행하기 위한 Tetora의 내장 칸반 시스템입니다. 영속적인 작업 저장소(SQLite 기반)와 준비된 작업을 감시해 사람의 개입 없이 agent에게 넘겨주는 auto-dispatch 엔진이 결합되어 있습니다.

주요 활용 사례:

- 엔지니어링 작업 백로그를 쌓아두고 agent들이 밤새 처리하도록 하기
- 전문성에 따라 특정 agent에게 작업 라우팅 (예: `kokuyou`는 백엔드, `kohaku`는 콘텐츠)
- 의존 관계로 작업을 연결하여 agent들이 서로 이어서 작업하도록 하기
- git과 작업 실행 연동: 자동 브랜치 생성, 커밋, 푸시, PR/MR

**요구 사항:** `config.json`에 `taskBoard.enabled: true` 설정 및 Tetora 데몬 실행 중.

---

## 작업 라이프사이클

작업은 다음 순서로 상태가 변경됩니다:

```
idea → needs-thought → backlog → todo → doing → review → done
                                                  ↓
                                           partial-done
                                                  ↓
                                              failed
```

| 상태 | 의미 |
|---|---|
| `idea` | 아직 구체화되지 않은 대략적인 아이디어 |
| `needs-thought` | 구현 전 분석 또는 설계가 필요한 작업 |
| `backlog` | 정의되고 우선순위가 지정되었지만 아직 예약되지 않음 |
| `todo` | 실행 준비 완료 — 담당자가 설정되어 있으면 auto-dispatch가 처리 |
| `doing` | 현재 실행 중 |
| `review` | 실행 완료, 품질 검토 대기 중 |
| `done` | 완료 및 검토됨 |
| `partial-done` | 실행은 성공했지만 후처리 실패 (예: git 머지 충돌). 복구 가능. |
| `failed` | 실행 실패 또는 빈 출력. `maxRetries`까지 재시도됩니다. |

Auto-dispatch는 `status=todo`인 작업을 처리합니다. 작업에 담당자가 없으면 자동으로 `defaultAgent`(기본값: `ruri`)에게 할당됩니다. `backlog` 상태의 작업은 설정된 `backlogAgent`(기본값: `ruri`)가 주기적으로 트리아지하여 유망한 작업을 `todo`로 이동시킵니다.

---

## 작업 생성

### CLI

```bash
# 최소 작업 (백로그에 들어가며 미할당)
tetora task create --title="Add rate limiting to API"

# 모든 옵션 포함
tetora task create \
  --title="Refactor auth middleware" \
  --description="Split token validation into its own package. See ADR-14." \
  --priority=high \
  --assignee=kokuyou \
  --type=refactor

# 작업 목록 조회
tetora task list
tetora task list --status=todo
tetora task list --assignee=kokuyou
tetora task list --project=api-v2

# 특정 작업 표시
tetora task show task-abc123
tetora task show task-abc123 --full   # 댓글/스레드 포함

# 수동으로 작업 이동
tetora task move task-abc123 --status=todo

# agent에게 할당
tetora task assign task-abc123 --assignee=kokuyou

# 댓글 추가 (spec, context, log, system 타입)
tetora task comment task-abc123 \
  --author=takuma \
  --content="Must pass existing test suite. Do not touch auth.go." \
  --type=spec
```

작업 ID는 `task-<uuid>` 형식으로 자동 생성됩니다. 전체 ID 또는 짧은 접두사로 작업을 참조할 수 있으며, CLI가 일치하는 항목을 제안합니다.

### HTTP API

```bash
# 생성
curl -X POST http://localhost:8991/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Add rate limiting",
    "description": "Implement token bucket per API key",
    "priority": "high",
    "assignee": "kokuyou",
    "type": "feat"
  }'

# 목록 (상태 필터)
curl "http://localhost:8991/api/tasks?status=todo"

# 새 상태로 이동
curl -X PATCH http://localhost:8991/api/tasks/task-abc123 \
  -H "Content-Type: application/json" \
  -d '{"status": "todo"}'
```

### 대시보드

대시보드(`http://localhost:8991/dashboard`)의 **Taskboard** 탭을 여세요. 작업은 칸반 컬럼에 표시됩니다. 카드를 컬럼 간에 드래그하여 상태를 변경하거나 카드를 클릭하면 댓글과 diff 뷰가 있는 상세 패널이 열립니다.

---

## Auto-Dispatch

Auto-dispatch는 `todo` 작업을 처리하여 agent를 통해 실행하는 백그라운드 루프입니다.

### 동작 방식

1. `interval`(기본값: `5m`)마다 타이커가 발생합니다.
2. 현재 실행 중인 작업 수를 확인합니다. `activeCount >= maxConcurrentTasks`이면 스캔을 건너뜁니다.
3. 담당자가 있는 각 `todo` 작업을 해당 agent에게 디스패치합니다. 미할당 작업은 자동으로 `defaultAgent`에게 할당됩니다.
4. 작업이 완료되면 전체 인터벌을 기다리지 않고 즉시 재스캔이 시작됩니다.
5. 데몬 시작 시, 이전 크래시에서 고아가 된 `doing` 작업은 완료 증거가 있으면 `done`으로 복원되거나 진짜 고아이면 `todo`로 재설정됩니다.

### 디스패치 흐름

```
                          ┌─────────┐
                          │  idea   │  (수동 개념 입력)
                          └────┬────┘
                               ▼
                       ┌──────────────┐
                       │ needs-thought │  (분석 필요)
                       └───────┬──────┘
                               ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       backlog                             │
  │                                                           │
  │  트리아지 (backlogAgent, 기본값: ruri)가 주기적으로 실행:  │
  │   • "ready"     → agent 할당 → todo로 이동                │
  │   • "decompose" → 하위 작업 생성 → 부모를 doing으로       │
  │   • "clarify"   → 질문 댓글 추가 → backlog 유지           │
  │                                                           │
  │  빠른 경로: 이미 담당자 있음 + 차단 의존성 없음            │
  │   → LLM 트리아지 건너뛰고 직접 todo로 이동                │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                        todo                               │
  │                                                           │
  │  Auto-dispatch가 매 스캔 사이클마다 작업 처리:             │
  │   • 담당자 있음       → 해당 agent에게 디스패치            │
  │   • 담당자 없음       → defaultAgent 할당 후 실행          │
  │   • workflow 있음     → workflow 파이프라인으로 실행       │
  │   • dependsOn 있음    → 의존성이 완료될 때까지 대기        │
  │   • 재개 가능한 이전 실행 → 체크포인트에서 재개            │
  └──────────────────────┬───────────────────────────────────┘
                         ▼
  ┌──────────────────────────────────────────────────────────┐
  │                       doing                               │
  │                                                           │
  │  Agent가 작업 실행 (단일 프롬프트 또는 workflow DAG)       │
  │                                                           │
  │  가드: stuckThreshold (기본값 2h)                         │
  │   • workflow가 여전히 실행 중 → 타임스탬프 갱신           │
  │   • 진짜 stuck → todo로 재설정                            │
  └────────┬──────────┬──────────┬──────────────────────────┘
           │          │          │
        성공      부분 실패     실패
           │          │          │
           ▼          ▼          ▼
       ┌────────┐ ┌──────────┐ ┌────────┐
       │ review │ │ partial- │ │ failed │
       │        │ │   done   │ │        │
       └───┬────┘ └────┬─────┘ └───┬────┘
           │           │           │
           │     대시보드의        │  재시도 (maxRetries까지)
           │     Resume 버튼       │  또는 에스컬레이션
           ▼                       ▼
       ┌────────┐            ┌──────────┐
       │  done  │            │ escalate │
       └────────┘            │ to human │
                             └──────────┘
```

### 트리아지 상세

트리아지는 `backlogTriageInterval`(기본값: `1h`)마다 실행되며 `backlogAgent`(기본값: `ruri`)가 수행합니다. Agent는 각 백로그 작업과 댓글, 사용 가능한 agent 명단을 받아 다음 중 하나를 결정합니다:

| 액션 | 효과 |
|---|---|
| `ready` | 특정 agent를 할당하고 `todo`로 이동 |
| `decompose` | 하위 작업 생성 (담당자 포함), 부모는 `doing`으로 이동 |
| `clarify` | 댓글로 질문 추가, 작업은 `backlog`에 유지 |

**빠른 경로**: 이미 담당자가 있고 차단 의존성이 없는 작업은 LLM 트리아지를 완전히 건너뛰고 즉시 `todo`로 이동합니다.

### 자동 할당

`todo` 작업에 담당자가 없으면 dispatcher가 자동으로 `defaultAgent`(설정 가능, 기본값: `ruri`)에게 할당합니다. 이렇게 하면 작업이 조용히 멈추는 것을 방지합니다. 일반적인 흐름:

1. 담당자 없이 작업 생성 → `backlog` 진입
2. 트리아지가 `todo`로 이동 (agent 할당 여부와 무관)
3. 트리아지가 할당하지 않았으면 → dispatcher가 `defaultAgent` 할당
4. 작업이 정상적으로 실행됨

### 설정

`config.json`에 추가:

```json
{
  "taskBoard": {
    "enabled": true,
    "maxRetries": 3,
    "requireReview": true,
    "defaultWorkflow": "",
    "gitCommit": true,
    "gitPush": true,
    "gitPR": true,
    "gitWorktree": true,
    "autoDispatch": {
      "enabled": true,
      "interval": "5m",
      "maxConcurrentTasks": 3,
      "defaultAgent": "kokuyou",
      "backlogAgent": "ruri",
      "reviewAgent": "ruri",
      "escalateAssignee": "takuma",
      "stuckThreshold": "2h",
      "backlogTriageInterval": "1h",
      "reviewLoop": false,
      "maxBudget": 5.0,
      "defaultModel": ""
    }
  }
}
```

| 필드 | 기본값 | 설명 |
|---|---|---|
| `enabled` | `false` | Auto-dispatch 루프 활성화 |
| `interval` | `5m` | 준비된 작업 스캔 간격 |
| `maxConcurrentTasks` | `3` | 동시 실행 최대 작업 수 |
| `defaultAgent` | `ruri` | 디스패치 전 미할당 `todo` 작업에 자동 할당 |
| `backlogAgent` | `ruri` | 백로그 작업을 검토하고 이동시키는 agent |
| `reviewAgent` | `ruri` | 완료된 작업 출력을 검토하는 agent |
| `escalateAssignee` | `takuma` | 자동 검토가 사람의 판단을 요청할 때 할당될 사람 |
| `stuckThreshold` | `2h` | 작업이 `doing` 상태로 있을 수 있는 최대 시간 |
| `backlogTriageInterval` | `1h` | 백로그 트리아지 실행 최소 간격 |
| `reviewLoop` | `false` | Dev↔QA 루프 활성화 (실행 → 검토 → 수정, `maxRetries`까지) |
| `maxBudget` | 제한 없음 | 작업당 최대 비용(USD) |
| `defaultModel` | — | 모든 자동 디스패치 작업에 대한 모델 오버라이드 |

---

## Slot Pressure

Slot pressure는 auto-dispatch가 모든 동시성 슬롯을 소모하여 인터랙티브 세션(사람이 보낸 채팅 메시지, 요청 시 디스패치)을 굶기는 것을 방지합니다.

`config.json`에서 활성화:

```json
{
  "slotPressure": {
    "enabled": true,
    "reservedSlots": 2,
    "warnThreshold": 3,
    "nonInteractiveTimeout": "5m"
  }
}
```

| 필드 | 기본값 | 설명 |
|---|---|---|
| `reservedSlots` | `2` | 인터랙티브용으로 예약된 슬롯. 사용 가능한 슬롯이 이 수준으로 떨어지면 비인터랙티브 작업은 대기해야 합니다. |
| `warnThreshold` | `3` | 사용 가능한 슬롯이 이 수준으로 떨어지면 경고가 발생합니다. "排程接近滿載" 메시지가 대시보드와 알림 채널에 나타납니다. |
| `nonInteractiveTimeout` | `5m` | 비인터랙티브 작업이 취소되기 전 슬롯을 기다리는 시간. |

인터랙티브 소스 (사람의 채팅, `tetora dispatch`, `tetora route`)는 항상 즉시 슬롯을 확보합니다. 백그라운드 소스 (taskboard, cron)는 압력이 높으면 대기합니다.

---

## Git 연동

`gitCommit`, `gitPush`, `gitPR`이 활성화되면 dispatcher는 작업이 성공적으로 완료된 후 git 작업을 실행합니다.

**브랜치 이름**은 `gitWorkflow.branchConvention`으로 제어됩니다:

```json
{
  "taskBoard": {
    "gitWorkflow": {
      "branchConvention": "{type}/{agent}-{description}",
      "types": ["feat", "fix", "refactor", "chore"],
      "defaultType": "feat",
      "autoMerge": true
    }
  }
}
```

기본 템플릿 `{type}/{agent}-{description}`은 `feat/kokuyou-add-rate-limiting`과 같은 브랜치를 생성합니다. `{description}` 부분은 작업 제목에서 파생됩니다(소문자, 공백을 하이픈으로 대체, 40자로 잘림).

작업의 `type` 필드가 브랜치 접두사를 설정합니다. 작업에 타입이 없으면 `defaultType`이 사용됩니다.

**Auto PR/MR**은 GitHub(`gh`)와 GitLab(`glab`) 모두 지원합니다. `PATH`에서 사용 가능한 바이너리가 자동으로 사용됩니다.

---

## Worktree 모드

`gitWorktree: true`이면 각 작업은 공유 작업 디렉터리 대신 격리된 git worktree에서 실행됩니다. 이렇게 하면 동일한 저장소에서 여러 작업이 동시에 실행될 때 파일 충돌이 방지됩니다.

```
~/.tetora/runtime/worktrees/
  task-abc123/   ← 이 작업을 위한 격리된 복사본
  task-def456/   ← 이 작업을 위한 격리된 복사본
```

작업 완료 시:

- `autoMerge: true`(기본값)이면 worktree 브랜치가 `main`으로 머지되고 worktree가 제거됩니다.
- 머지가 실패하면 작업이 `partial-done` 상태로 이동합니다. Worktree는 수동 해결을 위해 보존됩니다.

`partial-done`에서 복구하는 방법:

```bash
# 무슨 일이 있었는지 확인
tetora task show task-abc123 --full

# 브랜치를 수동으로 머지
git merge feat/kokuyou-add-rate-limiting

# 완료로 표시
tetora task move task-abc123 --status=done
```

---

## Workflow 연동

작업은 단일 agent 프롬프트 대신 workflow 파이프라인을 통해 실행될 수 있습니다. 이는 작업에 여러 단계의 협력이 필요할 때 유용합니다 (예: 리서치 → 구현 → 테스트 → 문서화).

작업에 workflow 할당:

```bash
# 작업 생성 시 설정
tetora task create \
  --title="Implement OAuth2 flow" \
  --workflow=engineering-pipeline \
  --assignee=kokuyou

# 또는 기존 작업 업데이트
tetora task update task-abc123 --workflow=engineering-pipeline
```

특정 작업에 대해 보드 레벨 기본 workflow를 비활성화하려면:

```json
{ "workflow": "none" }
```

보드 레벨 기본 workflow는 오버라이드되지 않는 한 모든 자동 디스패치 작업에 적용됩니다:

```json
{
  "taskBoard": {
    "defaultWorkflow": "engineering-pipeline"
  }
}
```

작업의 `workflowRunId` 필드는 특정 workflow 실행과 연결되며 대시보드의 Workflows 탭에서 볼 수 있습니다.

---

## 대시보드 화면

`http://localhost:8991/dashboard`에서 대시보드를 열고 **Taskboard** 탭으로 이동하세요.

**칸반 보드** — 각 상태별 컬럼. 카드에는 제목, 담당자, 우선순위 배지, 비용이 표시됩니다. 드래그하여 상태를 변경합니다.

**작업 상세 패널** — 아무 카드나 클릭하면 열립니다. 표시 내용:
- 전체 설명 및 모든 댓글 (spec, context, log 항목)
- 세션 링크 (아직 실행 중이면 라이브 워커 터미널로 이동)
- 비용, 소요 시간, 재시도 횟수
- 해당되는 경우 workflow 실행 링크

**Diff 검토 패널** — `requireReview: true`이면 완료된 작업이 검토 큐에 나타납니다. 검토자는 변경 사항의 diff를 보고 승인하거나 수정을 요청할 수 있습니다.

---

## 팁

**작업 크기 조정.** 작업은 30~90분 범위로 유지하세요. 너무 큰 작업(며칠이 걸리는 리팩터링)은 타임아웃이 되거나 빈 출력을 생성하여 실패로 표시됩니다. `parentId` 필드를 사용하여 하위 작업으로 분할하세요.

**동시 디스패치 한도.** `maxConcurrentTasks: 3`이 안전한 기본값입니다. provider가 허용하는 API 연결 수를 초과하면 경합과 타임아웃이 발생합니다. 3으로 시작하고 안정적인 동작을 확인한 후에만 5로 늘리세요.

**Partial-done 복구.** 작업이 `partial-done`으로 들어가면 agent는 작업을 성공적으로 완료한 것입니다 — git 머지 단계만 실패한 것입니다. 충돌을 수동으로 해결한 다음 작업을 `done`으로 이동하세요. 비용 및 세션 데이터는 보존됩니다.

**`dependsOn` 사용.** 충족되지 않은 의존성이 있는 작업은 나열된 모든 작업 ID가 `done` 상태에 도달할 때까지 dispatcher가 건너뜁니다. 업스트림 작업의 결과는 의존하는 작업의 프롬프트에 "Previous Task Results" 아래에 자동으로 주입됩니다.

**백로그 트리아지.** `backlogAgent`는 각 `backlog` 작업을 읽고 실현 가능성과 우선순위를 평가하여 명확한 작업을 `todo`로 이동시킵니다. `backlog` 작업에 상세한 설명과 완료 기준을 작성하세요 — 트리아지 agent가 작업을 이동할지 사람이 검토할 때까지 둘지 결정하는 데 사용합니다.

**재시도와 리뷰 루프.** `reviewLoop: false`(기본값)이면 실패한 작업은 이전 로그 댓글이 주입된 상태로 `maxRetries`번까지 재시도됩니다. `reviewLoop: true`이면 각 실행을 완료로 간주하기 전에 `reviewAgent`가 검토합니다 — 문제가 발견되면 agent가 피드백을 받고 다시 시도합니다.
