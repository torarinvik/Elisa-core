#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE0="${ELISAC_STAGE0_BIN:-}"
STAGE1="${ELISAC_STAGE1_BIN:-}"
STAGE1_ROOT="${ELISAC_STAGE1_ROOT:-}"
if [[ -z "$STAGE0" || -z "$STAGE1" ]]; then
    echo "set ELISAC_STAGE0_BIN and ELISAC_STAGE1_BIN to the exact compiler products to compare" >&2
    exit 2
fi
for compiler in "$STAGE0" "$STAGE1"; do
    if [[ ! -x "$compiler" ]]; then
        echo "compiler is not executable: $compiler" >&2
        exit 2
    fi
done
if [[ ! -d "$STAGE1_ROOT" ]]; then
    echo "set ELISAC_STAGE1_ROOT to the matching Stage1 build root (runtime linkage uses it)" >&2
    exit 2
fi
STAGE1_PROVENANCE="$STAGE1_ROOT/scripts/stage1_provenance.py"
if [[ ! -f "$STAGE1_PROVENANCE" ]]; then
    echo "Stage1 provenance checker is missing: $STAGE1_PROVENANCE" >&2
    exit 2
fi
python3 "$STAGE1_PROVENANCE" check "$STAGE1_ROOT" "$STAGE1"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/hierarchy-root-store-diff.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM HUP

for compiler in "$STAGE0" "$STAGE1"; do
    name="$(basename "$compiler")"
    positive_emit=llvm
    negative_emit=llvm
    if [[ "$compiler" == "$STAGE1" ]]; then
        positive_emit=exe
        negative_emit=exe
        run_compiler() { (cd -- "$STAGE1_ROOT" && "$compiler" "$@"); }
    else
        run_compiler() { (cd -- "$ROOT" && "$compiler" "$@"); }
    fi
    if [[ "$compiler" == "$STAGE1" ]]; then
        if ! run_compiler -emit exe -O0 -o "$WORK/$name" "$ROOT/test/hierarchy_root_store/positive.elisa" >"$WORK/$name-positive.log" 2>&1; then
            echo "FAIL $name rejected the store-backed plain hierarchy" >&2
            cat "$WORK/$name-positive.log" >&2
            exit 1
        fi
        if ! "$WORK/$name"; then
            echo "FAIL $name hierarchy-root store positive returned nonzero" >&2
            exit 1
        fi
    elif ! run_compiler -emit "$positive_emit" -O0 "$ROOT/test/hierarchy_root_store/positive.elisa" >"$WORK/$name-positive.log" 2>&1; then
        echo "FAIL $name rejected the store-backed plain hierarchy" >&2
        cat "$WORK/$name-positive.log" >&2
        exit 1
    fi
    if run_compiler -emit "$negative_emit" -O0 "$ROOT/test/hierarchy_root_store/negative_inline_has_no_store.elisa" >"$WORK/$name-negative.log" 2>&1; then
        echo "FAIL $name synthesized Store for an inline hierarchy with handle width only" >&2
        exit 1
    fi
    if ! rg -qi 'Node\.Store|unknown type|unknown.*Store|store type|declined' "$WORK/$name-negative.log"; then
        echo "FAIL $name rejected the negative control for an unrelated reason" >&2
        cat "$WORK/$name-negative.log" >&2
        exit 1
    fi
done

echo "Stage0/Stage1 hierarchy-root Store differential OK"
