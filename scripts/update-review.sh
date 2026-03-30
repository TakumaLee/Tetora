#!/bin/sh
# update-review.sh — refreshes the dynamic sections of tasks/review.md
# Called automatically by .git/hooks/post-merge after every git pull.

set -e

REVIEW="tasks/review.md"
REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if [ ! -f "$REVIEW" ]; then
  echo "[update-review] $REVIEW not found, skipping."
  exit 0
fi

TODAY=$(date +%Y-%m-%d)
TMP=$(mktemp)
COMMITS_FILE=$(mktemp)
DIFF_FILE=$(mktemp)

git log --oneline -10 > "$COMMITS_FILE"
git diff --stat HEAD 2>/dev/null > "$DIFF_FILE" || echo "(none)" > "$DIFF_FILE"

# Replace content between marker pairs using Python (available on macOS/Linux)
python3 - "$REVIEW" "$COMMITS_FILE" "$DIFF_FILE" "$TODAY" "$TMP" << 'PYEOF'
import sys

review_path, commits_path, diff_path, today, out_path = sys.argv[1:]

with open(review_path) as f:
    lines = f.readlines()
with open(commits_path) as f:
    commits = f.read().rstrip()
with open(diff_path) as f:
    diff = f.read().rstrip() or "(none)"

START = "<!-- AUTO-UPDATED by scripts/update-review.sh -->"
END   = "<!-- END AUTO-UPDATED -->"
DATE_PREFIX = "> Last updated:"

out = []
skip = False
block = 0  # 0=commits, 1=diff

for line in lines:
    stripped = line.rstrip('\n')

    # Update date line
    if stripped.startswith(DATE_PREFIX):
        out.append(f"{DATE_PREFIX} {today}\n")
        continue

    if stripped == START:
        out.append(line)
        skip = True
        continue

    if stripped == END:
        skip = False
        content = commits if block == 0 else diff
        out.append("```\n")
        out.append(content + "\n")
        out.append("```\n")
        out.append(line)
        block += 1
        continue

    if skip:
        continue

    out.append(line)

with open(out_path, 'w') as f:
    f.writelines(out)
PYEOF

mv "$TMP" "$REVIEW"
rm -f "$COMMITS_FILE" "$DIFF_FILE"

echo "[update-review] tasks/review.md refreshed ($TODAY)"
