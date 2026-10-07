"""Python twin of scripts/platform.sh: the host link flags and LLVM paths.

Values come from the environment scripts/platform.sh exports when a shell driver sourced it,
else they are derived the same way here, so a Python harness run on its own still links on
macOS (ld64) and Linux (GNU ld) without tools/linux_shim.
"""
from __future__ import annotations

import os
import shutil
import subprocess
import sys

DARWIN = sys.platform == "darwin"


def _llvm_config() -> str:
    env = os.environ.get("LLVM_CONFIG")
    if env:
        return env
    found = shutil.which("llvm-config")
    if found:
        return found
    return "/opt/homebrew/opt/llvm/bin/llvm-config"


LLVM_CONFIG = _llvm_config()


def _query(flag: str, fallback: str) -> str:
    try:
        return subprocess.run([LLVM_CONFIG, flag], capture_output=True, text=True, check=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return fallback


def _env(name: str, default) -> str:
    value = os.environ.get(name)
    return value if value is not None else (default() if callable(default) else default)


LD_DEAD_STRIP = _env("ELISA_LD_DEAD_STRIP", "-Wl,-dead_strip" if DARWIN else "-Wl,--gc-sections").split()
LINK_EXE_FLAGS = _env("ELISA_LINK_EXE_FLAGS", "" if DARWIN else "-no-pie -Wl,--unresolved-symbols=ignore-all").split()
LLVM_LIBDIR = _env("ELISA_LLVM_LIBDIR", lambda: _query("--libdir", "/opt/homebrew/opt/llvm/lib"))
LLVM_BIN_DIR = _env("ELISA_LLVM_BIN_DIR", lambda: _query("--bindir", "/opt/homebrew/opt/llvm/bin"))
# platform.sh's static-archive fallback is not repeated here: a harness that needs it runs
# under a shell driver, which exports ELISA_LLVM_LIBS.
LLVM_LIBS = _env("ELISA_LLVM_LIBS", "-lLLVM").split()
# Every executable link: dead-strip/gc-sections plus the host's exe flags.
EXE_LINK = LD_DEAD_STRIP + LINK_EXE_FLAGS
# Elisa-core: leave the benchmark harnesses' optional runtime symbols unresolved at link time.
LD_ALLOW_UNDEFINED = _env("ELISA_LD_ALLOW_UNDEFINED", "-Wl,-undefined,dynamic_lookup" if DARWIN
                          else "-Wl,--unresolved-symbols=ignore-all").split()
