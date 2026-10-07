# shellcheck shell=bash
# Host-platform facts for every build/test script, so link lines and tool paths need no
# per-script `uname` branch and no PATH shim (tools/linux_shim, kept only for old callers).
# Sourced, idempotent, safe under `set -euo pipefail`. Every value is a plain exported
# string (no arrays) so child scripts and generated helpers inherit it; expand the flag
# variables UNQUOTED so an empty value vanishes and a multi-flag value splits.
#
#   ELISA_HOST_OS            Darwin | Linux | ...
#   ELISA_LLVM_BIN_DIR       llvm-config --bindir   (clang, opt, llc, llvm-mc, wasm-ld ...)
#   LLVM_CONFIG              $LLVM_CONFIG, else llvm-config on PATH, else Homebrew's (Darwin)
#   ELISA_LLVM_LIBDIR        llvm-config --libdir
#   ELISA_LLVM_LIBS          what replaces `-lLLVM`: -lLLVM when libLLVM.{so,dylib} exists,
#                            else -lLLVM-<major>, else the static archives in one link group
#   ELISA_LD_DEAD_STRIP      -Wl,-dead_strip | -Wl,--gc-sections
#   ELISA_LD_STACK_512M      -Wl,-stack_size,0x20000000 on Darwin; empty elsewhere (GNU ld
#                            has no main-thread stack flag: the stack is `ulimit -s`)
#   ELISA_LINK_EXE_FLAGS     extra flags for every executable link. Linux: -no-pie (stage0
#                            and stage1 objects use absolute relocations) and
#                            -Wl,--unresolved-symbols=ignore-all (the runtime references the
#                            optional elisa_native_callback_* hooks; ld64's -dead_strip drops
#                            the referencing code on macOS, GNU --gc-sections does not, and a
#                            Linux twin of pymodule_runtime_fallback.c is the proper fix).
#   ELISA_LD_ALLOW_UNDEFINED -Wl,-undefined,dynamic_lookup | -Wl,--unresolved-symbols=ignore-all
#   ELISA_SHARED_MODULE_FLAGS  -bundle -undefined dynamic_lookup | -shared -fPIC
#   ELISA_BREW_BIN           /opt/homebrew/bin on Darwin when present, else empty
#   ELISA_PYTHON314_BIN / ELISA_PYTHON314_CONFIG / ELISA_PYTHON312_BIN / ELISA_PYTHON312_CONFIG
#   ELISA_NPROC              online CPU count
#   elisa_sha256 [FILE...]   sha256sum-compatible output (stdin when no file)
if [[ -z "${ELISA_PLATFORM_SOURCED:-}" ]]; then
export ELISA_PLATFORM_SOURCED=1
export ELISA_HOST_OS="${ELISA_HOST_OS:-$(uname -s)}"

if [[ "$ELISA_HOST_OS" == Darwin && -d /opt/homebrew/bin ]]; then
    export ELISA_BREW_BIN="${ELISA_BREW_BIN:-/opt/homebrew/bin}"
else
    export ELISA_BREW_BIN="${ELISA_BREW_BIN:-}"
fi

if [[ -z "${LLVM_CONFIG:-}" ]]; then
    LLVM_CONFIG="$(command -v llvm-config 2>/dev/null || true)"
    if [[ -z "$LLVM_CONFIG" && -x /opt/homebrew/opt/llvm/bin/llvm-config ]]; then
        LLVM_CONFIG=/opt/homebrew/opt/llvm/bin/llvm-config
    fi
    [[ -n "$LLVM_CONFIG" ]] || LLVM_CONFIG="$(command -v llvm-config-21 2>/dev/null || echo llvm-config)"
fi
export LLVM_CONFIG
# Prefer llvm-config's own directory when it is the toolchain bin dir (Homebrew's stable
# opt/llvm/bin rather than the versioned Cellar path --bindir prints).
if [[ -z "${ELISA_LLVM_BIN_DIR:-}" && "$LLVM_CONFIG" == */* && -x "${LLVM_CONFIG%/*}/clang" && -x "${LLVM_CONFIG%/*}/opt" ]]; then
    ELISA_LLVM_BIN_DIR="${LLVM_CONFIG%/*}"
fi
export ELISA_LLVM_BIN_DIR="${ELISA_LLVM_BIN_DIR:-$("$LLVM_CONFIG" --bindir 2>/dev/null || echo /opt/homebrew/opt/llvm/bin)}"
export ELISA_LLVM_LIBDIR="${ELISA_LLVM_LIBDIR:-$("$LLVM_CONFIG" --libdir 2>/dev/null || echo /opt/homebrew/opt/llvm/lib)}"
# Bare `clang` in the scripts: make the LLVM toolchain reachable when PATH lacks one.
command -v clang >/dev/null 2>&1 || export PATH="$PATH:$ELISA_LLVM_BIN_DIR"

if [[ -z "${ELISA_LLVM_LIBS:-}" ]]; then
    _ep_major="$("$LLVM_CONFIG" --version 2>/dev/null | cut -d. -f1)"
    if compgen -G "$ELISA_LLVM_LIBDIR/libLLVM.dylib" >/dev/null || compgen -G "$ELISA_LLVM_LIBDIR/libLLVM.so" >/dev/null; then
        ELISA_LLVM_LIBS="-lLLVM"
    elif [[ -n "$_ep_major" ]] && compgen -G "$ELISA_LLVM_LIBDIR/libLLVM-$_ep_major.so*" >/dev/null; then
        ELISA_LLVM_LIBS="-lLLVM-$_ep_major"
    elif [[ "$ELISA_HOST_OS" != Darwin ]] && "$LLVM_CONFIG" --version >/dev/null 2>&1; then
        # Upstream release tarballs ship no libLLVM*.so: the static archives, one link group.
        ELISA_LLVM_LIBS="-L$ELISA_LLVM_LIBDIR -Wl,--start-group $("$LLVM_CONFIG" --libs --link-static) -Wl,--end-group $("$LLVM_CONFIG" --system-libs --link-static) -lstdc++ -lm"
    else
        ELISA_LLVM_LIBS="-lLLVM"
    fi
    unset _ep_major
fi
export ELISA_LLVM_LIBS

if [[ "$ELISA_HOST_OS" == Darwin ]]; then
    export ELISA_LD_DEAD_STRIP="-Wl,-dead_strip"
    export ELISA_LD_STACK_512M="-Wl,-stack_size,0x20000000"
    export ELISA_LINK_EXE_FLAGS=""
    export ELISA_LD_ALLOW_UNDEFINED="-Wl,-undefined,dynamic_lookup"
    export ELISA_SHARED_MODULE_FLAGS="-bundle -undefined dynamic_lookup"
else
    export ELISA_LD_DEAD_STRIP="-Wl,--gc-sections"
    export ELISA_LD_STACK_512M=""
    export ELISA_LINK_EXE_FLAGS="-no-pie -Wl,--unresolved-symbols=ignore-all"
    export ELISA_LD_ALLOW_UNDEFINED="-Wl,--unresolved-symbols=ignore-all"
    export ELISA_SHARED_MODULE_FLAGS="-shared -fPIC"
fi

_ep_py() { # $1 = version: Homebrew's on Darwin, else whatever PATH has, else plain python3
    if [[ -n "$ELISA_BREW_BIN" && -x "$ELISA_BREW_BIN/python$1" ]]; then echo "$ELISA_BREW_BIN/python$1"
    else command -v "python$1" 2>/dev/null || command -v "python3${1#*[0-9]}" 2>/dev/null || echo "python$1"; fi
}
export ELISA_PYTHON314_BIN="${ELISA_PYTHON314_BIN:-$(_ep_py 3.14)}"
export ELISA_PYTHON314_CONFIG="${ELISA_PYTHON314_CONFIG:-$(_ep_py 3.14-config)}"
export ELISA_PYTHON312_BIN="${ELISA_PYTHON312_BIN:-$(_ep_py 3.12)}"
export ELISA_PYTHON312_CONFIG="${ELISA_PYTHON312_CONFIG:-$(_ep_py 3.12-config)}"
unset -f _ep_py

export ELISA_NPROC="${ELISA_NPROC:-$( (nproc 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4) )}"
fi

elisa_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}
