---
title: "MCP (Model Context Protocol) 연동"
lang: "ko"
order: 5
description: "Expose Tetora capabilities to any MCP-compatible client."
---
# MCP (Model Context Protocol) 연동

Tetora에는 AI agent (Claude Code 등)가 표준 MCP 프로토콜을 통해 Tetora의 API와 상호작용할 수 있는 내장 MCP 서버가 포함되어 있습니다.

## 아키텍처

```
Claude Code  ──stdio──>  tetora mcp-server  ──HTTP──>  Tetora Daemon
  (client)                (bridge process)              (localhost:8991)
```

MCP 서버는 **stdio JSON-RPC 2.0 브리지**입니다 — stdin에서 요청을 읽고, Tetora의 HTTP API로 프록시하여 stdout에 응답을 씁니다. Claude Code가 이를 자식 프로세스로 실행합니다.

## 설치

### 1. Claude Code 설정에 MCP 서버 추가

`~/.claude/settings.json`에 다음을 추가합니다:

```json
{
  "mcpServers": {
    "tetora": {
      "command": "/Users/you/.tetora/bin/tetora",
      "args": ["mcp-server"]
    }
  }
}
```

경로를 실제 `tetora` 바이너리 위치로 교체하세요. 다음으로 확인할 수 있습니다:

```bash
which tetora
# 또는
ls ~/.tetora/bin/tetora
```

### 2. Tetora 데몬 실행 확인

MCP 브리지는 Tetora HTTP API로 프록시하므로 데몬이 실행 중이어야 합니다:

```bash
tetora start
```

### 3. 확인

Claude Code를 재시작하세요. MCP 도구가 `tetora_` 접두사를 붙인 사용 가능한 도구로 표시됩니다.

## 사용 가능한 도구

### 작업 관리

| 도구 | 설명 |
|------|-------------|
| `tetora_taskboard_list` | 칸반 보드 티켓 목록 조회. 선택적 필터: `project`, `assignee`, `priority`. |
| `tetora_taskboard_update` | 작업 업데이트 (status, assignee, priority, title). `id` 필수. |
| `tetora_taskboard_comment` | 작업에 댓글 추가. `id`와 `comment` 필수. |

### 메모리

| 도구 | 설명 |
|------|-------------|
| `tetora_memory_get` | 메모리 항목 읽기. `agent`와 `key` 필수. |
| `tetora_memory_set` | 메모리 항목 쓰기. `agent`, `key`, `value` 필수. |
| `tetora_memory_search` | 모든 메모리 항목 목록 조회. 선택적 필터: `role`. |

### 디스패치

| 도구 | 설명 |
|------|-------------|
| `tetora_dispatch` | 다른 agent에게 작업 디스패치. 새 Claude Code 세션을 생성합니다. `prompt` 필수. 선택 사항: `agent`, `workdir`, `model`. |

### 지식

| 도구 | 설명 |
|------|-------------|
| `tetora_knowledge_search` | 공유 지식 베이스 검색. `q` 필수. 선택 사항: `limit`. |

### 알림

| 도구 | 설명 |
|------|-------------|
| `tetora_notify` | Discord/Telegram을 통해 사용자에게 알림 전송. `message` 필수. 선택 사항: `level` (info/warn/error). |
| `tetora_ask_user` | Discord를 통해 사용자에게 질문하고 응답을 기다립니다 (최대 6분). `question` 필수. 선택 사항: `options` (빠른 응답 버튼, 최대 4개). |

## 도구 상세

### tetora_taskboard_list

```json
{
  "project": "tetora",
  "assignee": "kokuyou",
  "priority": "P0"
}
```

모든 파라미터는 선택 사항입니다. 작업의 JSON 배열을 반환합니다.

### tetora_taskboard_update

```json
{
  "id": "TASK-42",
  "status": "in_progress",
  "assignee": "kokuyou",
  "priority": "P1",
  "title": "New title"
}
```

`id`만 필수입니다. 다른 필드는 제공된 경우에만 업데이트됩니다. 상태 값: `todo`, `in_progress`, `review`, `done`.

### tetora_taskboard_comment

```json
{
  "id": "TASK-42",
  "comment": "Started working on this",
  "author": "kokuyou"
}
```

### tetora_dispatch

```json
{
  "prompt": "Fix the broken CSS on the dashboard sidebar",
  "agent": "kokuyou",
  "workdir": "/path/to/project",
  "model": "sonnet"
}
```

`prompt`만 필수입니다. `agent`를 생략하면 Tetora의 smart dispatch가 가장 적합한 agent로 라우팅합니다.

### tetora_ask_user

```json
{
  "question": "Should I proceed with the database migration?",
  "options": ["Yes", "No", "Skip for now"]
}
```

이것은 **블로킹 호출**입니다 — 사용자가 Discord를 통해 응답할 때까지 최대 6분을 기다립니다. 사용자는 선택적 빠른 응답 버튼이 있는 질문을 보고 사용자 정의 답변을 입력할 수도 있습니다.

## CLI 명령어

### 외부 MCP 서버 관리

Tetora는 외부 MCP 서버에 연결하는 MCP **호스트** 역할도 할 수 있습니다:

```bash
# 설정된 MCP 서버 목록 조회
tetora mcp list

# 서버의 전체 설정 표시
tetora mcp show <name>

# 새 MCP 서버 추가
tetora mcp add <name> --command CMD [--args A1,A2] [--env K=V,K2=V2]

# 서버 설정 제거
tetora mcp remove <name>

# 서버 연결 테스트
tetora mcp test <name>
```

### MCP 브리지 실행

```bash
# MCP 브리지 서버 시작 (일반적으로 Claude Code가 실행, 수동 실행 불필요)
tetora mcp-server
```

첫 번째 실행 시 올바른 바이너리 경로가 포함된 `~/.tetora/mcp/bridge.json`이 생성됩니다.

## 설정

`config.json`의 MCP 관련 설정:

| 필드 | 타입 | 기본값 | 설명 |
|------|------|---------|-------------|
| `mcpServers` | object | `{}` | 외부 MCP 서버 설정 맵 (이름 → {command, args, env}). |

브리지 서버는 데몬에 연결하기 위해 메인 설정에서 `listenAddr`와 `apiToken`을 읽습니다.

## 인증

`config.json`에 `apiToken`이 설정되어 있으면 MCP 브리지가 데몬으로의 모든 HTTP 요청에 자동으로 `Authorization: Bearer <token>`을 포함합니다. 추가적인 MCP 레벨 인증은 필요하지 않습니다.

## 문제 해결

**Claude Code에 도구가 나타나지 않을 때:**
- `settings.json`의 바이너리 경로가 올바른지 확인하세요
- Tetora 데몬이 실행 중인지 확인하세요 (`tetora start`)
- MCP 연결 오류에 대한 Claude Code 로그를 확인하세요

**"HTTP 401" 오류:**
- `config.json`의 `apiToken`이 일치해야 합니다. 브리지가 자동으로 읽습니다.

**"connection refused" 오류:**
- 데몬이 실행 중이 아니거나 `listenAddr`이 일치하지 않습니다. 기본값: `127.0.0.1:8991`.

**`tetora_ask_user` 타임아웃:**
- 사용자가 Discord를 통해 응답할 시간은 6분입니다. Discord 봇이 연결되어 있는지 확인하세요.
