# Template Marketplace — Tetora Store

> Status: Draft (中期規劃)
> Priority: Strategic (商業模式)
> Dependencies: spec-dashboard-visibility.md (Capabilities tab 先做)

## Vision

**Shopify for AI Workflows** — 讓用戶發佈、發現、安裝 workflow templates，Tetora 從中抽成。

現狀：36 個內建模板，embedded 在 binary 裡，無法更新、無法社群貢獻。
目標：遠端 marketplace，任何人可以發佈模板，用戶一鍵安裝，付費模板 Tetora 抽成。

---

## Business Model

### Tier 結構

| | Free (開源) | Pro ($19/mo) | Enterprise |
|---|---|---|---|
| 內建模板 | 36 個基礎模板 | ✅ | ✅ |
| 社群免費模板 | ✅ 無限安裝 | ✅ | ✅ |
| 付費模板 | ❌ | ✅ 可購買 | ✅ + 批量授權 |
| 發佈模板 | ✅ 免費模板 | ✅ 免費 + 付費 | ✅ |
| Private 模板 | ❌ | 5 個 | 無限 |
| 模板分析 | ❌ | 基本 | 完整 |

### Revenue Streams

1. **Marketplace 抽成** — 付費模板 30% 平台費（同 App Store）
2. **Pro 訂閱** — 解鎖付費模板購買 + private 模板 + 分析
3. **Enterprise** — 團隊共享模板、RBAC、audit log
4. **Featured Placement** — 模板作者付費置頂（$50/week）

### 定價參考

- 簡單模板（3-5 steps）: 免費 or $5
- 產業模板（8-15 steps, external steps）: $15-$30
- 企業級模板套件（10+ 模板組合）: $99-$299

---

## Architecture

### System Overview

```
┌──────────────┐     ┌──────────────────┐     ┌─────────────┐
│ Tetora CLI   │────▶│ Tetora Store API │────▶│ Store DB    │
│ (client)     │◀────│ (registry.tetora │     │ (Postgres)  │
│              │     │  .dev)           │     └─────────────┘
├──────────────┤     ├──────────────────┤     ┌─────────────┐
│ Dashboard    │────▶│ Auth (GitHub     │────▶│ Stripe      │
│ (browser)    │     │  OAuth)          │     │ (payments)  │
└──────────────┘     └──────────────────┘     └─────────────┘
```

### Registry API (遠端)

**Base URL:** `https://registry.tetora.dev/v1`

```
GET    /templates                    # 搜尋/瀏覽模板
GET    /templates/{id}               # 模板詳情
GET    /templates/{id}/download      # 下載模板 JSON
POST   /templates                    # 發佈模板 (auth required)
PUT    /templates/{id}               # 更新模板
DELETE /templates/{id}               # 下架模板
POST   /templates/{id}/review        # 提交評分/評論
GET    /templates/{id}/stats         # 下載數、評分等

GET    /categories                   # 分類列表
GET    /featured                     # 推薦模板
GET    /authors/{id}                 # 作者頁面

POST   /auth/login                   # GitHub OAuth
POST   /purchases                    # 購買付費模板 (Stripe)
GET    /purchases                    # 已購買列表
```

### Template Package Format

```json
{
  "manifest": {
    "name": "invoice-reconciliation",
    "version": "1.2.0",
    "description": "Automated invoice matching and reconciliation",
    "author": {
      "name": "takuma",
      "github": "TakumaLee"
    },
    "license": "MIT",
    "category": "finance",
    "tags": ["accounting", "automation", "reconciliation"],
    "pricing": {
      "type": "free",
      "price": 0
    },
    "requirements": {
      "tetoraVersion": ">=2.0.0",
      "tools": ["web_search"],
      "externalServices": []
    },
    "screenshots": ["url1", "url2"],
    "readme": "# Invoice Reconciliation\n\n..."
  },
  "workflow": {
    "name": "invoice-reconciliation",
    "steps": [...]
  }
}
```

### Client-side Integration (Tetora binary)

```go
// config.go
type StoreConfig struct {
    Enabled    bool   `json:"enabled"`
    RegistryURL string `json:"registryUrl,omitempty"` // default: https://registry.tetora.dev/v1
    AuthToken  string `json:"authToken,omitempty"`    // GitHub OAuth token
    CacheDir   string `json:"cacheDir,omitempty"`     // local cache for downloaded templates
}
```

**New endpoints on local Tetora daemon:**
```
GET    /api/store/templates          # Proxy to registry + merge with local
GET    /api/store/templates/{id}     # Fetch template detail from registry
POST   /api/store/templates/{id}/install  # Download + install locally
POST   /api/store/publish            # Package + upload local workflow to registry
GET    /api/store/purchases          # User's purchased templates
```

---

## Implementation Phases

### Phase 0: Foundation (先做 spec-dashboard-visibility.md)

Capabilities tab 上線，本地 template/skill/tool 可瀏覽。這是 marketplace UI 的基礎。

### Phase 1: Local Store Experience (MVP)

**Goal:** 在 Dashboard 中模擬 marketplace 體驗，但資料全在本地。

1. **Template Card 升級**
   - 加 category badge、tag、rating placeholder
   - 加 "Share" button（匯出為 JSON）
   - 加 "Import" button（從 JSON 安裝）

2. **Template Import/Export CLI**
   ```
   tetora template export my-workflow -o my-workflow.json
   tetora template import ./my-workflow.json
   tetora template validate ./my-workflow.json
   ```

3. **Template Validation**
   - Schema validation（required fields, step types）
   - Security scan（Sentori scanner, 已有基礎設施）
   - Dependency check（required tools exist?）

**Deliverable:** 用戶可以 export → 分享 JSON → 別人 import。手動 marketplace。

### Phase 2: Registry API (Backend)

**Goal:** 建立遠端 registry，支援搜尋、下載、發佈。

**Tech stack:** Go + PostgreSQL + S3 (template storage)

1. **Registry server** — 獨立 repo `tetora-registry`
   - Template CRUD with versioning
   - Search with full-text + category filter
   - Download counting + popularity ranking

2. **Auth** — GitHub OAuth (簡單、開發者友善)

3. **Tetora client integration**
   - `StoreConfig` in config.go
   - Background sync: cache popular templates locally
   - Dashboard "Store" tab: browse + install

**Deliverable:** `tetora store search "invoice"` → 列出遠端模板 → `tetora store install invoice-reconciliation`

### Phase 3: Payments + Monetization

**Goal:** 付費模板 + Tetora 抽成。

1. **Stripe Connect** — 模板作者連結 Stripe 帳戶
2. **Purchase flow** — Dashboard 內一鍵購買，Stripe Checkout
3. **License verification** — 安裝時驗證購買記錄
4. **Author dashboard** — 收入統計、下載數、評分

### Phase 4: Community + Discovery

1. **評分系統** — 1-5 stars + text review
2. **Featured/Curated** — 官方推薦模板
3. **Collections** — 「Finance Pack」「HR Pack」模板組合
4. **Author profiles** — 展示發佈的模板 + 信譽分
5. **Template versioning** — 已安裝的模板可升級

---

## Dashboard UI: Store Tab

### Phase 1 (Local): Capabilities tab 加 Import/Export

在現有 Templates section 加兩個按鈕：
```
[Import JSON] [Export Selected]
```

### Phase 2+: 新增 Store tab

```
Sidebar:
├── Dashboard
├── Chat
├── Operations
│   ├── Agents
│   ├── Workflows
│   ├── Tasks
│   ├── Capabilities    (本地 tools/skills/templates)
│   └── Files
├── Store               (新增，遠端 marketplace)
│   ├── Browse
│   ├── My Purchases
│   └── Publish
└── Settings
```

**Store Browse 頁面：**
```
┌─────────────────────────────────────────────────┐
│ 🏪 Tetora Store              [Search: ________] │
│                                                  │
│ Featured                                         │
│ ┌──────────┬──────────┬──────────┐               │
│ │ Invoice  │ Employee │ CI/CD    │               │
│ │ Recon.   │ Onboard  │ Pipeline │               │
│ │ ⭐4.8    │ ⭐4.6    │ ⭐4.9    │               │
│ │ FREE     │ $15      │ $25      │               │
│ │ 1.2k ↓   │ 890 ↓    │ 2.1k ↓   │               │
│ └──────────┴──────────┴──────────┘               │
│                                                  │
│ Categories: [All] [Finance] [HR] [DevOps] [...]  │
│                                                  │
│ Popular This Week                                │
│ ┌──────────┬──────────┬──────────┬──────────┐    │
│ │ ...      │ ...      │ ...      │ ...      │    │
│ └──────────┴──────────┴──────────┴──────────┘    │
└─────────────────────────────────────────────────┘
```

---

## Skill Marketplace (Extension)

Same infrastructure 可以擴展到 Skills：

| 項目 | Templates | Skills |
|---|---|---|
| 格式 | JSON workflow definition | Directory (SKILL.md + metadata.json) |
| 安全性 | Low risk (declarative) | High risk (可執行代碼) |
| 審核 | 自動 schema validation | Sentori scan + 人工審核 |
| Package | Single JSON file | Tar.gz archive |

Skills marketplace 風險較高（arbitrary code execution），Phase 3+ 再做，先確保 template marketplace 成熟。

---

## Competitive Landscape

| 平台 | 模式 | Tetora 差異 |
|---|---|---|
| n8n Templates | 免費社群分享 | Tetora 有付費模板 + agent 整合 |
| Zapier Templates | 免費，鎖定平台 | Tetora 開源 + 本地執行 |
| GitHub Actions Marketplace | 免費，code-based | Tetora 是 no-code workflow |
| Hugging Face Spaces | 免費/Pro | Tetora 專注 workflow orchestration |

**Tetora 的護城河：**
- Agent-native（模板可以包含 multi-agent 協作）
- External steps（模板可以包含人工審核節點）
- 本地執行（企業資料不出境）
- Discord 整合（模板可以包含通知/互動步驟）

---

## Success Metrics

| Phase | Metric | Target |
|---|---|---|
| Phase 1 | Template export/import 使用次數 | 50/month |
| Phase 2 | Registry 模板數量 | 200+ |
| Phase 2 | Monthly active installers | 500+ |
| Phase 3 | 付費模板 GMV | $5k/month (first year) |
| Phase 4 | 模板作者數量 | 50+ |

---

## Implementation Order

1. ✅ spec-dashboard-visibility.md (Capabilities tab)
2. Phase 1: Import/Export + Validation (~1 week)
3. Phase 2: Registry API + Client integration (~2-3 weeks)
4. Phase 3: Stripe + Payments (~1-2 weeks)
5. Phase 4: Community features (~2 weeks)

## Open Questions

- [ ] Registry hosting: self-hosted or Cloudflare Workers?
- [ ] Auth: GitHub only or also email/password?
- [ ] 抽成比例: 30% (App Store standard) or 20% (更吸引作者)?
- [ ] 免費模板是否需要帳號才能發佈?
- [ ] 企業 private registry (self-hosted registry for orgs)?
