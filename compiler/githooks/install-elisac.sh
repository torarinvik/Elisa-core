#!/bin/sh
# Rebuild + install the elisac compiler so the canonical binary
# (~/.elisac/elisac) never lags the source tree. Invoked by the
# post-commit / post-merge / post-checkout hooks in this directory.
# Incremental builds are ~1-2s; a cold build is ~10s.
COMPILER_DIR="$(git rev-parse --show-toplevel)/compiler"
[ -d "$COMPILER_DIR" ] || exit 0
mkdir -p "${HOME}/.elisac"
if go build -C "$COMPILER_DIR" -o "${HOME}/.elisac/elisac" ./src 2>/tmp/elisac-autoinstall.log; then
    echo "[hook] elisac reinstalled -> ${HOME}/.elisac/elisac"
else
    echo "[hook] elisac rebuild FAILED (binary left as-is); see /tmp/elisac-autoinstall.log" >&2
fi
exit 0
