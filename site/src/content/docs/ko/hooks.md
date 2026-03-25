---
title: "Claude Code Hooks 연동"
lang: "ko"
order: 3
description: "Integrate with Claude Code Hooks for real-time session observation."
---
# Claude Code Hooks 연동

## 개요

Claude Code Hooks는 Claude Code에 내장된 이벤트 시스템으로, 세션의 주요 시점에 셸 명령을 실행합니다. Tetora는 자신을 hook 수신자로 등록하여 폴링, tmux, 래퍼 스크립트 없이 실시간으로 모든 실행 중인 agent 세션을 관찰할 수 있습니다.

**Hooks가 활성화하는 기능:**

- 대시보드의 실시간 진행 상황 추적 (도구 호출, 세션 상태, 라이브 워커 목록)
- statusline 브리지를 통한 비용 및 토큰 모니터링
- 도구 사용 감사 (어떤 도구가 어느 세션에서 어느 디렉터리에서 실행되었는지)
- 세션 완료 감지 및 자동 작업 상태 업데이트
- Plan mode 게이트: 대시보드에서 사람이 계획을 승인할 때까지 `ExitPlanMode`를 보류
- 인터랙티브 질문 라우팅: `AskUserQuestion`이 MCP 브리지로 리다이렉트되어 터미널을 차단하는 대신 채팅 플랫폼에 질문이 표시됨

Hooks는 Tetora v2.0부터 권장되는 연동 방식입니다. 구형 tmux 기반 방식(v1.x)은 여전히 작동하지만 plan gate 및 질문 라우팅 등 hooks 전용 기능을 지원하지 않습니다.

---

## 아키텍처

```
Claude Code session
  │
  ├── PreToolUse  ──────────────────► Tetora /api/hooks/event
  │   (ExitPlanMode)                  └─► Plan gate: 승인될 때까지 롱폴
  │   (AskUserQuestion)               └─► Deny: MCP 브리지로 리다이렉트
  │
  ├── PostToolUse ──────────────────► Tetora /api/hooks/event
  │                                   └─► 워커 상태 업데이트
  │                                   └─► 계획 파일 쓰기 감지
  │
  ├── Stop        ──────────────────► Tetora /api/hooks/event
  │                                   └─► 워커를 done으로 표시
  │                                   └─► 작업 완료 트리거
  │
  └── Notification ─────────────────► Tetora /api/hooks/event
                                      └─► Discord/Telegram으로 전달
```

Hook 명령은 Claude Code의 `~/.claude/settings.json`에 주입되는 작은 curl 호출입니다. 모든 이벤트는 실행 중인 Tetora 데몬의 `POST /api/hooks/event`로 전송됩니다.

---

## 설치

### Hooks 설치

Tetora 데몬이 실행 중인 상태에서:

```bash
tetora hooks install
```

이 명령은 `~/.claude/settings.json`에 항목을 작성하고 `~/.tetora/mcp/bridge.json`에 MCP 브리지 설정을 생성합니다.

`~/.claude/settings.json`에 작성되는 내용 예시:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST http://localhost:8991/api/hooks/event -H 'Content-Type: application/json' -d @-"
          }
        ]
      }
    ],
    "Stop": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "Notification": [ { "hooks": [ { "type": "command", "command": "..." } ] } ],
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [ { "type": "command", "command": "...", "timeout": 600 } ]
      },
      {
        "matcher": "AskUserQuestion",
        "hooks": [ { "type": "command", "command": "..." } ]
      }
    ]
  }
}
```

### 상태 확인

```bash
tetora hooks status
```

설치된 hooks, 등록된 Tetora 규칙 수, 데몬 시작 이후 수신된 총 이벤트 수를 표시합니다.

대시보드에서도 확인할 수 있습니다: **Engineering Details → Hooks**에서 동일한 상태와 라이브 이벤트 피드를 볼 수 있습니다.

### Hooks 제거

```bash
tetora hooks remove
```

`~/.claude/settings.json`에서 모든 Tetora 항목을 제거합니다. 기존 비-Tetora hooks는 보존됩니다.

---

## Hook 이벤트

### PostToolUse

모든 도구 호출이 완료된 후 발생합니다. Tetora는 이를 다음 용도로 사용합니다:

- agent가 사용 중인 도구 추적 (`Bash`, `Write`, `Edit`, `Read` 등)
- 라이브 워커 목록에서 워커의 `lastTool` 및 `toolCount` 업데이트
- agent가 계획 파일에 쓸 때 감지 (계획 캐시 업데이트 트리거)

### Stop

Claude Code 세션이 종료될 때 발생합니다 (정상 완료 또는 취소). Tetora는 이를 다음 용도로 사용합니다:

- 라이브 워커 목록에서 워커를 `done`으로 표시
- 대시보드에 완료 SSE 이벤트 게시
- 태스크보드 작업의 다운스트림 작업 상태 업데이트 트리거

### Notification

Claude Code가 알림을 보낼 때 발생합니다 (예: 권한 필요, 긴 중지). Tetora는 이를 Discord/Telegram으로 전달하고 대시보드 SSE 스트림에 게시합니다.

### PreToolUse: ExitPlanMode (plan gate)

agent가 plan mode를 종료하려 할 때 Tetora가 롱폴(타임아웃: 600초)로 이벤트를 가로챕니다. 계획 내용이 캐시되고 대시보드의 세션 상세 보기에 표시됩니다.

대시보드에서 사람이 계획을 승인하거나 거부할 수 있습니다. 승인되면 hook이 반환되고 Claude Code가 진행합니다. 거부되거나 타임아웃이 만료되면 종료가 차단되고 Claude Code는 plan mode에 머뭅니다.

### PreToolUse: AskUserQuestion (질문 라우팅)

Claude Code가 사용자에게 인터랙티브하게 질문하려 할 때 Tetora가 이를 가로채고 기본 동작을 거부합니다. 질문은 MCP 브리지를 통해 라우팅되어 터미널 앞에 앉아 있지 않아도 답할 수 있도록 설정된 채팅 플랫폼(Discord, Telegram 등)에 표시됩니다.

---

## 실시간 진행 상황 추적

Hooks가 설치되면 대시보드 **Workers** 패널에 라이브 세션이 표시됩니다:

| 필드 | 소스 |
|---|---|
| Session ID | hook 이벤트의 `session_id` |
| 상태 | `working` / `idle` / `done` |
| 마지막 도구 | 가장 최근 `PostToolUse` 도구 이름 |
| 작업 디렉터리 | hook 이벤트의 `cwd` |
| 도구 호출 횟수 | 누적 `PostToolUse` 횟수 |
| 비용 / 토큰 | statusline 브리지 (`POST /api/hooks/usage`) |
| 출처 | Tetora가 디스패치한 경우 연결된 작업 또는 cron 작업 |

비용 및 토큰 데이터는 Claude Code statusline 스크립트에서 가져옵니다. 이 스크립트는 설정 가능한 간격으로 `/api/hooks/usage`에 데이터를 전송합니다. statusline 스크립트는 hooks와 별개로, Claude Code 상태 표시줄 출력을 읽어 Tetora에 전달합니다.

---

## 비용 모니터링

usage 엔드포인트(`POST /api/hooks/usage`)는 다음을 수신합니다:

```json
{
  "sessionId": "abc123",
  "costUsd": 0.0042,
  "inputTokens": 8200,
  "outputTokens": 340,
  "contextPct": 12,
  "model": "claude-sonnet-4-5"
}
```

이 데이터는 대시보드 Workers 패널에 표시되고 일일 비용 차트로 집계됩니다. 세션 비용이 설정된 role별 또는 전역 예산을 초과하면 예산 알림이 발생합니다.

---

## 문제 해결

### Hooks가 발생하지 않을 때

**데몬 실행 여부 확인:**
```bash
tetora status
```

**Hooks 설치 여부 확인:**
```bash
tetora hooks status
```

**settings.json 직접 확인:**
```bash
cat ~/.claude/settings.json | grep -A5 "hooks"
```

hooks 키가 없으면 `tetora hooks install`을 다시 실행하세요.

**데몬이 hook 이벤트를 수신할 수 있는지 확인:**
```bash
curl -s -X POST http://localhost:8991/api/hooks/event \
  -H "Content-Type: application/json" \
  -d '{"hook_event_name":"Stop","session_id":"test-123"}'
# 예상: {"ok":true}
```

데몬이 예상 포트에서 수신 대기 중이 아닌 경우 `config.json`의 `listenAddr`을 확인하세요.

### settings.json 권한 오류

Claude Code의 `settings.json`은 `~/.claude/settings.json`에 있습니다. 파일이 다른 사용자 소유이거나 권한이 제한적인 경우:

```bash
ls -la ~/.claude/settings.json
chmod 644 ~/.claude/settings.json
```

### 대시보드 workers 패널이 비어 있을 때

1. Hooks가 설치되어 있고 데몬이 실행 중인지 확인하세요.
2. Claude Code 세션을 수동으로 시작하고 도구를 하나 실행하세요 (예: `ls`).
3. 대시보드 Workers 패널을 확인하세요 — 몇 초 내에 세션이 표시되어야 합니다.
4. 표시되지 않으면 데몬 로그를 확인하세요: `tetora logs -f | grep hooks`

### Plan gate가 나타나지 않을 때

Plan gate는 Claude Code가 `ExitPlanMode`를 호출하려 할 때만 활성화됩니다. 이는 plan mode 세션(`--plan`으로 시작하거나 role 설정에서 `permissionMode: "plan"`으로 설정된 경우)에서만 발생합니다. 인터랙티브 `acceptEdits` 세션은 plan mode를 사용하지 않습니다.

### 질문이 채팅으로 라우팅되지 않을 때

`AskUserQuestion` deny hook은 MCP 브리지가 설정되어 있어야 합니다. `tetora hooks install`을 다시 실행하세요 — 브리지 설정이 재생성됩니다. 그런 다음 브리지를 Claude Code MCP 설정에 추가하세요:

```bash
cat ~/.tetora/mcp/bridge.json
```

해당 파일을 `~/.claude/settings.json`의 `mcpServers` 아래에 MCP 서버로 추가하세요.

---

## tmux에서 마이그레이션 (v1.x)

Tetora v1.x에서는 agent가 tmux pane 내에서 실행되었고 Tetora는 pane 출력을 읽어 모니터링했습니다. v2.0에서는 agent가 베어 Claude Code 프로세스로 실행되고 Tetora가 hooks를 통해 이를 관찰합니다.

**v1.x에서 업그레이드하는 경우:**

1. 업그레이드 후 `tetora hooks install`을 한 번 실행하세요.
2. `config.json`에서 tmux 세션 관리 설정을 제거하세요 (`tmux.*` 키는 이제 무시됩니다).
3. 기존 세션 이력은 `history.db`에 보존됩니다 — 마이그레이션이 필요 없습니다.
4. `tetora session list` 명령과 대시보드의 Sessions 탭은 이전과 동일하게 작동합니다.

tmux 터미널 브리지(`discord_terminal.go`)는 Discord를 통한 인터랙티브 터미널 접근을 위해 여전히 사용 가능합니다. 이는 agent 실행과 별개로, 실행 중인 터미널 세션에 키스트로크를 보낼 수 있게 해줍니다. Hooks와 터미널 브리지는 상호 배타적이지 않고 상호 보완적입니다.
