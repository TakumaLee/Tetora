---
title: "Discord 멀티태스킹 가이드"
lang: "ko"
order: 6
description: "Run multiple agents concurrently via Discord threads."
---
# Discord 멀티태스킹 가이드

Tetora는 Discord에서 **Thread + `/focus`** 를 통한 병렬 멀티태스킹 대화를 지원합니다. 각 thread는 독립적인 session과 agent 바인딩을 가집니다.

---

## 기본 개념

### 메인 채널 — 단일 Session

각 Discord 채널에는 **하나의 활성 session**만 있으며, 모든 메시지가 같은 대화 컨텍스트를 공유합니다.

- Session key 형식: `discord:{channelID}`
- 같은 채널 내 모든 사람의 메시지가 같은 session으로 들어갑니다
- `!new`로 리셋할 때까지 대화 이력이 계속 누적됩니다

### Thread — 독립 Session

Discord thread는 `/focus`를 통해 특정 agent에 바인딩하여 완전히 독립된 session을 가질 수 있습니다.

- Session key 형식: `agent:{agentName}:discord:thread:{guildID}:{threadID}`
- 메인 채널의 session과 완전히 격리되어 컨텍스트가 서로 영향을 주지 않습니다
- 각 thread를 다른 agent에 바인딩할 수 있습니다

---

## 명령어

| 명령어 | 위치 | 설명 |
|---|---|---|
| `/focus <agent>` | Thread 내 | 이 thread를 지정된 agent에 바인딩하고 독립 session 생성 |
| `/unfocus` | Thread 내 | Thread의 agent 바인딩 해제 |
| `!new` | 메인 채널 | 현재 session을 보관하고 다음 메시지부터 완전히 새로운 session 시작 |

---

## 멀티태스킹 작업 흐름

### Step 1: Discord Thread 만들기

메인 채널에서 메시지를 우클릭 → **Create Thread** (또는 Discord의 thread 생성 기능 사용).

### Step 2: Thread 내에서 Agent 바인딩

```
/focus ruri
```

바인딩이 성공하면 이 thread 내의 모든 대화가:
- ruri의 SOUL.md 역할 설정을 사용합니다
- 독립적인 대화 이력을 가집니다
- 메인 채널의 session에 영향을 주지 않습니다

### Step 3: 필요에 따라 여러 Thread 열기

```
#general (메인 채널)              ← 일상 대화, 1개 session
  └─ Thread: "auth 모듈 리팩터링"  ← /focus kokuyou → 독립 session
  └─ Thread: "이번 주 블로그 작성" ← /focus kohaku  → 독립 session
  └─ Thread: "경쟁사 분석 보고서"  ← /focus hisui   → 독립 session
  └─ Thread: "프로젝트 계획 논의"  ← /focus ruri    → 독립 session
```

각 thread는 완전히 격리된 대화 공간으로, 동시에 진행할 수 있습니다.

---

## 주의 사항

### TTL (유효 시간)

- Thread 바인딩은 기본적으로 **24시간** 후 만료됩니다
- 만료 후 thread는 일반 모드로 돌아갑니다 (메인 채널의 라우팅 로직을 따름)
- 설정에서 `threadBindings.ttlHours`로 조정할 수 있습니다

### 병렬 실행 제한

- 전역 최대 병렬 수는 `maxConcurrent`로 제어됩니다 (기본값 8)
- 모든 채널 + thread가 이 한도를 공유합니다
- 한도를 초과하는 메시지는 대기열에 들어갑니다

### 설정 활성화

설정에서 thread bindings가 활성화되어 있는지 확인하세요:

```json
{
  "discord": {
    "threadBindings": {
      "enabled": true,
      "ttlHours": 24
    }
  }
}
```

### 메인 채널의 제한

- 메인 채널에서는 `/focus`로 두 번째 session을 만들 수 없습니다
- 대화 컨텍스트를 리셋하려면 `!new`를 사용하세요
- 같은 채널 내에서 여러 메시지를 동시에 보내면 session을 공유하게 되어 컨텍스트가 서로 영향을 줄 수 있습니다

---

## 사용 시나리오 권장 사항

| 시나리오 | 권장 방법 |
|---|---|
| 일상적인 잡담, 간단한 질문 | 메인 채널에서 직접 대화 |
| 특정 주제에 집중하여 논의 필요 | Thread 열기 + `/focus` |
| 다른 작업을 다른 agent에게 배정 | 각 작업마다 thread 하나, 각각 해당 agent로 `/focus` |
| 대화 컨텍스트가 너무 길어서 다시 시작 | 메인 채널은 `!new`, thread는 `/unfocus` 후 `/focus` |
| 여러 사람이 같은 주제로 협업 | 공유 thread 하나 열어서 모두 thread 내에서 대화 |
