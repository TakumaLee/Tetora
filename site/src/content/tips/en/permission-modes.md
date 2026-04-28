---
title: "Permission Modes — Control What Your Agents Can Change"
lang: en
date: "2026-04-28"
excerpt: "Don't let your content agent touch your infrastructure config. Learn how to lock down agent write access with Tetora's three permission modes."
description: "A practical guide to Tetora's permission modes — review, plan, and acceptEdits — so each agent only touches the files it's supposed to."
---

## The Problem

You dispatch an agent to draft a blog post. It finishes the draft, then notices your `tetora.config.json` looks "inconsistent" and rewrites it. Now your cron jobs are broken.

Write access without boundaries is how small helpfulness becomes a big incident.

## Three Permission Modes

Tetora provides three modes that control what an agent is allowed to write:

| Mode | Can Read | Can Write | Use When |
|---|---|---|---|
| `review` | ✅ everything | ❌ nothing | Auditing, code review, research |
| `plan` | ✅ everything | ✅ task specs only | Planning, ticket creation |
| `acceptEdits` | ✅ everything | ✅ within scope | Active implementation |

Set the default per agent in `tetora.config.json`:

```json
{
  "agents": {
    "kohaku": {
      "permission_mode": "acceptEdits",
      "scope": ["site/src/content/**"]
    },
    "hisui": {
      "permission_mode": "review",
      "scope": ["**/*"]
    },
    "tekkou": {
      "permission_mode": "acceptEdits",
      "scope": ["src/**", "tests/**"]
    }
  }
}
```

`kohaku` can write anything under `site/src/content/` — and nothing else. `hisui` is read-only everywhere. `tekkou` owns the source and test directories.

## Per-Dispatch Override

Sometimes you want to bump an agent up or down for a single task without changing the config:

```bash
# Elevate hisui for a one-off fix
tetora dispatch --agent hisui --permission acceptEdits --scope "docs/**" \
  "Fix all broken links in the documentation"

# Lock tekkou to review for a sensitive audit
tetora dispatch --agent tekkou --permission review \
  "Audit database migration files for correctness"
```

The override applies only to that dispatch. The next task uses the agent's default from config.

## Scope Boundary Enforcement

When an agent with `acceptEdits` tries to write outside its declared `scope`, Tetora blocks the write and logs it as a scope violation rather than silently failing. This means you'll see:

```
[SCOPE VIOLATION] tekkou attempted write to site/src/content/tips/
  Allowed: src/**, tests/**
  Action: BLOCKED — logged to tasks/scope-violations.log
```

No silent drift. No after-the-fact investigation.

## Key Takeaway

Assign the narrowest permission your agent actually needs. A content agent doesn't need production database access. A code reviewer doesn't need commit rights. Setting this up once in `tetora.config.json` prevents an entire class of "the agent did _what_?" incidents.

Next: see **Cost Governance Per Role** to apply the same principle to model spend.
