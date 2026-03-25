#!/usr/bin/env python3
"""Patch docs frontmatter: add order + description where missing."""

import pathlib
import re

DOCS_DIR = pathlib.Path(__file__).parent.parent / "src" / "content" / "docs"

ORDER_MAP = {
    "configuration": 1,
    "workflow": 2,
    "hooks": 3,
    "taskboard": 4,
    "mcp": 5,
    "discord-multitasking": 6,
    "troubleshooting": 7,
}

# English descriptions (used as fallback for all languages)
DESCRIPTION_MAP = {
    "configuration": "Configure Tetora via ~/.tetora/config.json — models, providers, and runtime settings.",
    "workflow": "Define multi-step task pipelines with JSON workflows and agent orchestration.",
    "hooks": "Integrate with Claude Code Hooks for real-time session observation.",
    "taskboard": "Track tasks, priorities, and agent assignments with the built-in taskboard.",
    "mcp": "Expose Tetora capabilities to any MCP-compatible client.",
    "discord-multitasking": "Run multiple agents concurrently via Discord threads.",
    "troubleshooting": "Common issues and solutions for Tetora setup and operation.",
}

FM_RE = re.compile(r"^---\n(.*?)\n---", re.DOTALL)


def patch_file(path: pathlib.Path) -> bool:
    text = path.read_text(encoding="utf-8")
    m = FM_RE.match(text)
    if not m:
        print(f"  SKIP (no frontmatter): {path}")
        return False

    fm_block = m.group(1)
    topic = path.stem  # e.g. "configuration"
    order = ORDER_MAP.get(topic)
    desc = DESCRIPTION_MAP.get(topic)

    if order is None:
        print(f"  SKIP (unknown topic): {path}")
        return False

    lines = fm_block.split("\n")
    has_order = any(l.startswith("order:") for l in lines)
    has_desc = any(l.startswith("description:") for l in lines)

    if has_order and has_desc:
        return False

    new_lines = list(lines)
    if not has_order:
        new_lines.append(f"order: {order}")
    if not has_desc:
        new_lines.append(f'description: "{desc}"')

    new_fm = "\n".join(new_lines)
    new_text = f"---\n{new_fm}\n---{text[m.end():]}"
    path.write_text(new_text, encoding="utf-8")
    return True


def main():
    changed = 0
    for lang_dir in sorted(DOCS_DIR.iterdir()):
        if not lang_dir.is_dir():
            continue
        for md in sorted(lang_dir.glob("*.md")):
            if patch_file(md):
                print(f"  PATCHED: {md.relative_to(DOCS_DIR)}")
                changed += 1
    print(f"\nDone. {changed} files patched.")


if __name__ == "__main__":
    main()
