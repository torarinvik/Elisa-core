#!/usr/bin/env python3
"""Benchmark llcontext Lua parse throughput against the C reference parser.

This script only compares parse throughput because the llcontext-only modes
`env`, `closure`, `label`, and `analysis` do not have direct C-reference
 equivalents.
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

from lua_frontend_benchmark_input import build_synthetic_lua_benchmark_input


MIB_PER_SECOND_RE = re.compile(r"MiB/s=([0-9]+(?:\.[0-9]+)?)")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stmt-count", type=int, default=4000, help="Approximate statement budget for the generated mixed-Lua synthetic input")
    parser.add_argument("--parse-iterations", type=int, default=20, help="Iterations per benchmark run")
    parser.add_argument("--repeats", type=int, default=3, help="Number of repeated runs per input")
    parser.add_argument("--corpus-manifest", default=None, help="Optional manifest of real-Lua corpus files relative to the repo root")
    parser.add_argument("--skip-real-corpus", action="store_true", help="Only benchmark the synthetic generated input")
    parser.add_argument("--opt-level", default="-O3", help="Compiler/clang optimization flag")
    parser.add_argument("--keep-temp", action="store_true", help="Keep temporary build artifacts")
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


def build_llcontext_harness(compiler_dir: Path, frontend_path: Path, harness_path: Path, out_dir: Path, opt_level: str) -> Path:
    header_path = out_dir / "lua_frontend.h"
    object_path = out_dir / "lua_frontend.o"
    exe_path = out_dir / "lua_frontend_bench"
    benchmarks_dir = harness_path.parent
    runtime_shims_path = benchmarks_dir / "json_parser_runtime_shims.c"
    concurrency_runtime_path = compiler_dir / "runtime" / "concurrency.c"
    run(["go", "run", "./src", opt_level, "-emit", "header", "-o", str(header_path), str(frontend_path)], cwd=compiler_dir)
    run(["go", "run", "./src", opt_level, "-emit", "obj", "-o", str(object_path), str(frontend_path)], cwd=compiler_dir)
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
            str(exe_path),
        ],
        cwd=compiler_dir,
    )
    return exe_path


def build_reference_harness(reference_harness: Path, out_dir: Path, opt_level: str) -> Path:
    exe_path = out_dir / "lua_reference_bench"
    run(["clang", opt_level, "-std=c99", str(reference_harness), "-lm", "-ldl", "-o", str(exe_path)])
    return exe_path


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


def run_suite(exe: Path, input_path: Path, iterations: int, repeats: int, label: str) -> list[float]:
    print(label)
    mib_values: list[float] = []
    for _ in range(repeats):
        try:
            completed = run([str(exe), str(input_path), str(iterations)], capture_output=True)
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


def summarize_variant(label: str, values: list[float]) -> dict[str, float]:
    avg = statistics.mean(values)
    summary = {
        "avg_MiB_s": avg,
        "min_MiB_s": min(values),
        "max_MiB_s": max(values),
    }
    print(f"SUMMARY label={label} avg_MiB_s={avg:.2f} min_MiB_s={summary['min_MiB_s']:.2f} max_MiB_s={summary['max_MiB_s']:.2f}")
    return summary


def ratio_percent(current: float, reference: float) -> float:
    if reference == 0.0:
        return 0.0
    return current / reference


def write_json_report(
    out_path: Path,
    *,
    repo_root: Path,
    manifest_path: Path,
    temp_root: Path,
    keep_temp: bool,
    opt_level: str,
    stmt_count: int,
    parse_iterations: int,
    repeats: int,
    skip_real_corpus: bool,
    llcontext_bench: Path,
    reference_bench: Path,
    synthetic_input: dict[str, object],
    inputs: list[tuple[str, Path]],
    runs: list[dict[str, object]],
    skipped: list[dict[str, str]],
    aggregate: dict[str, object],
    real_corpus_aggregate: dict[str, object],
) -> None:
    report = {
        "tool": "run_lua_frontend_reference_benchmark.py",
        "repo_root": str(repo_root),
        "manifest_path": str(manifest_path),
        "temp_root": str(temp_root),
        "keep_temp": keep_temp,
        "opt_level": opt_level,
        "stmt_count": stmt_count,
        "parse_iterations": parse_iterations,
        "repeats": repeats,
        "skip_real_corpus": skip_real_corpus,
        "llcontext_bench": str(llcontext_bench),
        "reference_bench": str(reference_bench),
        "synthetic_input": synthetic_input,
        "inputs": [{"label": label, "path": str(path)} for label, path in inputs],
        "runs": runs,
        "skipped": skipped,
        "aggregate": aggregate,
        "real_corpus_aggregate": real_corpus_aggregate,
        "summary": {
            "run_count": len(runs),
            "skipped_count": len(skipped),
        },
    }
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    args = parse_args()
    if args.stmt_count <= 0 or args.parse_iterations <= 0 or args.repeats <= 0:
        raise SystemExit("all iteration/count arguments must be positive")

    repo_root = Path(__file__).resolve().parents[2]
    compiler_dir = repo_root / "compiler"
    frontend_path = repo_root / "Code" / "llcontext_lua" / "src" / "lua_frontend.llcontext"
    llcontext_harness = repo_root / "Code" / "benchmarks" / "lua_frontend_bench.c"
    reference_harness = repo_root / "Code" / "benchmarks" / "lua_reference_bench.c"
    manifest_path = Path(args.corpus_manifest) if args.corpus_manifest else repo_root / "Code" / "benchmarks" / "lua_frontend_corpus_manifest.txt"

    temp_root_obj = tempfile.TemporaryDirectory(prefix="lua_frontend_reference_bench.")
    temp_root = Path(temp_root_obj.name)
    input_path = temp_root / "synthetic_bench.lua"
    ll_out = temp_root / "llcontext"
    ref_out = temp_root / "reference"
    ll_out.mkdir(parents=True, exist_ok=True)
    ref_out.mkdir(parents=True, exist_ok=True)

    try:
        synthetic_input = build_synthetic_lua_benchmark_input(input_path, args.stmt_count)
        llcontext_bench = build_llcontext_harness(compiler_dir, frontend_path, llcontext_harness, ll_out, args.opt_level)
        reference_bench = build_reference_harness(reference_harness, ref_out, args.opt_level)

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

        runs: list[dict[str, object]] = []
        skipped: list[dict[str, str]] = []
        ll_averages: list[float] = []
        ref_averages: list[float] = []
        real_ll_averages: list[float] = []
        real_ref_averages: list[float] = []

        for input_label, bench_input in inputs:
            try:
                ll_values = run_suite(
                    llcontext_bench,
                    bench_input,
                    args.parse_iterations,
                    args.repeats,
                    f"LLCONTEXT_{input_label}_parse".upper(),
                )
            except RuntimeError as exc:
                if input_label == "synthetic":
                    raise
                print(f"SKIP label=llcontext_{input_label}_parse reason={exc}")
                skipped.append({"variant": "llcontext", "input": input_label, "path": str(bench_input), "reason": str(exc)})
                continue

            try:
                ref_values = run_suite(
                    reference_bench,
                    bench_input,
                    args.parse_iterations,
                    args.repeats,
                    f"REFERENCE_{input_label}_parse".upper(),
                )
            except RuntimeError as exc:
                if input_label == "synthetic":
                    raise
                print(f"SKIP label=reference_{input_label}_parse reason={exc}")
                skipped.append({"variant": "reference", "input": input_label, "path": str(bench_input), "reason": str(exc)})
                continue

            ll_summary = summarize_variant(f"llcontext_{input_label}_parse", ll_values)
            ref_summary = summarize_variant(f"reference_{input_label}_parse", ref_values)
            ratio = ratio_percent(ll_summary["avg_MiB_s"], ref_summary["avg_MiB_s"])
            print(f"SUMMARY label={input_label}_parse_ratio ll_over_reference={ratio:.3f}")

            ll_averages.append(ll_summary["avg_MiB_s"])
            ref_averages.append(ref_summary["avg_MiB_s"])
            if input_label != "synthetic":
                real_ll_averages.append(ll_summary["avg_MiB_s"])
                real_ref_averages.append(ref_summary["avg_MiB_s"])
            runs.append(
                {
                    "input": input_label,
                    "input_path": str(bench_input),
                    "mode": "parse",
                    "iterations": args.parse_iterations,
                    "repeats": args.repeats,
                    "llcontext": {"values_MiB_s": ll_values, **ll_summary},
                    "reference": {"values_MiB_s": ref_values, **ref_summary},
                    "ll_over_reference": ratio,
                }
            )

        aggregate: dict[str, object]
        real_corpus_aggregate: dict[str, object]
        if ll_averages and ref_averages:
            ll_avg = statistics.mean(ll_averages)
            ref_avg = statistics.mean(ref_averages)
            aggregate = {
                "mode": "parse",
                "common_input_count": len(ll_averages),
                "llcontext_avg_MiB_s": ll_avg,
                "reference_avg_MiB_s": ref_avg,
                "ll_over_reference": ratio_percent(ll_avg, ref_avg),
                "llcontext_min_MiB_s": min(ll_averages),
                "llcontext_max_MiB_s": max(ll_averages),
                "reference_min_MiB_s": min(ref_averages),
                "reference_max_MiB_s": max(ref_averages),
            }
            print(f"SUMMARY common_inputs={aggregate['common_input_count']}")
            print(f"SUMMARY aggregate_llcontext_parse_avg_MiB_s={ll_avg:.2f}")
            print(f"SUMMARY aggregate_reference_parse_avg_MiB_s={ref_avg:.2f}")
            print(f"SUMMARY aggregate_ll_over_reference={aggregate['ll_over_reference']:.3f}")
        else:
            aggregate = {"mode": "parse", "common_input_count": 0, "skipped": True}
            print("SUMMARY common_inputs=0")

        if real_ll_averages and real_ref_averages:
            real_ll_avg = statistics.mean(real_ll_averages)
            real_ref_avg = statistics.mean(real_ref_averages)
            real_corpus_aggregate = {
                "mode": "parse",
                "common_input_count": len(real_ll_averages),
                "llcontext_avg_MiB_s": real_ll_avg,
                "reference_avg_MiB_s": real_ref_avg,
                "ll_over_reference": ratio_percent(real_ll_avg, real_ref_avg),
                "llcontext_min_MiB_s": min(real_ll_averages),
                "llcontext_max_MiB_s": max(real_ll_averages),
                "reference_min_MiB_s": min(real_ref_averages),
                "reference_max_MiB_s": max(real_ref_averages),
            }
            print(f"SUMMARY real_corpus_common_inputs={real_corpus_aggregate['common_input_count']}")
            print(f"SUMMARY real_corpus_llcontext_parse_avg_MiB_s={real_ll_avg:.2f}")
            print(f"SUMMARY real_corpus_reference_parse_avg_MiB_s={real_ref_avg:.2f}")
            print(f"SUMMARY real_corpus_ll_over_reference={real_corpus_aggregate['ll_over_reference']:.3f}")
        else:
            real_corpus_aggregate = {"mode": "parse", "common_input_count": 0, "skipped": True}
            print("SUMMARY real_corpus_common_inputs=0")

        if args.json_out:
            write_json_report(
                Path(args.json_out),
                repo_root=repo_root,
                manifest_path=manifest_path,
                temp_root=temp_root,
                keep_temp=args.keep_temp,
                opt_level=args.opt_level,
                stmt_count=args.stmt_count,
                parse_iterations=args.parse_iterations,
                repeats=args.repeats,
                skip_real_corpus=args.skip_real_corpus,
                llcontext_bench=llcontext_bench,
                reference_bench=reference_bench,
                synthetic_input=synthetic_input,
                inputs=inputs,
                runs=runs,
                skipped=skipped,
                aggregate=aggregate,
                real_corpus_aggregate=real_corpus_aggregate,
            )
    finally:
        if not args.keep_temp:
            temp_root_obj.cleanup()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())