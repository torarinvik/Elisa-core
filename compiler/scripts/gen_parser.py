#!/usr/bin/env python3
"""Sync parser.go in the src layout.

This script intentionally preserves the checked-in canonical source file in
`src/parser/parser.go` so rerunning it cannot restore the pre-`src/` layout or
an older parser implementation.
"""

from pathlib import Path


def main() -> None:
    root = Path(__file__).resolve().parent.parent
    outpath = root / "src" / "parser" / "parser.go"
    content = outpath.read_text(encoding="utf-8")
    outpath.write_text(content, encoding="utf-8")
    print(f"Synced {outpath}")


if __name__ == "__main__":
    main()
