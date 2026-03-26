#!/usr/bin/env python3
"""Benchmark Lua frontend side-table vs inline common-field storage.

This script compares the checked-in Lua frontend against a temporary inline-control
variant where `LuaNode.common.span` is stored inline instead of in a side table.
It generates a valid benchmark input, builds both variants, runs parse/sample
benchmarks repeatedly, prints raw benchmark lines, and reports average throughput.
"""

from __future__ import annotations

import argparse
import os
import re
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Iterable


MIB_PER_SECOND_RE = re.compile(r"MiB/s=([0-9]+(?:\.[0-9]+)?)")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stmt-count", type=int, default=4000, help="Number of local assignment statements to generate")
    parser.add_argument("--parse-iterations", type=int, default=20, help="Iterations per parse benchmark run")
    parser.add_argument("--sample-iterations", type=int, default=5000, help="Iterations per sample benchmark run")
    parser.add_argument("--repeats", type=int, default=3, help="Number of repeated runs per benchmark mode")
    parser.add_argument("--opt-level", default="-O3", help="Compiler/clang optimization flag")
    parser.add_argument("--keep-temp", action="store_true", help="Keep temporary build artifacts and inline-control files")
    return parser.parse_args()


def run(cmd: list[str], cwd: Path | None = None, capture_output: bool = False) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        cmd,
        cwd=cwd,
        check=True,
        text=True,
        capture_output=capture_output,
    )


def build_valid_input(path: Path, stmt_count: int) -> None:
    lines = [f"local x{i} = {i} + {i + 1}" for i in range(1, stmt_count + 1)]
    lines.append(f"return x{stmt_count}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def make_inline_control(lua_src_dir: Path, keep_temp: bool) -> tuple[Path, Path]:
    ast_path = lua_src_dir / "lua_ast.llcontext"
    frontend_path = lua_src_dir / "lua_frontend.llcontext"

    ast_text = ast_path.read_text(encoding="utf-8")
    marker = "    common:\n        @storage(side_table)\n        span: LuaSpan"
    if marker not in ast_text:
        raise RuntimeError("expected side-table storage marker for LuaNode.common.span")
    inline_ast_text = ast_text.replace(marker, "    common:\n        span: LuaSpan", 1)

    with tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        dir=lua_src_dir,
        prefix=".lua_ast_inline_bench_",
        suffix=".llcontext",
        delete=False,
    ) as ast_tmp:
        ast_tmp.write(inline_ast_text)
        inline_ast_path = Path(ast_tmp.name)

    frontend_text = frontend_path.read_text(encoding="utf-8")
    inline_frontend_text = frontend_text.replace('"lua_ast.llcontext"', f'"{inline_ast_path.name}"', 1)
    if inline_frontend_text == frontend_text:
        raise RuntimeError("expected lua_frontend.llcontext to include lua_ast.llcontext")

    with tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        dir=lua_src_dir,
        prefix=".lua_frontend_inline_bench_",
        suffix=".llcontext",
        delete=False,
    ) as frontend_tmp:
        frontend_tmp.write(inline_frontend_text)
        inline_frontend_path = Path(frontend_tmp.name)

    if keep_temp:
        print(f"inline_ast={inline_ast_path}")
        print(f"inline_frontend={inline_frontend_path}")

    return inline_ast_path, inline_frontend_path


def build_frontend(
    compiler_dir: Path,
    source_path: Path,
    out_dir: Path,
    opt_level: str,
    harness_path: Path,
) -> Path:
    header_path = out_dir / "lua_frontend.h"
    object_path = out_dir / "lua_frontend.o"
    bench_path = out_dir / "lua_frontend_bench"

    run(["go", "run", "./src", opt_level, "-emit", "header", "-o", str(header_path), str(source_path)], cwd=compiler_dir)
    run(["go", "run", "./src", opt_level, "-emit", "obj", "-o", str(object_path), str(source_path)], cwd=compiler_dir)
    run(["clang", opt_level, "-I", str(out_dir), str(harness_path), str(object_path), "-o", str(bench_path)], cwd=compiler_dir)
    return bench_path


def run_suite(exe: Path, input_path: Path, iterations: int, repeats: int, mode: str, label: str) -> list[float]:
    print(label)
    mib_values: list[float] = []
    for _ in range(repeats):
        completed = run([str(exe), str(input_path), str(iterations), mode], capture_output=True)
        output = completed.stdout.strip()
        if output:
            print(output)
        if completed.stderr:
            print(completed.stderr.strip(), file=sys.stderr)
        match = MIB_PER_SECOND_RE.search(output)
        if not match:
            raise RuntimeError(f"missing MiB/s metric in benchmark output: {output!r}")
        mib_values.append(float(match.group(1)))
    return mib_values


def summarize(label: str, values: Iterable[float]) -> float:
    values = list(values)
    avg = statistics.mean(values)
    print(f"SUMMARY label={label} avg_MiB_s={avg:.2f} min_MiB_s={min(values):.2f} max_MiB_s={max(values):.2f}")
    return avg


def delta_percent(current: float, inline: float) -> float:
    if inline == 0.0:
        return 0.0
    return ((current - inline) / inline) * 100.0


def main() -> int:
    args = parse_args()
    if args.stmt_count <= 0 or args.parse_iterations <= 0 or args.sample_iterations <= 0 or args.repeats <= 0:
        raise SystemExit("all iteration/count arguments must be positive")

    repo_root = Path(__file__).resolve().parents[2]
    compiler_dir = repo_root / "compiler"
    lua_src_dir = repo_root / "Code" / "llcontext_lua" / "src"
    harness_path = repo_root / "Code" / "benchmarks" / "lua_frontend_bench.c"

    temp_root_obj = tempfile.TemporaryDirectory(prefix="lua_frontend_storage_bench.")
    temp_root = Path(temp_root_obj.name)
    input_path = temp_root / "bench.lua"
    current_out = temp_root / "current"
    inline_out = temp_root / "inline"
    current_out.mkdir(parents=True, exist_ok=True)
    inline_out.mkdir(parents=True, exist_ok=True)

    inline_ast_path: Path | None = None
    inline_frontend_path: Path | None = None

    try:
        build_valid_input(input_path, args.stmt_count)
        inline_ast_path, inline_frontend_path = make_inline_control(lua_src_dir, args.keep_temp)

        current_bench = build_frontend(
            compiler_dir=compiler_dir,
            source_path=lua_src_dir / "lua_frontend.llcontext",
            out_dir=current_out,
            opt_level=args.opt_level,
            harness_path=harness_path,
        )
        inline_bench = build_frontend(
            compiler_dir=compiler_dir,
            source_path=inline_frontend_path,
            out_dir=inline_out,
            opt_level=args.opt_level,
            harness_path=harness_path,
        )

        if args.keep_temp:
            print(f"temp_root={temp_root}")
        else:
            print(f"temp_root={temp_root} (temporary)")

        current_parse = run_suite(current_bench, input_path, args.parse_iterations, args.repeats, "parse", "CURRENT_PARSE")
        inline_parse = run_suite(inline_bench, input_path, args.parse_iterations, args.repeats, "parse", "INLINE_PARSE")
        current_sample = run_suite(current_bench, input_path, args.sample_iterations, args.repeats, "sample", "CURRENT_SAMPLE")
        inline_sample = run_suite(inline_bench, input_path, args.sample_iterations, args.repeats, "sample", "INLINE_SAMPLE")

        current_parse_avg = summarize("current_parse", current_parse)
        inline_parse_avg = summarize("inline_parse", inline_parse)
        current_sample_avg = summarize("current_sample", current_sample)
        inline_sample_avg = summarize("inline_sample", inline_sample)
        print(f"SUMMARY label=parse_delta current_vs_inline_pct={delta_percent(current_parse_avg, inline_parse_avg):+.2f}")
        print(f"SUMMARY label=sample_delta current_vs_inline_pct={delta_percent(current_sample_avg, inline_sample_avg):+.2f}")
    finally:
        if not args.keep_temp:
            for path in (inline_frontend_path, inline_ast_path):
                if path is not None:
                    try:
                        path.unlink()
                    except FileNotFoundError:
                        pass
            temp_root_obj.cleanup()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
