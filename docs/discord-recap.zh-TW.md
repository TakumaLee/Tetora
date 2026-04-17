# Discord Recap 轉發

把 Claude Code 離開後回來時顯示的 recap 自動鏡射到 Discord — 每個 Claude session 對應一個專屬 thread，永遠不在 parent channel 裸露發文。

---

## 運作原理

Claude Code 離開一陣子後 resume 時，會生成一段摘要（internal 名稱 `away_summary`），寫入該 session 的 transcript JSONL（`~/.claude/projects/*/<sessionID>.jsonl`）。Tetora daemon 每 2 秒輪詢這些 transcript，抓到新的 away_summary → 查路由表 → 送到對應的 Discord thread。

- **零額外 token**：Claude 已經花 token 生成的 recap 直接讀磁碟轉發
- **去重**：每則 recap 有 uuid，送過就跳過，daemon 重啟也不會重發
- **不阻塞 Claude**：watcher 在 daemon 內獨立 goroutine，Discord 掛掉只影響轉發，不影響 Claude 本身

---

## 路由規則

```
新 session 第一次 recap
    ↓
查 cwd 是否在 projectChannels 裡
    ├─ 有 → 用該 channel 當 parent
    └─ 無 → 用 defaultParentChannel
    ↓
Bot 在 parent 底下建 thread: "[repo/branch] sessionShortID · MM-DD HH:mm"
    ↓
recap 送進 thread，session_id → thread_id 存進 SQLite

同 session 後續 recap
    ↓
查 recap_session_routing → 直接送進已綁的 thread
```

---

## 設定

編輯 `~/.tetora/config.json`，在 `discord` 區塊底下加 `recap`：

```json
{
  "discord": {
    "recap": {
      "enabled": true,
      "defaultParentChannel": "1494595765253705818",
      "projectChannels": {
        "/Users/你/Workspace/Projects/tetora": "1475737333247512689",
        "/Users/你/Workspace/Projects/stock-trading": "1476344344213590097"
      },
      "transcriptRoot": "~/.claude/projects",
      "pollIntervalMs": 2000,
      "threadAutoArchiveMin": 10080
    }
  }
}
```

| 欄位 | 必填 | 說明 |
|---|---|---|
| `enabled` | 是 | 總開關，`false` 時 watcher 不啟動 |
| `defaultParentChannel` | 是 | 當 cwd 找不到對應映射時的後備 channel ID |
| `projectChannels` | 否 | cwd 絕對路徑 → channel ID。路徑要完全一致 |
| `transcriptRoot` | 否 | 預設 `~/.claude/projects`，自訂時會展開 `~` |
| `pollIntervalMs` | 否 | 預設 2000（2 秒） |
| `threadAutoArchiveMin` | 否 | Discord thread 自動封存時間（分鐘）。選項：60 / 1440 / 4320 / 10080。預設 10080（7 天） |

改完 config 需重啟 daemon：

```bash
launchctl kickstart -k gui/$UID/com.tetora.daemon
```

---

## Thread 封存行為

Discord thread 在無活動 N 分鐘後會自動 archive（依 `threadAutoArchiveMin` 設定）。

- **Archive ≠ 刪除**：訊息都還在，只是被收到「封存討論串」清單
- **Bot 在 archived thread 送新訊息會自動 unarchive**：recap 本身就是 heartbeat
- 長期閒置的 session thread 被 archive 也沒關係，下次 Claude resume 送新 recap 自動復活

---

## SQLite Schema

Tetora 在 `~/.tetora/dbs/history.db` 建立兩張表：

```sql
-- 去重 + 稽核：哪個 recap uuid 已送過
CREATE TABLE recap_sent (
  uuid       TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  thread_id  TEXT NOT NULL,
  sent_at    TEXT NOT NULL
);

-- 路由：哪個 Claude session 綁到哪個 Discord thread
CREATE TABLE recap_session_routing (
  session_id        TEXT PRIMARY KEY,
  parent_channel_id TEXT NOT NULL,
  thread_id         TEXT NOT NULL,
  cwd               TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  last_recap_at     TEXT NOT NULL
);
```

---

## 手動改綁 thread

如果 bot 建錯 thread，或你想把某個 session 改綁到另一個 thread：

```bash
# 1. 查當前綁定
sqlite3 ~/.tetora/dbs/history.db "SELECT * FROM recap_session_routing WHERE session_id LIKE 'db49ea04%';"

# 2. 改綁
sqlite3 ~/.tetora/dbs/history.db "UPDATE recap_session_routing SET thread_id='新thread_id' WHERE session_id='完整session_id';"

# 3. 或直接刪除讓它下次 recap 自動重建
sqlite3 ~/.tetora/dbs/history.db "DELETE FROM recap_session_routing WHERE session_id='完整session_id';"
```

---

## 除錯

### Recap 沒送到 Discord

1. 確認 daemon 在跑：`launchctl print gui/$UID/com.tetora.daemon | grep state`
2. 查 watcher 是否啟動：`grep "recap watcher started" ~/.tetora/logs/tetora.log`
3. 查是否抓到 recap：`grep "recap forwarded" ~/.tetora/logs/tetora.log`
4. 查是否跳過（找不到 parent channel）：`grep "no parent channel" ~/.tetora/logs/tetora.log`
5. 查 Discord API 錯誤：`grep "discord api error" ~/.tetora/logs/tetora.log`

### 歷史 recap 會不會被 replay

不會。Daemon 啟動時會記錄所有現存 jsonl 的 EOF offset（log: `recap: primed transcript offsets files=N`），只從之後的新行開始解析。

### 新增專案想設 projectChannel

找到該專案 cwd 絕對路徑，加到 `projectChannels`，重啟 daemon。cwd 要和 Claude Code 記錄在 transcript 裡的完全一致（case-sensitive、無 trailing slash）。

---

## 限制（待改善）

- **Discord 訊息 2000 字硬切**：長 recap 會被截斷，改善中
- **沒有 Discord 端 `!bind` 指令**：目前只能透過 SQLite 改綁
- **沒有 SessionStart(resume) hook poke**：最多 2 秒延遲
