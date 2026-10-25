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
LOCAL_BIN="$COMPILER_DIR/bin/elisac"
LOCAL_RECEIPT="$LOCAL_BIN.provenance.json"
LOCAL_BACKUP_DIR=""
LOCAL_BIN_BACKUP=""
LOCAL_RECEIPT_BACKUP=""
LOCAL_BIN_RESTORE=""
LOCAL_RECEIPT_RESTORE=""
LOCAL_HAD_BIN=0
LOCAL_HAD_RECEIPT=0
LOCAL_PUBLISHING=0

restore_local_pair() {
    if [ "$LOCAL_HAD_BIN" = 1 ]; then
        cp -Pp "$LOCAL_BIN_BACKUP" "$LOCAL_BIN_RESTORE" && mv -f "$LOCAL_BIN_RESTORE" "$LOCAL_BIN" || return 1
    else
        rm -f "$LOCAL_BIN" || return 1
    fi
    if [ "$LOCAL_HAD_RECEIPT" = 1 ]; then
        cp -Pp "$LOCAL_RECEIPT_BACKUP" "$LOCAL_RECEIPT_RESTORE" && mv -f "$LOCAL_RECEIPT_RESTORE" "$LOCAL_RECEIPT" || return 1
    else
        rm -f "$LOCAL_RECEIPT" || return 1
    fi
    return 0
}

cleanup() {
    if [ "$LOCAL_PUBLISHING" = 1 ]; then
        if restore_local_pair; then
            LOCAL_PUBLISHING=0
        else
            echo "[hook] could not restore compiler/bin Stage0 pair; recovery files preserved in $LOCAL_BACKUP_DIR" >&2
        fi
    fi
    rm -f "$LOCAL_BIN_TEMP" "$LOCAL_RECEIPT_TEMP"
    if [ "$LOCAL_PUBLISHING" = 0 ] && [ -n "$LOCAL_BACKUP_DIR" ]; then
        rm -rf "$LOCAL_BACKUP_DIR"
    fi
}

trap cleanup 0
trap 'exit 1' HUP INT TERM
LOCAL_BACKUP_DIR="$(mktemp -d "$COMPILER_DIR/bin/.elisac-stage0-backup.XXXXXX")" || {
    echo "[hook] could not create a unique Stage0 rollback directory; compiler/bin pair retained" >&2
    exit 0
}
LOCAL_BIN_BACKUP="$LOCAL_BACKUP_DIR/elisac"
LOCAL_RECEIPT_BACKUP="$LOCAL_BACKUP_DIR/elisac.provenance.json"
LOCAL_BIN_RESTORE="$LOCAL_BACKUP_DIR/elisac.restore"
LOCAL_RECEIPT_RESTORE="$LOCAL_BACKUP_DIR/elisac.provenance.restore.json"
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
if [ -e "$LOCAL_BIN" ] || [ -L "$LOCAL_BIN" ]; then
    cp -Pp "$LOCAL_BIN" "$LOCAL_BIN_BACKUP" || {
        echo "[hook] could not save the previous compiler/bin Stage0 binary; local pair retained" >&2
        exit 0
    }
    LOCAL_HAD_BIN=1
fi
if [ -e "$LOCAL_RECEIPT" ] || [ -L "$LOCAL_RECEIPT" ]; then
    cp -Pp "$LOCAL_RECEIPT" "$LOCAL_RECEIPT_BACKUP" || {
        echo "[hook] could not save the previous compiler/bin Stage0 receipt; local pair retained" >&2
        exit 0
    }
    LOCAL_HAD_RECEIPT=1
fi
LOCAL_PUBLISHING=1
if ! mv -f "$LOCAL_BIN_TEMP" "$LOCAL_BIN"; then
    echo "[hook] could not publish the new compiler/bin Stage0 binary; restoring the previous pair" >&2
    exit 0
fi
if ! mv -f "$LOCAL_RECEIPT_TEMP" "$LOCAL_RECEIPT"; then
    echo "[hook] could not publish the new compiler/bin Stage0 receipt; restoring the previous pair" >&2
    exit 0
fi
LOCAL_PUBLISHING=0
echo "[hook] elisac-stage0 reinstalled -> $INSTALL_BIN"
exit 0
