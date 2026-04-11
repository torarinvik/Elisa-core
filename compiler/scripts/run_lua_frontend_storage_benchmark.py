#!/usr/bin/env python3
"""Benchmark Lua frontend analysis modes across synthetic and curated real-Lua inputs.

The script still supports an optional inline-control variant when the Lua AST uses
the older side-table span layout, but it also degrades cleanly when that variant
is no longer available. Real-corpus inputs are treated as developer tooling: when
an input is not supported by the current frontend for a given mode, the script
records a skip instead of failing the whole sweep.
"""

from __future__ import annotations

import argparse
import re
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Iterable


VALID_MODES = ("parse", "sample", "env", "closure", "label", "analysis")
MIB_PER_SECOND_RE = re.compile(r"MiB/s=([0-9]+(?:\.[0-9]+)?)")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stmt-count", type=int, default=4000, help="Number of local assignment statements to generate for the synthetic input")
    parser.add_argument("--parse-iterations", type=int, default=20, help="Iterations per parse-like benchmark run")
    parser.add_argument("--sample-iterations", type=int, default=5000, help="Iterations per sample benchmark run")
    parser.add_argument("--repeats", type=int, default=3, help="Number of repeated runs per benchmark mode")
    parser.add_argument("--modes", default="parse,sample,env,closure,label,analysis", help="Comma-separated benchmark modes to run")
    parser.add_argument("--corpus-manifest", default=None, help="Optional manifest of real-Lua corpus files relative to the repo root")
    parser.add_argument("--skip-real-corpus", action="store_true", help="Only benchmark the synthetic generated input")
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


def parse_modes(text: str) -> list[str]:
    modes = [part.strip() for part in text.split(",") if part.strip()]
    if not modes:
        raise SystemExit("at least one benchmark mode is required")
    invalid = [mode for mode in modes if mode not in VALID_MODES]
    if invalid:
        raise SystemExit(f"unsupported benchmark mode(s): {', '.join(invalid)}")
    return modes


def iterations_for_mode(args: argparse.Namespace, mode: str) -> int:
    return args.sample_iterations if mode == "sample" else args.parse_iterations


def build_valid_input(path: Path, stmt_count: int) -> None:
    lines = [f"local x{i} = {i} + {i + 1}" for i in range(1, stmt_count + 1)]
    lines.append(f"return x{stmt_count}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def load_corpus_manifest(repo_root: Path, manifest_path: Path | None) -> list[tuple[str, Path]]:
    if manifest_path is None or not manifest_path.exists():
        return []
    inputs: list[tuple[str, Path]] = []
    for raw_line in manifest_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        source_path = (repo_root / line).resolve()
        if not source_path.exists():
            raise RuntimeError(f"missing corpus file listed in {manifest_path}: {line}")
        label = source_path.stem.replace(".", "_")
        inputs.append((label, source_path))
    return inputs


def make_inline_control(lua_src_dir: Path, keep_temp: bool) -> tuple[Path | None, Path | None]:
    ast_path = lua_src_dir / "lua_ast.llcontext"
    frontend_path = lua_src_dir / "lua_frontend.llcontext"

    ast_text = ast_path.read_text(encoding="utf-8")
    marker = "    common:\n        @storage(side_table)\n        span: LuaSpan"
    if marker not in ast_text:
        if keep_temp:
            print("inline_control=disabled (lua_ast.llcontext no longer uses the side-table span marker)")
        return None, None
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


def format_run_failure(exc: subprocess.CalledProcessError) -> str:
    parts: list[str] = []
    if exc.stdout:
        parts.append(exc.stdout.strip())
    if exc.stderr:
        parts.append(exc.stderr.strip())
    message = " | ".join(part for part in parts if part)
    if message:
        return " ".join(message.split())
    return f"benchmark exited with status {exc.returncode}"


def run_suite(exe: Path, input_path: Path, iterations: int, repeats: int, mode: str, label: str) -> list[float]:
    print(label)
    mib_values: list[float] = []
    for _ in range(repeats):
        try:
            completed = run([str(exe), str(input_path), str(iterations), mode], capture_output=True)
        except subprocess.CalledProcessError as exc:
            raise RuntimeError(format_run_failure(exc)) from exc
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
    manifest_path = Path(args.corpus_manifest) if args.corpus_manifest else repo_root / "Code" / "benchmarks" / "lua_frontend_corpus_manifest.txt"
    modes = parse_modes(args.modes)

    temp_root_obj = tempfile.TemporaryDirectory(prefix="lua_frontend_storage_bench.")
    temp_root = Path(temp_root_obj.name)
    input_path = temp_root / "synthetic_bench.lua"
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
        inline_bench: Path | None = None
        if inline_frontend_path is not None:
            inline_bench = build_frontend(
                compiler_dir=compiler_dir,
                source_path=inline_frontend_path,
                out_dir=inline_out,
                opt_level=args.opt_level,
                harness_path=harness_path,
            )

        inputs: list[tuple[str, Path]] = [("synthetic", input_path)]
        if not args.skip_real_corpus:
            inputs.extend(load_corpus_manifest(repo_root, manifest_path))

        if args.keep_temp:
            print(f"temp_root={temp_root}")
        else:
            print(f"temp_root={temp_root} (temporary)")

        current_mode_averages: dict[str, list[float]] = {}
        inline_mode_averages: dict[str, list[float]] = {}
        skipped_labels: list[str] = []

        for input_label, bench_input in inputs:
            for mode in modes:
                iterations = iterations_for_mode(args, mode)
                try:
                    current_values = run_suite(
                        current_bench,
                        bench_input,
                        iterations,
                        args.repeats,
                        mode,
                        f"CURRENT_{input_label}_{mode}".upper(),
                    )
                except RuntimeError as exc:
                    if input_label == "synthetic":
                        raise
                    print(f"SKIP label=current_{input_label}_{mode} reason={exc}")
                    skipped_labels.append(f"current_{input_label}_{mode}")
                    continue
                current_avg = summarize(f"current_{input_label}_{mode}", current_values)
                current_mode_averages.setdefault(mode, []).append(current_avg)
                if inline_bench is not None:
                    try:
                        inline_values = run_suite(
                            inline_bench,
                            bench_input,
                            iterations,
                            args.repeats,
                            mode,
                            f"INLINE_{input_label}_{mode}".upper(),
                        )
                    except RuntimeError as exc:
                        print(f"SKIP label=inline_{input_label}_{mode} reason={exc}")
                        skipped_labels.append(f"inline_{input_label}_{mode}")
                        continue
                    inline_avg = summarize(f"inline_{input_label}_{mode}", inline_values)
                    inline_mode_averages.setdefault(mode, []).append(inline_avg)
                    print(f"SUMMARY label={input_label}_{mode}_delta current_vs_inline_pct={delta_percent(current_avg, inline_avg):+.2f}")

        for mode in modes:
            if current_mode_averages.get(mode):
                current_avg = summarize(f"aggregate_current_{mode}", current_mode_averages[mode])
            else:
                print(f"SUMMARY label=aggregate_current_{mode} skipped=1 no_successful_inputs=1")
                continue
            if inline_mode_averages.get(mode):
                inline_avg = summarize(f"aggregate_inline_{mode}", inline_mode_averages[mode])
                print(f"SUMMARY label=aggregate_{mode}_delta current_vs_inline_pct={delta_percent(current_avg, inline_avg):+.2f}")
        print(f"SUMMARY skipped_labels={len(skipped_labels)}")
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
