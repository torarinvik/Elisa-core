#!/usr/bin/env python3
"""Benchmark Lua frontend analysis modes across synthetic and curated real-Lua inputs.

The script still supports an optional inline-control variant when the Lua AST uses
the older side-table span layout, but it also degrades cleanly when that variant
is no longer available. Real-corpus inputs are treated as developer tooling: when
an input is not supported by the current frontend for a given mode, the script
records a skip instead of failing the whole sweep.
"""

from __future__ import annotations

import elisa_platform
import argparse
import json
import re
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path

from lua_frontend_benchmark_support import (
    run,
    load_corpus_manifest,
    make_inline_control,
    build_benchmark_executable,
    format_run_failure,
    run_suite,
    summarize,
    delta_percent,
    run_label,
    write_json_report,
)
from typing import Iterable

from lua_frontend_benchmark_input import build_synthetic_lua_benchmark_input


VALID_MODES = ("parse", "checksum", "metrics", "lexer", "sample", "env", "closure", "label", "analysis", "checked")
PARALLEL_CAPABLE_MODES = ("parse", "metrics", "lexer", "analysis")
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
    parser.add_argument("--parallel-workers", default=None, help="Optional comma-separated worker counts for exported parallel parse/metrics/lexer/analysis benchmarks")
    parser.add_argument("--keep-temp", action="store_true", help="Keep temporary build artifacts and inline-control files")
    parser.add_argument("--json-out", default=None, help="Write a machine-readable JSON benchmark report to this path")
    return parser.parse_args()


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


def parse_parallel_workers(text: str | None) -> list[int]:
    if text is None:
        return []
    parts = [part.strip() for part in text.split(",") if part.strip()]
    if not parts:
        raise SystemExit("at least one parallel worker count is required when --parallel-workers is provided")

    worker_counts: list[int] = []
    seen: set[int] = set()
    for part in parts:
        try:
            worker_count = int(part)
        except ValueError as exc:
            raise SystemExit(f"invalid parallel worker count: {part!r}") from exc
        if worker_count <= 0:
            raise SystemExit("parallel worker counts must be positive")
        if worker_count not in seen:
            seen.add(worker_count)
            worker_counts.append(worker_count)
    return worker_counts


def iterations_for_mode(args: argparse.Namespace, mode: str) -> int:
    return args.sample_iterations if mode == "sample" else args.parse_iterations


def main() -> int:
    args = parse_args()
    if args.stmt_count <= 0 or args.parse_iterations <= 0 or args.sample_iterations <= 0 or args.repeats <= 0:
        raise SystemExit("all iteration/count arguments must be positive")

    repo_root = Path(__file__).resolve().parents[2]
    compiler_dir = repo_root / "compiler"
    lua_src_dir = repo_root / "Code" / "elisacore_lua" / "src"
    manifest_path = Path(args.corpus_manifest) if args.corpus_manifest else repo_root / "Code" / "benchmarks" / "lua_frontend_corpus_manifest.txt"
    modes = parse_modes(args.modes)
    opt_levels = parse_opt_levels(args.opt_level, args.opt_levels)
    parallel_workers = parse_parallel_workers(args.parallel_workers)
    parallel_modes = [mode for mode in modes if mode in PARALLEL_CAPABLE_MODES]

    temp_root_obj = tempfile.TemporaryDirectory(prefix="lua_frontend_storage_bench.")
    temp_root = Path(temp_root_obj.name)
    input_path = temp_root / "synthetic_bench.lua"
    inline_ast_path: Path | None = None
    inline_frontend_path: Path | None = None
    harness_path = repo_root / "Code" / "benchmarks" / "lua_frontend_bench.c"
    parallel_harness_path = repo_root / "Code" / "benchmarks" / "lua_frontend_parallel_bench.c"
    current_bench_by_opt: dict[str, Path] = {}
    inline_bench_by_opt: dict[str, Path] = {}
    current_parallel_bench_by_opt: dict[str, Path] = {}
    inline_parallel_bench_by_opt: dict[str, Path] = {}

    try:
        synthetic_input = build_synthetic_lua_benchmark_input(input_path, args.stmt_count)
        inline_ast_path, inline_frontend_path = make_inline_control(lua_src_dir, args.keep_temp)

        for opt_level in opt_levels:
            opt_tag = opt_level.replace("-", "opt_").replace("+", "plus").replace(".", "_")
            current_out = temp_root / f"current_{opt_tag}"
            inline_out = temp_root / f"inline_{opt_tag}"
            current_parallel_out = temp_root / f"current_parallel_{opt_tag}"
            inline_parallel_out = temp_root / f"inline_parallel_{opt_tag}"
            current_out.mkdir(parents=True, exist_ok=True)
            inline_out.mkdir(parents=True, exist_ok=True)
            current_parallel_out.mkdir(parents=True, exist_ok=True)
            inline_parallel_out.mkdir(parents=True, exist_ok=True)
            current_bench_by_opt[opt_level] = build_benchmark_executable(
                compiler_dir=compiler_dir,
                source_path=lua_src_dir / "lua_frontend.elisa",
                out_dir=current_out,
                opt_level=opt_level,
                harness_path=harness_path,
            )
            if inline_frontend_path is not None:
                inline_bench_by_opt[opt_level] = build_benchmark_executable(
                    compiler_dir=compiler_dir,
                    source_path=inline_frontend_path,
                    out_dir=inline_out,
                    opt_level=opt_level,
                    harness_path=harness_path,
                )
            if parallel_workers and parallel_modes:
                current_parallel_bench_by_opt[opt_level] = build_benchmark_executable(
                    compiler_dir=compiler_dir,
                    source_path=lua_src_dir / "lua_frontend.elisa",
                    out_dir=current_parallel_out,
                    opt_level=opt_level,
                    harness_path=parallel_harness_path,
                )
                if inline_frontend_path is not None:
                    inline_parallel_bench_by_opt[opt_level] = build_benchmark_executable(
                        compiler_dir=compiler_dir,
                        source_path=inline_frontend_path,
                        out_dir=inline_parallel_out,
                        opt_level=opt_level,
                        harness_path=parallel_harness_path,
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
        if parallel_workers and parallel_modes:
            worker_text = ",".join(str(worker_count) for worker_count in parallel_workers)
            print(f"PARALLEL workers={worker_text} modes={','.join(parallel_modes)}")
        elif parallel_workers:
            worker_text = ",".join(str(worker_count) for worker_count in parallel_workers)
            print(f"PARALLEL workers={worker_text} skipped=1 reason=no_parallel_capable_modes_requested")

        mode_averages: dict[tuple[str, str, int, str, str], list[float]] = {}
        skipped_labels: list[str] = []
        skipped_entries: list[dict[str, object]] = []
        run_entries: list[dict[str, object]] = []
        aggregate_entries: list[dict[str, object]] = []

        def record_run(
            *,
            variant: str,
            execution: str,
            worker_count: int,
            opt_level: str,
            input_label: str,
            bench_input: Path,
            mode: str,
            iterations: int,
            values: list[float],
        ) -> float:
            label = run_label(variant, opt_level, input_label, mode, execution, worker_count).lower()
            avg = summarize(label, values)
            mode_averages.setdefault((variant, execution, worker_count, opt_level, mode), []).append(avg)
            run_entries.append(
                {
                    "variant": variant,
                    "execution": execution,
                    "worker_count": worker_count,
                    "opt_level": opt_level,
                    "input": input_label,
                    "input_path": str(bench_input),
                    "mode": mode,
                    "iterations": iterations,
                    "repeats": args.repeats,
                    "values_MiB_s": values,
                    "avg_MiB_s": avg,
                    "min_MiB_s": min(values),
                    "max_MiB_s": max(values),
                }
            )
            return avg

        for opt_level in opt_levels:
            current_bench = current_bench_by_opt[opt_level]
            inline_bench = inline_bench_by_opt.get(opt_level)
            current_parallel_bench = current_parallel_bench_by_opt.get(opt_level)
            inline_parallel_bench = inline_parallel_bench_by_opt.get(opt_level)
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
                            run_label("current", opt_level, input_label, mode, "serial", 0),
                        )
                    except RuntimeError as exc:
                        if input_label == "synthetic":
                            raise
                        print(f"SKIP label=current_{opt_level}_{input_label}_{mode} reason={exc}")
                        skipped_labels.append(f"current_{opt_level}_{input_label}_{mode}")
                        skipped_entries.append({"variant": "current", "execution": "serial", "worker_count": 0, "opt_level": opt_level, "input": input_label, "mode": mode, "label": f"current_{opt_level}_{input_label}_{mode}", "reason": str(exc)})
                        continue
                    current_avg = record_run(
                        variant="current",
                        execution="serial",
                        worker_count=0,
                        opt_level=opt_level,
                        input_label=input_label,
                        bench_input=bench_input,
                        mode=mode,
                        iterations=iterations,
                        values=current_values,
                    )
                    current_parallel_avgs: dict[int, float] = {}
                    if current_parallel_bench is not None and mode in parallel_modes:
                        for worker_count in parallel_workers:
                            try:
                                parallel_values = run_suite(
                                    current_parallel_bench,
                                    bench_input,
                                    iterations,
                                    args.repeats,
                                    mode,
                                    run_label("current", opt_level, input_label, mode, "parallel", worker_count),
                                    worker_count=worker_count,
                                )
                            except RuntimeError as exc:
                                if input_label == "synthetic":
                                    raise
                                skip_label = f"current_parallel_w{worker_count}_{opt_level}_{input_label}_{mode}"
                                print(f"SKIP label={skip_label} reason={exc}")
                                skipped_labels.append(skip_label)
                                skipped_entries.append(
                                    {
                                        "variant": "current",
                                        "execution": "parallel",
                                        "worker_count": worker_count,
                                        "opt_level": opt_level,
                                        "input": input_label,
                                        "mode": mode,
                                        "label": skip_label,
                                        "reason": str(exc),
                                    }
                                )
                                continue
                            current_parallel_avgs[worker_count] = record_run(
                                variant="current",
                                execution="parallel",
                                worker_count=worker_count,
                                opt_level=opt_level,
                                input_label=input_label,
                                bench_input=bench_input,
                                mode=mode,
                                iterations=iterations,
                                values=parallel_values,
                            )
                    if inline_bench is not None:
                        try:
                            inline_values = run_suite(
                                inline_bench,
                                bench_input,
                                iterations,
                                args.repeats,
                                mode,
                                run_label("inline", opt_level, input_label, mode, "serial", 0),
                            )
                        except RuntimeError as exc:
                            print(f"SKIP label=inline_{opt_level}_{input_label}_{mode} reason={exc}")
                            skipped_labels.append(f"inline_{opt_level}_{input_label}_{mode}")
                            skipped_entries.append({"variant": "inline", "execution": "serial", "worker_count": 0, "opt_level": opt_level, "input": input_label, "mode": mode, "label": f"inline_{opt_level}_{input_label}_{mode}", "reason": str(exc)})
                            continue
                        inline_avg = record_run(
                            variant="inline",
                            execution="serial",
                            worker_count=0,
                            opt_level=opt_level,
                            input_label=input_label,
                            bench_input=bench_input,
                            mode=mode,
                            iterations=iterations,
                            values=inline_values,
                        )
                        per_input_delta = delta_percent(current_avg, inline_avg)
                        print(f"SUMMARY label={opt_level}_{input_label}_{mode}_delta current_vs_inline_pct={per_input_delta:+.2f}")
                        aggregate_entries.append(
                            {
                                "kind": "per_input_delta",
                                "execution": "serial",
                                "worker_count": 0,
                                "opt_level": opt_level,
                                "input": input_label,
                                "mode": mode,
                                "current_avg_MiB_s": current_avg,
                                "inline_avg_MiB_s": inline_avg,
                                "current_vs_inline_pct": per_input_delta,
                            }
                        )
                        if inline_parallel_bench is not None and mode in parallel_modes:
                            for worker_count in parallel_workers:
                                try:
                                    inline_parallel_values = run_suite(
                                        inline_parallel_bench,
                                        bench_input,
                                        iterations,
                                        args.repeats,
                                        mode,
                                        run_label("inline", opt_level, input_label, mode, "parallel", worker_count),
                                        worker_count=worker_count,
                                    )
                                except RuntimeError as exc:
                                    skip_label = f"inline_parallel_w{worker_count}_{opt_level}_{input_label}_{mode}"
                                    print(f"SKIP label={skip_label} reason={exc}")
                                    skipped_labels.append(skip_label)
                                    skipped_entries.append(
                                        {
                                            "variant": "inline",
                                            "execution": "parallel",
                                            "worker_count": worker_count,
                                            "opt_level": opt_level,
                                            "input": input_label,
                                            "mode": mode,
                                            "label": skip_label,
                                            "reason": str(exc),
                                        }
                                    )
                                    continue
                                inline_parallel_avg = record_run(
                                    variant="inline",
                                    execution="parallel",
                                    worker_count=worker_count,
                                    opt_level=opt_level,
                                    input_label=input_label,
                                    bench_input=bench_input,
                                    mode=mode,
                                    iterations=iterations,
                                    values=inline_parallel_values,
                                )
                                current_parallel_avg = current_parallel_avgs.get(worker_count)
                                if current_parallel_avg is None:
                                    continue
                                per_input_parallel_delta = delta_percent(current_parallel_avg, inline_parallel_avg)
                                print(
                                    f"SUMMARY label={opt_level}_{input_label}_{mode}_parallel_w{worker_count}_delta "
                                    f"current_vs_inline_pct={per_input_parallel_delta:+.2f}"
                                )
                                aggregate_entries.append(
                                    {
                                        "kind": "per_input_delta",
                                        "execution": "parallel",
                                        "worker_count": worker_count,
                                        "opt_level": opt_level,
                                        "input": input_label,
                                        "mode": mode,
                                        "current_avg_MiB_s": current_parallel_avg,
                                        "inline_avg_MiB_s": inline_parallel_avg,
                                        "current_vs_inline_pct": per_input_parallel_delta,
                                    }
                                )

        for opt_level in opt_levels:
            for mode in modes:
                executions: list[tuple[str, int]] = [("serial", 0)]
                if mode in parallel_modes:
                    executions.extend(("parallel", worker_count) for worker_count in parallel_workers)
                for execution, worker_count in executions:
                    current_key = ("current", execution, worker_count, opt_level, mode)
                    inline_key = ("inline", execution, worker_count, opt_level, mode)
                    current_values = mode_averages.get(current_key)
                    if current_values:
                        current_label = f"aggregate_current_{execution}_w{worker_count}_{opt_level}_{mode}" if execution == "parallel" else f"aggregate_current_{opt_level}_{mode}"
                        current_avg = summarize(current_label, current_values)
                        aggregate_entries.append(
                            {
                                "kind": "aggregate_current",
                                "execution": execution,
                                "worker_count": worker_count,
                                "opt_level": opt_level,
                                "mode": mode,
                                "avg_MiB_s": current_avg,
                                "min_MiB_s": min(current_values),
                                "max_MiB_s": max(current_values),
                                "input_count": len(current_values),
                            }
                        )
                    else:
                        current_label = f"aggregate_current_{execution}_w{worker_count}_{opt_level}_{mode}" if execution == "parallel" else f"aggregate_current_{opt_level}_{mode}"
                        print(f"SUMMARY label={current_label} skipped=1 no_successful_inputs=1")
                        aggregate_entries.append(
                            {
                                "kind": "aggregate_current",
                                "execution": execution,
                                "worker_count": worker_count,
                                "opt_level": opt_level,
                                "mode": mode,
                                "skipped": True,
                                "no_successful_inputs": True,
                            }
                        )
                        continue
                    inline_values = mode_averages.get(inline_key)
                    if inline_values:
                        inline_label = f"aggregate_inline_{execution}_w{worker_count}_{opt_level}_{mode}" if execution == "parallel" else f"aggregate_inline_{opt_level}_{mode}"
                        inline_avg = summarize(inline_label, inline_values)
                        aggregate_entries.append(
                            {
                                "kind": "aggregate_inline",
                                "execution": execution,
                                "worker_count": worker_count,
                                "opt_level": opt_level,
                                "mode": mode,
                                "avg_MiB_s": inline_avg,
                                "min_MiB_s": min(inline_values),
                                "max_MiB_s": max(inline_values),
                                "input_count": len(inline_values),
                            }
                        )
                        aggregate_delta = delta_percent(current_avg, inline_avg)
                        delta_label = f"aggregate_{execution}_w{worker_count}_{opt_level}_{mode}_delta" if execution == "parallel" else f"aggregate_{opt_level}_{mode}_delta"
                        print(f"SUMMARY label={delta_label} current_vs_inline_pct={aggregate_delta:+.2f}")
                        aggregate_entries.append(
                            {
                                "kind": "aggregate_delta",
                                "execution": execution,
                                "worker_count": worker_count,
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
                parallel_workers=parallel_workers,
                parallel_modes=parallel_modes,
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
                current_parallel_benches_by_opt=current_parallel_bench_by_opt,
                inline_parallel_benches_by_opt=inline_parallel_bench_by_opt,
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
