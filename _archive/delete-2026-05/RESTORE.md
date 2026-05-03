# Archive: delete-2026-05

Archived on: 2026-05-03
Branch: chore/delete-leaf-packages-2026-05
Decision: These packages were confirmed as easy to rebuild in the AI era, overkill for a personal project, or superseded by better approaches (LLM, MCP, etc.)

## Packages

| Package | Reason |
|---------|--------|
| pwa | PWA manifest — not needed |
| sprite | Agent sprite animations — not needed |
| nlp | Dictionary sentiment analysis — LLM does this better |
| classify | Complexity classification — LLM does this better |
| bm25 | Keyword search — use LLM instead |
| quickaction | Quick actions, thin layer |
| quiet | Quiet hours — inline into cron |
| estimate | Task estimation — inline or remove |
| messaging | Messaging abstraction — too thin |
| upload | Upload handler — not needed |
| webhook | Webhook receiver — not needed |
| sandbox | Docker sandbox — too complex for personal |
| tmux | Tmux integration — not needed |
| plugin | Plugin system — MCP replaces this |
| pairing | Device pairing — not needed for personal |
| sla | SLA monitoring — overkill for personal |
| trust | Trust system — not needed for personal |
| oauth | OAuth — will be handled differently |
| completion | Command completion |
| push | Web push notifications |
| handoff | Agent handoff |
| canvas | MCP canvas |
| export | Export command |
| telemetry | Telemetry reporting |
| benchmark | Only used bm25 (also archived) |

## Restore Instructions

To restore a package:
1. `git mv _archive/delete-2026-05/internal/PKGNAME internal/PKGNAME`
2. Re-add the import and usage in the relevant files (see git history for the original wiring)
3. `go build ./...`
