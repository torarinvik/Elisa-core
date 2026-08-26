#!/usr/bin/env bash
# Build the native SDL3 desktop shell.
#
# The core is compiled ONCE to a native object from the same export surface the
# wasm build uses (src/wasm/wasm_exports.elisa): every entry point there is
# scalar C ABI, which is exactly what a C host needs too. No second export file.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/env.sh"
mkdir -p "$ROOT/build"

SDL_CFLAGS="$(pkg-config --cflags sdl3 2>/dev/null || echo "-I/opt/homebrew/include")"
SDL_LIBS="$(pkg-config --libs sdl3 2>/dev/null || echo "-L/opt/homebrew/lib -lSDL3")"

"$ELISACORE_BIN" -emit obj -O2 -o "$ROOT/build/nwcore_native.o" \
    "$ROOT/src/wasm/wasm_exports.elisa"

cc -O2 -std=c11 $SDL_CFLAGS \
    -o "$ROOT/build/nw-sdl3" \
    "$ROOT/shells/sdl3/main.c" \
    "$ROOT/build/nwcore_native.o" \
    $SDL_LIBS

printf 'built %s\n' "$ROOT/build/nw-sdl3"
printf 'run it with: %s\n' "$ROOT/build/nw-sdl3"
printf '  space = play/pause, right arrow = step, q/esc = quit\n'
