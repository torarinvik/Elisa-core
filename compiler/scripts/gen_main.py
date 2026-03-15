#!/usr/bin/env python3
"""Sync main.go in the src layout.

This script intentionally preserves the checked-in canonical source file in
`src/main.go` so rerunning it cannot restore the pre-`src/` layout or an older
entry-point implementation.
"""

from pathlib import Path


def main() -> None:
    root = Path(__file__).resolve().parent.parent
    outpath = root / "src" / "main.go"
    content = outpath.read_text(encoding="utf-8")
    outpath.write_text(content, encoding="utf-8")
    print(f"Synced {outpath}")


if __name__ == "__main__":
    main()
