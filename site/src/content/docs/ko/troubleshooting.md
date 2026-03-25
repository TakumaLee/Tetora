---
title: "문제 해결 가이드"
lang: "ko"
order: 7
description: "Common issues and solutions for Tetora setup and operation."
---
# 문제 해결 가이드

이 가이드는 Tetora 실행 시 자주 발생하는 문제를 다룹니다. 각 문제에 대해 가장 가능성 높은 원인이 먼저 나열됩니다.

---

## tetora doctor

항상 여기서 시작하세요. 설치 후 또는 무언가 작동을 멈췄을 때 `tetora doctor`를 실행하세요:

```
=== Tetora Doctor ===

  ✓ Config          /Users/you/.tetora/config.json
  ✓ Claude CLI      claude 1.2.3
  ✓ Provider        claude-cli
  ✓ Port            localhost:8991 in use (daemon running)
  ✓ Telegram        enabled (chatID=123456)
  ✓ Jobs            jobs.json (4 jobs, 3 enabled)
  ✓ History DB      12 tasks
  ✓ Workdir         /Users/you/dev
  ✓ Agent/ruri      Commander
  ✓ Binary          /Users/you/.tetora/bin/tetora
  ✓ Encryption      key configured
  ✓ ffmpeg          available
  ✓ sqlite3         available
  ✓ Agents Dir      /Users/you/.tetora/agents (3 agents)
  ✓ Workspace       /Users/you/.tetora/workspace

All checks passed.
```

각 줄은 하나의 검사입니다. 빨간색 `✗`는 하드 실패(수정하지 않으면 데몬이 작동하지 않음)를 의미합니다. 노란색 `~`는 제안(선택 사항이지만 권장)을 의미합니다.

실패한 검사에 대한 일반적인 수정 방법:

| 실패한 검사 | 수정 방법 |
|---|---|
| `Config: not found` | `tetora init` 실행 |
| `Claude CLI: not found` | `config.json`에 `claudePath` 설정 또는 Claude Code 설치 |
| `sqlite3: not found` | `brew install sqlite3` (macOS) 또는 `apt install sqlite3` (Linux) |
| `Agent/name: soul file missing` | `~/.tetora/agents/{name}/SOUL.md` 생성 또는 `tetora init` 실행 |
| `Workspace: not found` | `tetora init`을 실행하여 디렉터리 구조 생성 |

---

## "session produced no output"

작업이 완료되었지만 출력이 비어 있습니다. 작업이 자동으로 `failed`로 표시됩니다.

**원인 1: 컨텍스트 창이 너무 큼.** 세션에 주입된 프롬프트가 모델의 컨텍스트 한도를 초과했습니다. Claude Code는 컨텍스트를 맞출 수 없을 때 즉시 종료합니다.

수정: `config.json`에서 세션 압축을 활성화하세요:

```json
{
  "sessionCompaction": {
    "enabled": true,
    "tokenThreshold": 150000,
    "messageThreshold": 100,
    "strategy": "auto"
  }
}
```

또는 작업에 주입되는 컨텍스트 양을 줄이세요 (더 짧은 설명, 더 적은 spec 댓글, 더 짧은 `dependsOn` 체인).

**원인 2: Claude Code CLI 시작 실패.** `claudePath`의 바이너리가 시작 시 크래시합니다 — 보통 잘못된 API 키, 네트워크 문제, 또는 버전 불일치 때문입니다.

수정: Claude Code 바이너리를 직접 실행하여 오류를 확인하세요:

```bash
/usr/local/bin/claude --version
/usr/local/bin/claude -p "hello"
```

보고된 오류를 수정한 다음 작업을 재시도하세요:

```bash
tetora task move task-abc123 --status=todo
```

**원인 3: 빈 프롬프트.** 작업에 제목은 있지만 설명이 없으며, 제목만으로는 agent가 행동하기에 너무 모호합니다. 세션이 실행되어 empty-check를 통과하지 못하는 출력을 생성하여 플래그가 됩니다.

수정: 구체적인 설명을 추가하세요:

```bash
tetora task update task-abc123 \
  --description="Create src/ratelimit/bucket.go with a token bucket implementation..."
```

---

## 대시보드에서 "unauthorized" 오류

대시보드가 401을 반환하거나 다시 로드 후 빈 페이지가 표시됩니다.

**원인 1: Service Worker가 오래된 인증 토큰을 캐시함.** PWA Service Worker는 인증 헤더를 포함한 응답을 캐시합니다. 새 토큰으로 데몬을 재시작하면 캐시된 버전이 오래된 것입니다.

수정: 페이지를 강제 새로고침하세요. Chrome/Safari에서:

- Mac: `Cmd + Shift + R`
- Windows/Linux: `Ctrl + Shift + R`

또는 DevTools → Application → Service Workers → "Unregister"를 클릭한 다음 다시 로드하세요.

**원인 2: Referer 헤더 불일치.** 대시보드의 인증 미들웨어는 `Referer` 헤더를 검증합니다. 브라우저 확장 프로그램, 프록시 또는 `Referer` 헤더 없이 curl로 보낸 요청은 거부됩니다.

수정: 프록시를 통하지 않고 `http://localhost:8991/dashboard`에서 직접 대시보드에 접근하세요. 외부 도구에서 API 접근이 필요한 경우 브라우저 세션 인증 대신 API 토큰을 사용하세요.

---

## 대시보드가 업데이트되지 않음

대시보드가 로드되지만 활동 피드, 워커 목록 또는 태스크 보드가 오래된 상태로 유지됩니다.

**원인: Service Worker 버전 불일치.** PWA Service Worker는 `make bump` 업데이트 후에도 캐시된 버전의 대시보드 JS/HTML을 제공합니다.

수정:

1. 강제 새로고침 (`Cmd + Shift + R` / `Ctrl + Shift + R`)
2. 작동하지 않으면 DevTools → Application → Service Workers → "Update" 또는 "Unregister" 클릭
3. 페이지 다시 로드

**원인: SSE 연결 끊김.** 대시보드는 Server-Sent Events를 통해 실시간 업데이트를 수신합니다. 연결이 끊어지면 (네트워크 문제, 노트북 절전), 피드 업데이트가 중단됩니다.

수정: 페이지를 다시 로드하세요. SSE 연결은 페이지 로드 시 자동으로 재설정됩니다.

---

## "排程接近滿載" 경고

Discord/Telegram 또는 대시보드 알림 피드에서 이 메시지가 표시됩니다.

이것은 slot pressure 경고입니다. 사용 가능한 동시성 슬롯이 `warnThreshold`(기본값: 3) 이하로 떨어지면 발생합니다. Tetora가 용량에 가깝게 실행 중임을 의미합니다.

**대처 방법:**

- 예상된 상황이라면 (많은 작업이 실행 중): 조치가 필요 없습니다. 경고는 정보성 메시지입니다.
- 많은 작업을 실행하지 않는 경우: `doing` 상태에서 멈춘 작업을 확인하세요:

```bash
tetora task list --status=doing
```

- 용량을 늘리려면: `config.json`에서 `maxConcurrent`를 늘리고 `slotPressure.warnThreshold`를 그에 맞게 조정하세요.
- 인터랙티브 세션이 지연되는 경우: 인터랙티브 용도로 더 많은 슬롯을 예약하기 위해 `slotPressure.reservedSlots`를 늘리세요.

---

## "doing" 상태에서 멈춘 작업

작업이 `status=doing`을 표시하지만 어떤 agent도 활발히 작업하지 않고 있습니다.

**원인 1: 작업 중 데몬이 재시작됨.** 데몬이 종료될 때 작업이 실행 중이었습니다. 다음 시작 시 Tetora는 고아가 된 `doing` 작업을 확인하여 비용/소요 시간 증거가 있으면 `done`으로 복원하거나 없으면 `todo`로 재설정합니다.

이것은 자동으로 처리됩니다 — 다음 데몬 시작을 기다리세요. 데몬이 이미 실행 중이고 작업이 여전히 멈춰 있다면 하트비트 또는 stuck-task 리셋이 `stuckThreshold`(기본값: 2h) 내에 처리합니다.

즉시 강제 재설정하려면:

```bash
tetora task move task-abc123 --status=todo
```

**원인 2: 하트비트/stall 감지.** 하트비트 모니터(`heartbeat.go`)가 실행 중인 세션을 확인합니다. 세션이 stall 임계값 동안 출력을 생성하지 않으면 자동으로 취소되고 작업이 `failed`로 이동합니다.

`[auto-reset]` 또는 `[stall-detected]` 시스템 댓글에 대한 작업 댓글을 확인하세요:

```bash
tetora task show task-abc123 --full
```

**API를 통한 수동 취소:**

```bash
curl -X POST http://localhost:8991/api/tasks/task-abc123/cancel
```

---

## Worktree 머지 실패

작업이 완료되고 `[worktree] merge failed`와 같은 댓글과 함께 `partial-done`으로 이동합니다.

이것은 작업 브랜치의 agent 변경 사항이 `main`과 충돌함을 의미합니다.

**복구 단계:**

```bash
# 작업 상세 및 생성된 브랜치 확인
tetora task show task-abc123 --full

# 프로젝트 저장소로 이동
cd /path/to/your/repo

# 브랜치를 수동으로 머지
git merge feat/kokuyou-task-abc123

# 에디터에서 충돌을 해결한 다음 커밋
git add .
git commit -m "merge: feat/kokuyou-task-abc123"

# 작업을 완료로 표시
tetora task move task-abc123 --status=done
```

Worktree 디렉터리는 수동으로 정리하거나 작업을 `done`으로 이동할 때까지 `~/.tetora/runtime/worktrees/task-abc123/`에 보존됩니다.

---

## 높은 토큰 비용

세션이 예상보다 더 많은 토큰을 사용하고 있습니다.

**원인 1: 컨텍스트 압축이 되지 않음.** 세션 압축 없이는 각 턴이 전체 대화 이력을 누적합니다. 긴 실행 작업(많은 도구 호출)은 컨텍스트가 선형적으로 증가합니다.

수정: `sessionCompaction`을 활성화하세요 (위의 "session produced no output" 섹션 참조).

**원인 2: 큰 지식 베이스 또는 규칙 파일.** `workspace/rules/` 및 `workspace/knowledge/`의 파일들이 모든 agent 프롬프트에 주입됩니다. 이 파일들이 크면 매 호출마다 토큰을 소비합니다.

수정:
- `workspace/knowledge/`를 감사하세요 — 개별 파일은 50 KB 이하로 유지하세요.
- 거의 필요하지 않은 참조 자료는 자동 주입 경로에서 이동하세요.
- `tetora knowledge list`를 실행하여 주입되는 내용과 크기를 확인하세요.

**원인 3: 잘못된 모델 라우팅.** 일상적인 작업에 비싼 모델(Opus)이 사용되고 있습니다.

수정: agent 설정에서 `defaultModel`을 검토하고 대량 작업에 더 저렴한 기본값을 설정하세요:

```json
{
  "taskBoard": {
    "autoDispatch": {
      "defaultModel": "sonnet"
    }
  }
}
```

---

## Provider 타임아웃 오류

`context deadline exceeded` 또는 `provider request timed out`과 같은 타임아웃 오류로 작업이 실패합니다.

**원인 1: 작업 타임아웃이 너무 짧음.** 기본 타임아웃이 복잡한 작업에 너무 짧을 수 있습니다.

수정: agent 설정 또는 작업별로 더 긴 타임아웃을 설정하세요:

```json
{
  "roles": {
    "kokuyou": {
      "timeout": "60m"
    }
  }
}
```

또는 작업 설명에 더 많은 세부 정보를 추가하여 LLM 타임아웃 추정값을 늘리세요 (Tetora가 빠른 모델 호출을 통해 설명을 사용하여 타임아웃을 추정합니다).

**원인 2: API 속도 제한 또는 경합.** 동일한 provider에 너무 많은 동시 요청이 발생합니다.

수정: `maxConcurrentTasks`를 줄이거나 비용이 많이 드는 작업을 조절하기 위해 `maxBudget`을 추가하세요:

```json
{
  "autoDispatch": {
    "maxConcurrentTasks": 2,
    "maxBudget": 3.0
  }
}
```

---

## `make bump`가 workflow를 중단시킴

workflow 또는 작업이 실행 중인 상태에서 `make bump`를 실행했습니다. 데몬이 작업 중에 재시작되었습니다.

재시작은 Tetora의 고아 복구 로직을 트리거합니다:

- 완료 증거가 있는 작업(비용 기록됨, 소요 시간 기록됨)은 `done`으로 복원됩니다.
- 완료 증거가 없고 유예 기간(2분)이 지난 작업은 재디스패치를 위해 `todo`로 재설정됩니다.
- 최근 2분 내에 업데이트된 작업은 다음 stuck-task 스캔까지 그대로 둡니다.

**무슨 일이 있었는지 확인하려면:**

```bash
tetora task list --status=doing
tetora task list --status=failed
```

`[auto-restore]` 또는 `[auto-reset]` 항목에 대한 작업 댓글을 검토하세요.

**활성 작업 중 bump를 방지해야 하는 경우** (아직 플래그로 제공되지 않음), bump 전 실행 중인 작업이 없는지 확인하세요:

```bash
tetora task list --status=doing
# 비어 있으면 bump 안전
make bump
```

---

## SQLite 오류

로그에서 `database is locked`, `SQLITE_BUSY`, 또는 `index.lock`과 같은 오류가 발생합니다.

**원인 1: WAL 모드 pragma 누락.** WAL 모드 없이는 SQLite가 배타적 파일 잠금을 사용하여 동시 읽기/쓰기 시 `database is locked`가 발생합니다.

모든 Tetora DB 호출은 `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`을 앞에 추가하는 `queryDB()` 및 `execDB()`를 통해 이루어집니다. 스크립트에서 sqlite3를 직접 호출하는 경우 이 pragma를 추가하세요:

```bash
sqlite3 ~/.tetora/history.db \
  "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; SELECT count(*) FROM tasks;"
```

**원인 2: 오래된 `index.lock` 파일.** git 작업이 중단되면 `index.lock`이 남습니다. worktree 관리자는 git 작업을 시작하기 전에 오래된 잠금을 확인하지만 크래시로 인해 남을 수 있습니다.

수정:

```bash
# 오래된 잠금 파일 찾기
find ~/.tetora/runtime/worktrees -name "index.lock"

# 제거 (git 작업이 활발히 실행 중이 아닐 때만)
rm /path/to/repo/.git/index.lock
```

---

## Discord / Telegram이 응답하지 않음

봇에게 보낸 메시지에 응답이 없습니다.

**원인 1: 잘못된 채널 설정.** Discord에는 두 가지 채널 목록이 있습니다: `channelIDs` (모든 메시지에 직접 응답)와 `mentionChannelIDs` (@멘션 시에만 응답). 채널이 어느 목록에도 없으면 메시지가 무시됩니다.

수정: `config.json`을 확인하세요:

```json
{
  "discord": {
    "enabled": true,
    "channelIDs": ["123456789012345678"],
    "mentionChannelIDs": []
  }
}
```

**원인 2: 봇 토큰 만료 또는 잘못됨.** Telegram 봇 토큰은 만료되지 않지만 Discord 토큰은 봇이 서버에서 추방되거나 토큰이 재생성되면 무효화될 수 있습니다.

수정: Discord 개발자 포털에서 봇 토큰을 재생성하고 `config.json`을 업데이트하세요.

**원인 3: 데몬이 실행 중이지 않음.** 봇 게이트웨이는 `tetora serve`가 실행 중일 때만 활성화됩니다.

수정:

```bash
tetora status
tetora serve   # 실행 중이 아닌 경우
```

---

## glab / gh CLI 오류

git 연동이 `glab` 또는 `gh`의 오류와 함께 실패합니다.

**일반적인 오류: `gh: command not found`**

수정:
```bash
brew install gh      # macOS
gh auth login        # 인증
```

**일반적인 오류: `glab: You are not logged in`**

수정:
```bash
brew install glab    # macOS
glab auth login      # GitLab 인스턴스로 인증
```

**일반적인 오류: `remote: HTTP Basic: Access denied`**

수정: 저장소 호스트에 SSH 키 또는 HTTPS 자격증명이 설정되어 있는지 확인하세요. GitLab의 경우:

```bash
glab auth status
ssh -T git@gitlab.com   # SSH 연결 테스트
```

GitHub의 경우:

```bash
gh auth status
ssh -T git@github.com
```

**PR/MR 생성은 성공하지만 잘못된 베이스 브랜치를 가리킴**

기본적으로 PR은 저장소의 기본 브랜치(`main` 또는 `master`)를 대상으로 합니다. workflow가 다른 베이스를 사용하는 경우 post-task git 설정에서 명시적으로 설정하거나 호스팅 플랫폼에서 저장소의 기본 브랜치가 올바르게 설정되어 있는지 확인하세요.
