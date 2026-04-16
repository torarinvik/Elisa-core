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
import json
import re
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Iterable

from lua_frontend_benchmark_input import build_synthetic_lua_benchmark_input


VALID_MODES = ("parse", "checksum", "metrics", "lexer", "sample", "env", "closure", "label", "analysis")
MIB_PER_SECOND_RE = re.compile(r"MiB/s=([0-9]+(?:\.[0-9]+)?)")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stmt-count", type=int, default=4000, help="Approximate statement budget for the generated mixed-Lua synthetic input")
    parser.add_argument("--parse-iterations", type=int, default=20, help="Iterations per parse-like benchmark run")
    parser.add_argument("--sample-iterations", type=int, default=5000, help="Iterations per sample benchmark run")
    parser.add_argument("--repeats", type=int, default=3, help="Number of repeated runs per benchmark mode")
    parser.add_argument("--modes", default="parse,metrics,checksum,lexer,env,closure,label,analysis", help="Comma-separated benchmark modes to run")
    parser.add_argument("--corpus-manifest", default=None, help="Optional manifest of real-Lua corpus files relative to the repo root")
    parser.add_argument("--skip-real-corpus", action="store_true", help="Only benchmark the synthetic generated input")
    parser.add_argument("--opt-level", default="-O3", help="Compiler/clang optimization flag")
    parser.add_argument("--opt-levels", default=None, help="Optional comma-separated optimization flags to benchmark in one run (for example: -O0,-O2,-O3)")
    parser.add_argument("--keep-temp", action="store_true", help="Keep temporary build artifacts and inline-control files")
    parser.add_argument("--json-out", default=None, help="Write a machine-readable JSON benchmark report to this path")
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


def parse_opt_levels(opt_level: str, opt_levels: str | None) -> list[str]:
    if opt_levels is None:
        return [opt_level]
    levels = [part.strip() for part in opt_levels.split(",") if part.strip()]
    if not levels:
        raise SystemExit("at least one optimization level is required when --opt-levels is provided")
    return levels


def iterations_for_mode(args: argparse.Namespace, mode: str) -> int:
    return args.sample_iterations if mode == "sample" else args.parse_iterations


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
    benchmarks_dir = harness_path.parent
    runtime_shims_path = benchmarks_dir / "json_parser_runtime_shims.c"
    concurrency_runtime_path = benchmarks_dir / "json_parser_concurrency_runtime.c"

    run(["go", "run", "./src", opt_level, "-emit", "header", "-o", str(header_path), str(source_path)], cwd=compiler_dir)
    run(["go", "run", "./src", opt_level, "-emit", "obj", "-o", str(object_path), str(source_path)], cwd=compiler_dir)
    run(
        [
            "clang",
            opt_level,
            "-pthread",
            "-Wl,-undefined,dynamic_lookup",
            "-I",
            str(out_dir),
            str(harness_path),
            str(runtime_shims_path),
            str(concurrency_runtime_path),
            str(object_path),
            "-o",
            str(bench_path),
        ],
        cwd=compiler_dir,
    )
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


def write_json_report(
    out_path: Path,
    *,
    repo_root: Path,
    manifest_path: Path,
    modes: list[str],
    opt_levels: list[str],
    temp_root: Path,
    keep_temp: bool,
    default_opt_level: str,
    stmt_count: int,
    parse_iterations: int,
    sample_iterations: int,
    repeats: int,
    skip_real_corpus: bool,
    current_benches_by_opt: dict[str, Path],
    inline_benches_by_opt: dict[str, Path],
    inline_ast_path: Path | None,
    inline_frontend_path: Path | None,
    synthetic_input: dict[str, object],
    inputs: list[tuple[str, Path]],
    runs: list[dict[str, object]],
    aggregate_summaries: list[dict[str, object]],
    skipped: list[dict[str, str]],
) -> None:
    report = {
        "tool": "run_lua_frontend_storage_benchmark.py",
        "repo_root": str(repo_root),
        "manifest_path": str(manifest_path),
        "modes": modes,
        "opt_levels": opt_levels,
        "temp_root": str(temp_root),
        "keep_temp": keep_temp,
        "opt_level": default_opt_level,
        "stmt_count": stmt_count,
        "parse_iterations": parse_iterations,
        "sample_iterations": sample_iterations,
        "repeats": repeats,
        "skip_real_corpus": skip_real_corpus,
        "current_bench": str(current_benches_by_opt[opt_levels[0]]),
        "inline_bench": None if opt_levels[0] not in inline_benches_by_opt else str(inline_benches_by_opt[opt_levels[0]]),
        "current_benches_by_opt_level": {
            opt_level: str(path) for opt_level, path in sorted(current_benches_by_opt.items())
        },
        "inline_benches_by_opt_level": {
            opt_level: str(path) for opt_level, path in sorted(inline_benches_by_opt.items())
        },
        "inline_ast": None if inline_ast_path is None else str(inline_ast_path),
        "inline_frontend": None if inline_frontend_path is None else str(inline_frontend_path),
        "synthetic_input": synthetic_input,
        "inputs": [{"label": label, "path": str(path)} for label, path in inputs],
        "runs": runs,
        "aggregate_summaries": aggregate_summaries,
        "skipped": skipped,
        "summary": {
            "run_count": len(runs),
            "aggregate_count": len(aggregate_summaries),
            "skipped_count": len(skipped),
            "inline_available": len(inline_benches_by_opt) > 0,
            "opt_level_count": len(opt_levels),
        },
    }
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")


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
    opt_levels = parse_opt_levels(args.opt_level, args.opt_levels)

    temp_root_obj = tempfile.TemporaryDirectory(prefix="lua_frontend_storage_bench.")
    temp_root = Path(temp_root_obj.name)
    input_path = temp_root / "synthetic_bench.lua"
    inline_ast_path: Path | None = None
    inline_frontend_path: Path | None = None
    current_bench_by_opt: dict[str, Path] = {}
    inline_bench_by_opt: dict[str, Path] = {}

    try:
        synthetic_input = build_synthetic_lua_benchmark_input(input_path, args.stmt_count)
        inline_ast_path, inline_frontend_path = make_inline_control(lua_src_dir, args.keep_temp)

        for opt_level in opt_levels:
            opt_tag = opt_level.replace("-", "opt_").replace("+", "plus").replace(".", "_")
            current_out = temp_root / f"current_{opt_tag}"
            inline_out = temp_root / f"inline_{opt_tag}"
            current_out.mkdir(parents=True, exist_ok=True)
            inline_out.mkdir(parents=True, exist_ok=True)
            current_bench_by_opt[opt_level] = build_frontend(
                compiler_dir=compiler_dir,
                source_path=lua_src_dir / "lua_frontend.llcontext",
                out_dir=current_out,
                opt_level=opt_level,
                harness_path=harness_path,
            )
            if inline_frontend_path is not None:
                inline_bench_by_opt[opt_level] = build_frontend(
                    compiler_dir=compiler_dir,
                    source_path=inline_frontend_path,
                    out_dir=inline_out,
                    opt_level=opt_level,
                    harness_path=harness_path,
                )

        inputs: list[tuple[str, Path]] = [("synthetic", input_path)]
        if not args.skip_real_corpus:
            inputs.extend(load_corpus_manifest(repo_root, manifest_path))

        if args.keep_temp:
            print(f"temp_root={temp_root}")
        else:
            print(f"temp_root={temp_root} (temporary)")
        print(
            "SYNTHETIC "
            f"kind={synthetic_input['kind']} "
            f"chunks={synthetic_input['chunk_count']} "
            f"approx_stmt_count={synthetic_input['approx_stmt_count']} "
            f"bytes={synthetic_input['bytes']}"
        )

        current_mode_averages: dict[tuple[str, str], list[float]] = {}
        inline_mode_averages: dict[tuple[str, str], list[float]] = {}
        skipped_labels: list[str] = []
        skipped_entries: list[dict[str, str]] = []
        run_entries: list[dict[str, object]] = []
        aggregate_entries: list[dict[str, object]] = []

        for opt_level in opt_levels:
            current_bench = current_bench_by_opt[opt_level]
            inline_bench = inline_bench_by_opt.get(opt_level)
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
                            f"CURRENT_{opt_level}_{input_label}_{mode}".upper(),
                        )
                    except RuntimeError as exc:
                        if input_label == "synthetic":
                            raise
                        print(f"SKIP label=current_{opt_level}_{input_label}_{mode} reason={exc}")
                        skipped_labels.append(f"current_{opt_level}_{input_label}_{mode}")
                        skipped_entries.append({"variant": "current", "opt_level": opt_level, "input": input_label, "mode": mode, "label": f"current_{opt_level}_{input_label}_{mode}", "reason": str(exc)})
                        continue
                    current_avg = summarize(f"current_{opt_level}_{input_label}_{mode}", current_values)
                    current_mode_averages.setdefault((opt_level, mode), []).append(current_avg)
                    run_entries.append(
                        {
                            "variant": "current",
                            "opt_level": opt_level,
                            "input": input_label,
                            "input_path": str(bench_input),
                            "mode": mode,
                            "iterations": iterations,
                            "repeats": args.repeats,
                            "values_MiB_s": current_values,
                            "avg_MiB_s": current_avg,
                            "min_MiB_s": min(current_values),
                            "max_MiB_s": max(current_values),
                        }
                    )
                    if inline_bench is not None:
                        try:
                            inline_values = run_suite(
                                inline_bench,
                                bench_input,
                                iterations,
                                args.repeats,
                                mode,
                                f"INLINE_{opt_level}_{input_label}_{mode}".upper(),
                            )
                        except RuntimeError as exc:
                            print(f"SKIP label=inline_{opt_level}_{input_label}_{mode} reason={exc}")
                            skipped_labels.append(f"inline_{opt_level}_{input_label}_{mode}")
                            skipped_entries.append({"variant": "inline", "opt_level": opt_level, "input": input_label, "mode": mode, "label": f"inline_{opt_level}_{input_label}_{mode}", "reason": str(exc)})
                            continue
                        inline_avg = summarize(f"inline_{opt_level}_{input_label}_{mode}", inline_values)
                        inline_mode_averages.setdefault((opt_level, mode), []).append(inline_avg)
                        run_entries.append(
                            {
                                "variant": "inline",
                                "opt_level": opt_level,
                                "input": input_label,
                                "input_path": str(bench_input),
                                "mode": mode,
                                "iterations": iterations,
                                "repeats": args.repeats,
                                "values_MiB_s": inline_values,
                                "avg_MiB_s": inline_avg,
                                "min_MiB_s": min(inline_values),
                                "max_MiB_s": max(inline_values),
                            }
                        )
                        per_input_delta = delta_percent(current_avg, inline_avg)
                        print(f"SUMMARY label={opt_level}_{input_label}_{mode}_delta current_vs_inline_pct={per_input_delta:+.2f}")
                        aggregate_entries.append(
                            {
                                "kind": "per_input_delta",
                                "opt_level": opt_level,
                                "input": input_label,
                                "mode": mode,
                                "current_avg_MiB_s": current_avg,
                                "inline_avg_MiB_s": inline_avg,
                                "current_vs_inline_pct": per_input_delta,
                            }
                        )

        for opt_level in opt_levels:
            for mode in modes:
                aggregate_key = (opt_level, mode)
                if current_mode_averages.get(aggregate_key):
                    current_avg = summarize(f"aggregate_current_{opt_level}_{mode}", current_mode_averages[aggregate_key])
                    aggregate_entries.append(
                        {
                            "kind": "aggregate_current",
                            "opt_level": opt_level,
                            "mode": mode,
                            "avg_MiB_s": current_avg,
                            "min_MiB_s": min(current_mode_averages[aggregate_key]),
                            "max_MiB_s": max(current_mode_averages[aggregate_key]),
                            "input_count": len(current_mode_averages[aggregate_key]),
                        }
                    )
                else:
                    print(f"SUMMARY label=aggregate_current_{opt_level}_{mode} skipped=1 no_successful_inputs=1")
                    aggregate_entries.append(
                        {
                            "kind": "aggregate_current",
                            "opt_level": opt_level,
                            "mode": mode,
                            "skipped": True,
                            "no_successful_inputs": True,
                        }
                    )
                    continue
                if inline_mode_averages.get(aggregate_key):
                    inline_avg = summarize(f"aggregate_inline_{opt_level}_{mode}", inline_mode_averages[aggregate_key])
                    aggregate_entries.append(
                        {
                            "kind": "aggregate_inline",
                            "opt_level": opt_level,
                            "mode": mode,
                            "avg_MiB_s": inline_avg,
                            "min_MiB_s": min(inline_mode_averages[aggregate_key]),
                            "max_MiB_s": max(inline_mode_averages[aggregate_key]),
                            "input_count": len(inline_mode_averages[aggregate_key]),
                        }
                    )
                    aggregate_delta = delta_percent(current_avg, inline_avg)
                    print(f"SUMMARY label=aggregate_{opt_level}_{mode}_delta current_vs_inline_pct={aggregate_delta:+.2f}")
                    aggregate_entries.append(
                        {
                            "kind": "aggregate_delta",
                            "opt_level": opt_level,
                            "mode": mode,
                            "current_avg_MiB_s": current_avg,
                            "inline_avg_MiB_s": inline_avg,
                            "current_vs_inline_pct": aggregate_delta,
                        }
                    )
        print(f"SUMMARY skipped_labels={len(skipped_labels)}")

        if args.json_out:
            write_json_report(
                Path(args.json_out),
                repo_root=repo_root,
                manifest_path=manifest_path,
                modes=modes,
                opt_levels=opt_levels,
                temp_root=temp_root,
                keep_temp=args.keep_temp,
                default_opt_level=args.opt_level,
                stmt_count=args.stmt_count,
                parse_iterations=args.parse_iterations,
                sample_iterations=args.sample_iterations,
                repeats=args.repeats,
                skip_real_corpus=args.skip_real_corpus,
                current_benches_by_opt=current_bench_by_opt,
                inline_benches_by_opt=inline_bench_by_opt,
                inline_ast_path=inline_ast_path,
                inline_frontend_path=inline_frontend_path,
                synthetic_input=synthetic_input,
                inputs=inputs,
                runs=run_entries,
                aggregate_summaries=aggregate_entries,
                skipped=skipped_entries,
            )
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
