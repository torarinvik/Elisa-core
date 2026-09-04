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
COMPILER_DIR="$(git rev-parse --show-toplevel)/compiler"
[ -d "$COMPILER_DIR" ] || exit 0
mkdir -p "${HOME}/.elisac"
if go build -C "$COMPILER_DIR" -o "${HOME}/.elisac/elisac-stage0" ./src 2>/tmp/elisac-autoinstall.log; then
    rm -f "${HOME}/.elisac/elisac"
    echo "[hook] elisac-stage0 reinstalled -> ${HOME}/.elisac/elisac-stage0"
else
    echo "[hook] elisac rebuild FAILED (binary left as-is); see /tmp/elisac-autoinstall.log" >&2
fi
exit 0
