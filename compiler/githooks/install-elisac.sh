#!/bin/sh
# Rebuild + install the Go compiler as ~/.elisac/elisac-stage0 so the canonical
# binary never lags the source tree. Invoked by the post-commit / post-merge /
# post-checkout hooks in this directory. Incremental builds are ~1-2s; a cold
# build is ~10s.
#
# Named by STAGE: the self-hosted compiler installs beside it as elisac-stage1
# (Elisa-compiler scripts/install_stage1.sh), and the explicit names say which
# one a command line is using. There is deliberately no bare `elisac`; a stale
# one from before the rename is removed so nothing can resolve to it by accident.
# Only the primary checkout installs. Agents work in linked worktrees (scratch branches with
# uncommitted experiments); a checkout there must not replace the shared ~/.elisac binary that
# every other session compiles with. In a linked worktree --git-dir differs from
# --git-common-dir.
if [ "$(git rev-parse --absolute-git-dir 2>/dev/null)" != "$(cd "$(git rev-parse --git-common-dir 2>/dev/null)" 2>/dev/null && pwd -P)" ]; then
    exit 0
fi
COMPILER_DIR="$(git rev-parse --show-toplevel)/compiler"
[ -d "$COMPILER_DIR" ] || exit 0
# Shared defaults must come from committed inputs, not another agent's experiment.
SOURCE_ROOT="$(git rev-parse --show-toplevel)"
if [ -n "$(git -C "$SOURCE_ROOT" status --porcelain -- compiler)" ]; then
    echo "[hook] compiler checkout has uncommitted changes; installed compiler retained" >&2
    exit 0
fi
mkdir -p "${HOME}/.elisac" "$COMPILER_DIR/bin"
LOCAL_BIN_TEMP="$COMPILER_DIR/bin/elisac.tmp.$$"
LOCAL_RECEIPT_TEMP="$LOCAL_BIN_TEMP.provenance.json"
trap 'rm -f "$LOCAL_BIN_TEMP" "$LOCAL_RECEIPT_TEMP"' EXIT HUP INT TERM
INSTALL_BIN="${HOME}/.elisac/elisac-stage0"
PROVENANCE_SCRIPT="${ELISA_STAGE0_PROVENANCE_SCRIPT:-$COMPILER_DIR/scripts/stage0_provenance.py}"
PYTHON_TOOL="${PYTHON_BIN:-python3}"

# The bundled helper stages a fresh build and atomically publishes a checked
# binary/receipt pair. Do not fall back to VCS timestamps: default binary bytes
# must stay stable across documentation and test commits in a standalone clone.
if [ ! -f "$PROVENANCE_SCRIPT" ] || ! command -v "$PYTHON_TOOL" >/dev/null 2>&1; then
    echo "[hook] Stage0 provenance helper/Python unavailable; installed compiler retained" >&2
    exit 0
fi
if ! "$PYTHON_TOOL" "$PROVENANCE_SCRIPT" build --root "$COMPILER_DIR" --binary "$INSTALL_BIN" >/tmp/elisac-autoinstall.log 2>&1; then
    echo "[hook] elisac provenance build FAILED (binary left as-is); see /tmp/elisac-autoinstall.log" >&2
    exit 0
fi

rm -f "${HOME}/.elisac/elisac"
# Stage1 scripts also read compiler/bin/elisac. Stage both copies before either
# replacement; hash-bound receipt checks reject a transient mismatched pair.
cp "$INSTALL_BIN" "$LOCAL_BIN_TEMP" && cp "$INSTALL_BIN.provenance.json" "$LOCAL_RECEIPT_TEMP" || {
    echo "[hook] could not copy Stage0 binary/receipt into compiler/bin; installed compiler retained" >&2
    exit 0
}
mv -f "$LOCAL_BIN_TEMP" "$COMPILER_DIR/bin/elisac" || exit 0
mv -f "$LOCAL_RECEIPT_TEMP" "$COMPILER_DIR/bin/elisac.provenance.json" || exit 0
echo "[hook] elisac-stage0 reinstalled -> $INSTALL_BIN"
exit 0
