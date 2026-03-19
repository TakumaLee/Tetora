#!/usr/bin/env bash
#
# migrate-multi-tenant.sh — Migrate existing DB files to per-client layout.
#
# Before:  ~/.tetora/dbs/history.db
# After:   ~/.tetora/clients/cli_default/dbs/history.db
#          ~/.tetora/dbs/history.db → symlink to ../clients/cli_default/dbs/history.db
#
# Prerequisites:
#   - Stop the tetora daemon before running this script.
#   - This script is idempotent: re-running it on an already-migrated layout is a no-op.
#
# Rollback:
#   1. Stop tetora daemon.
#   2. rm ~/.tetora/dbs/history.db (the symlink)
#   3. mv ~/.tetora/clients/cli_default/dbs/history.db ~/.tetora/dbs/history.db
#   4. Repeat for taskboard.db and tasks.db if moved.
#   5. Restart tetora daemon.
#
set -euo pipefail

TETORA_HOME="${TETORA_HOME:-$HOME/.tetora}"
CLIENT_ID="cli_default"
SRC_DIR="$TETORA_HOME/dbs"
DST_DIR="$TETORA_HOME/clients/$CLIENT_ID/dbs"

# DB files to migrate.
DBS=("history.db" "taskboard.db" "tasks.db")
# Associated WAL/SHM files.
SUFFIXES=("" "-wal" "-shm")

log() { printf "[migrate] %s\n" "$1"; }

# --- Pre-flight checks ---

if ! command -v sqlite3 &>/dev/null; then
    log "WARNING: sqlite3 not found — skipping integrity checks."
    SKIP_INTEGRITY=1
else
    SKIP_INTEGRITY=0
fi

# Check if daemon is running.
if [ -f "$TETORA_HOME/runtime/tetora.pid" ]; then
    PID=$(cat "$TETORA_HOME/runtime/tetora.pid" 2>/dev/null || echo "")
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        log "ERROR: tetora daemon is running (PID $PID). Stop it first: tetora service stop"
        exit 1
    fi
fi

# --- Migration ---

mkdir -p "$DST_DIR"

migrated=0
skipped=0

for db in "${DBS[@]}"; do
    src="$SRC_DIR/$db"

    # Skip if source doesn't exist.
    if [ ! -f "$src" ]; then
        continue
    fi

    # Skip if source is already a symlink (already migrated).
    if [ -L "$src" ]; then
        log "SKIP: $src is already a symlink (already migrated)."
        skipped=$((skipped + 1))
        continue
    fi

    dst="$DST_DIR/$db"

    # Skip if destination already exists (avoid overwrite).
    if [ -f "$dst" ]; then
        log "SKIP: $dst already exists. Manual intervention required."
        skipped=$((skipped + 1))
        continue
    fi

    # Integrity check before move.
    if [ "$SKIP_INTEGRITY" -eq 0 ]; then
        if ! sqlite3 "$src" "PRAGMA integrity_check;" >/dev/null 2>&1; then
            log "ERROR: integrity check failed for $src. Aborting."
            exit 1
        fi
    fi

    # Move main DB file.
    log "Moving $src → $dst"
    mv "$src" "$dst"

    # Move WAL/SHM files if they exist.
    for suffix in "${SUFFIXES[@]}"; do
        if [ -n "$suffix" ] && [ -f "$src$suffix" ]; then
            log "  Moving $src$suffix → $dst$suffix"
            mv "$src$suffix" "$dst$suffix"
        fi
    done

    # Create symlink for backward compatibility.
    # Use relative path so it works if ~/.tetora is moved.
    rel_dst="../clients/$CLIENT_ID/dbs/$db"
    ln -s "$rel_dst" "$src"
    log "  Symlink: $src → $rel_dst"

    migrated=$((migrated + 1))
done

log "Done. Migrated: $migrated, Skipped: $skipped."

if [ "$migrated" -gt 0 ]; then
    log "You can now start the daemon: tetora service start"
fi
