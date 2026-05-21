#!/usr/bin/env python3
"""Benchmark elisacore Lua execution throughput against the PUC Lua reference harness."""

from __future__ import annotations

import argparse
import json
import re
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path


MIB_PER_SECOND_RE = re.compile(r"MiB/s=([0-9]+(?:\.[0-9]+)?)")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--iterations", type=int, default=5000, help="Iterations per benchmark run")
    parser.add_argument("--repeats", type=int, default=3, help="Number of repeated runs per input")
    parser.add_argument("--corpus-manifest", default=None, help="Optional manifest of execution-focused Lua corpus files relative to the repo root")
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


def load_corpus_manifest(repo_root: Path, manifest_path: Path) -> list[tuple[str, Path]]:
    inputs: list[tuple[str, Path]] = []
    for raw_line in manifest_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        source_path = (repo_root / line).resolve()
        if not source_path.exists():
            raise RuntimeError(f"missing corpus file listed in {manifest_path}: {line}")
        inputs.append((source_path.stem.replace(".", "_"), source_path))
    return inputs


def build_elisacore_harness(compiler_dir: Path, frontend_path: Path, harness_path: Path, out_dir: Path, opt_level: str) -> Path:
    header_path = out_dir / "lua_frontend.h"
    object_path = out_dir / "lua_frontend.o"
    exe_path = out_dir / "lua_frontend_execute_bench"
    benchmarks_dir = harness_path.parent
    runtime_shims_path = benchmarks_dir / "json_parser_runtime_shims.c"
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
            str(object_path),
            "-o",
            str(exe_path),
        ],
        cwd=compiler_dir,
    )
    return exe_path


def build_reference_harness(reference_harness: Path, out_dir: Path, opt_level: str) -> Path:
    exe_path = out_dir / "lua_reference_execute_bench"
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
    iterations: int,
    repeats: int,
    elisacore_bench: Path,
    reference_bench: Path,
    inputs: list[tuple[str, Path]],
    runs: list[dict[str, object]],
    aggregate: dict[str, object],
) -> None:
    report = {
        "tool": "run_lua_frontend_execution_benchmark.py",
        "repo_root": str(repo_root),
        "manifest_path": str(manifest_path),
        "temp_root": str(temp_root),
        "keep_temp": keep_temp,
        "opt_level": opt_level,
        "iterations": iterations,
        "repeats": repeats,
        "elisacore_bench": str(elisacore_bench),
        "reference_bench": str(reference_bench),
        "inputs": [{"label": label, "path": str(path)} for label, path in inputs],
        "runs": runs,
        "aggregate": aggregate,
        "summary": {
            "run_count": len(runs),
        },
    }
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    args = parse_args()
    if args.iterations <= 0 or args.repeats <= 0:
        raise SystemExit("iterations and repeats must be positive")

    repo_root = Path(__file__).resolve().parents[2]
    compiler_dir = repo_root / "compiler"
    frontend_path = repo_root / "Code" / "elisacore_lua" / "src" / "lua_frontend.elisa"
    elisacore_harness = repo_root / "Code" / "benchmarks" / "lua_frontend_execute_bench.c"
    reference_harness = repo_root / "Code" / "benchmarks" / "lua_reference_execute_bench.c"
    manifest_path = Path(args.corpus_manifest) if args.corpus_manifest else repo_root / "Code" / "benchmarks" / "lua_frontend_execution_corpus_manifest.txt"
    if not manifest_path.exists():
        raise SystemExit(f"missing corpus manifest: {manifest_path}")

    temp_root_obj = tempfile.TemporaryDirectory(prefix="lua_frontend_execute_bench.")
    temp_root = Path(temp_root_obj.name)
    ll_out = temp_root / "elisacore"
    ref_out = temp_root / "reference"
    ll_out.mkdir(parents=True, exist_ok=True)
    ref_out.mkdir(parents=True, exist_ok=True)

    try:
        elisacore_bench = build_elisacore_harness(compiler_dir, frontend_path, elisacore_harness, ll_out, args.opt_level)
        reference_bench = build_reference_harness(reference_harness, ref_out, args.opt_level)
        inputs = load_corpus_manifest(repo_root, manifest_path)
        if not inputs:
            raise SystemExit(f"execution corpus manifest is empty: {manifest_path}")

        if args.keep_temp:
            print(f"temp_root={temp_root}")
        else:
            print(f"temp_root={temp_root} (temporary)")

        runs: list[dict[str, object]] = []
        ll_averages: list[float] = []
        ref_averages: list[float] = []

        for input_label, bench_input in inputs:
            ll_values = run_suite(
                elisacore_bench,
                bench_input,
                args.iterations,
                args.repeats,
                f"ELISACORE_{input_label}_EXECUTE".upper(),
            )
            ref_values = run_suite(
                reference_bench,
                bench_input,
                args.iterations,
                args.repeats,
                f"REFERENCE_{input_label}_EXECUTE".upper(),
            )
            ll_summary = summarize_variant(f"elisacore_{input_label}_execute", ll_values)
            ref_summary = summarize_variant(f"reference_{input_label}_execute", ref_values)
            ratio = ratio_percent(ll_summary["avg_MiB_s"], ref_summary["avg_MiB_s"])
            print(f"SUMMARY label={input_label}_execute_ratio ll_over_reference={ratio:.3f}")

            ll_averages.append(ll_summary["avg_MiB_s"])
            ref_averages.append(ref_summary["avg_MiB_s"])
            runs.append(
                {
                    "input": input_label,
                    "input_path": str(bench_input),
                    "mode": "execute",
                    "iterations": args.iterations,
                    "repeats": args.repeats,
                    "elisacore": {"values_MiB_s": ll_values, **ll_summary},
                    "reference": {"values_MiB_s": ref_values, **ref_summary},
                    "ll_over_reference": ratio,
                }
            )

        aggregate = {
            "mode": "execute",
            "common_input_count": len(ll_averages),
            "elisacore_avg_MiB_s": statistics.mean(ll_averages),
            "reference_avg_MiB_s": statistics.mean(ref_averages),
            "ll_over_reference": ratio_percent(statistics.mean(ll_averages), statistics.mean(ref_averages)),
            "elisacore_min_MiB_s": min(ll_averages),
            "elisacore_max_MiB_s": max(ll_averages),
            "reference_min_MiB_s": min(ref_averages),
            "reference_max_MiB_s": max(ref_averages),
        }
        print(f"SUMMARY common_inputs={aggregate['common_input_count']}")
        print(f"SUMMARY aggregate_elisacore_execute_avg_MiB_s={aggregate['elisacore_avg_MiB_s']:.2f}")
        print(f"SUMMARY aggregate_reference_execute_avg_MiB_s={aggregate['reference_avg_MiB_s']:.2f}")
        print(f"SUMMARY aggregate_ll_over_reference={aggregate['ll_over_reference']:.3f}")

        if args.json_out:
            write_json_report(
                Path(args.json_out),
                repo_root=repo_root,
                manifest_path=manifest_path,
                temp_root=temp_root,
                keep_temp=args.keep_temp,
                opt_level=args.opt_level,
                iterations=args.iterations,
                repeats=args.repeats,
                elisacore_bench=elisacore_bench,
                reference_bench=reference_bench,
                inputs=inputs,
                runs=runs,
                aggregate=aggregate,
            )
    finally:
        if not args.keep_temp:
            temp_root_obj.cleanup()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())