#!/usr/bin/env python3
"""Write metadata.json for Lua frontend bundle scripts."""

from __future__ import annotations

import argparse
import json
import subprocess
from datetime import datetime, timezone
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, help="Path to metadata.json")
    parser.add_argument("--bundle-type", required=True, help="Bundle type label, for example profile or baseline")
    parser.add_argument("--repo-root", required=True, help="Repository root path")
    parser.add_argument("--out-dir", required=True, help="Bundle output directory")
    parser.add_argument(
        "--setting",
        action="append",
        default=[],
        help="Repeated key=value setting entry to store under the settings object",
    )
    parser.add_argument(
        "--command",
        action="append",
        default=[],
        help="Repeated key=value command entry to store under the commands object",
    )
    return parser.parse_args()


def parse_key_value_pairs(items: list[str], *, label: str) -> dict[str, str]:
    result: dict[str, str] = {}
    for item in items:
        if "=" not in item:
            raise SystemExit(f"invalid {label} entry {item!r}; expected key=value")
        key, value = item.split("=", 1)
        if not key:
            raise SystemExit(f"invalid {label} entry {item!r}; key must not be empty")
        result[key] = value
    return result


def run_text(cmd: list[str], *, cwd: Path | None = None) -> tuple[bool, str]:
    try:
        completed = subprocess.run(
            cmd,
            cwd=cwd,
            check=True,
            text=True,
            capture_output=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return False, ""
    return True, completed.stdout.strip()


def git_info(repo_root: Path) -> dict[str, str]:
    ok_head, head = run_text(["git", "rev-parse", "HEAD"], cwd=repo_root)
    if not ok_head or not head:
        return {"head": "unknown", "branch": "unknown", "status": "unknown"}

    ok_branch, branch = run_text(["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=repo_root)
    ok_status, status_text = run_text(["git", "status", "--short", "--untracked-files=no"], cwd=repo_root)
    status = "unknown"
    if ok_status:
        status = "dirty" if status_text else "clean"

    return {
        "head": head,
        "branch": branch if ok_branch and branch else "unknown",
        "status": status,
    }


def main() -> int:
    args = parse_args()
    repo_root = Path(args.repo_root)
    output_path = Path(args.output)

    _, hostname = run_text(["hostname"])
    _, uname = run_text(["uname", "-a"])

    data = {
        "bundle_type": args.bundle_type,
        "generated_at_utc": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "repo_root": args.repo_root,
        "out_dir": args.out_dir,
        "hostname": hostname or "unknown",
        "uname": uname or "unknown",
        "git": git_info(repo_root),
        "settings": parse_key_value_pairs(args.setting, label="setting"),
        "commands": parse_key_value_pairs(args.command, label="command"),
    }

    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())