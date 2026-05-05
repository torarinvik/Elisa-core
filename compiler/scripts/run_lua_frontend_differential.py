#!/usr/bin/env python3
"""Run a developer-facing Lua differential sweep against the C reference.

This tool compares curated corpus cases against:
- the llcontext Lua frontend (via the native benchmark harness in parse mode)
- the C reference parser (via luaL_loadbufferx linked from onelua.c)

It reports accept/reject mismatches clearly and, for accepted families with
semantic-shape annotations, also prints llcontext-only fingerprints to make
semantic shape easier to inspect during parity work.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path


CHECKSUM_RE = re.compile(r"checksum=([-0-9]+)")


@dataclass
class CaseResult:
    family: str
    path: Path
    expected_accept: bool
    expected_fingerprints: dict[str, int]
    ll_accept: bool
    ref_accept: bool
    ll_fingerprints: dict[str, int]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus-root", default=None, help="Override the curated differential corpus root")
    parser.add_argument("--opt-level", default="-O3", help="Compiler/clang optimization flag")
    parser.add_argument("--strict", action="store_true", help="Exit non-zero when any mismatch is found")
    parser.add_argument("--keep-temp", action="store_true", help="Keep temporary build artifacts")
    parser.add_argument("--json-out", default=None, help="Write a machine-readable JSON report to this path")
    return parser.parse_args()


def run(cmd: list[str], cwd: Path | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, cwd=cwd, check=check, text=True, capture_output=True)


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
    exe_path = out_dir / "lua_reference_parse"
    run(["clang", opt_level, "-std=c99", str(reference_harness), "-lm", "-ldl", "-o", str(exe_path)])
    return exe_path


def parse_checksum(output: str) -> int:
    match = CHECKSUM_RE.search(output)
    if not match:
        raise RuntimeError(f"missing checksum in harness output: {output!r}")
    return int(match.group(1))


def llcontext_accepts(bench_exe: Path, family: str, source_path: Path) -> bool:
    mode = "checked" if family in ("control_flow", "functions_closures", "labels_gotos") else "parse"
    completed = run([str(bench_exe), str(source_path), "1", mode], check=False)
    return completed.returncode == 0


def llcontext_fingerprint(bench_exe: Path, source_path: Path, mode: str) -> int:
    completed = run([str(bench_exe), str(source_path), "1", mode], check=False)
    if completed.returncode != 0:
        return -1
    return parse_checksum(completed.stdout)


def reference_accepts(reference_exe: Path, source_path: Path) -> bool:
    completed = run([str(reference_exe), str(source_path)], check=False)
    return completed.returncode == 0


def iter_cases(corpus_root: Path) -> list[tuple[str, Path, bool]]:
    cases: list[tuple[str, Path, bool, dict[str, int]]] = []
    for source_path in sorted(corpus_root.rglob("*.lua")):
        stem = source_path.stem
        if stem.startswith("accept_"):
            expect_accept = True
        elif stem.startswith("reject_"):
            expect_accept = False
        else:
            raise RuntimeError(f"corpus file must start with accept_ or reject_: {source_path}")
        family = source_path.parent.name
        expected_fingerprints = parse_expected_fingerprints(source_path)
        validate_expected_fingerprints(family, source_path, expect_accept, expected_fingerprints)
        cases.append((family, source_path, expect_accept, expected_fingerprints))
    return cases


def required_modes_for_case(family: str, expected_accept: bool) -> list[str]:
    if not expected_accept:
        return []
    if family == "labels_gotos":
        return ["label", "analysis"]
    if family in ("functions_closures", "globals"):
        return ["env", "closure", "analysis"]
    if family in ("control_flow", "numerics", "operators", "strings_comments", "tables_calls"):
        return ["analysis"]
    return []


def render_status(ok: bool) -> str:
    return "accept" if ok else "reject"


def parse_expected_fingerprints(source_path: Path) -> dict[str, int]:
    expected: dict[str, int] = {}
    prefix = "-- llcontext-"
    for raw_line in source_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line:
            continue
        if not line.startswith("--"):
            break
        if not line.startswith(prefix):
            continue
        payload = line[len(prefix):]
        if ":" not in payload:
            raise RuntimeError(f"invalid llcontext fingerprint annotation in {source_path}: {raw_line}")
        key, value_text = payload.split(":", 1)
        mode = key.strip().removesuffix("-fp")
        if mode not in {"env", "closure", "label", "analysis"}:
            raise RuntimeError(f"unsupported llcontext fingerprint annotation mode {mode!r} in {source_path}")
        expected[mode] = int(value_text.strip())
    return expected


def validate_expected_fingerprints(family: str, source_path: Path, expected_accept: bool, expected: dict[str, int]) -> None:
    required_modes = required_modes_for_case(family, expected_accept)
    missing = [mode for mode in required_modes if mode not in expected]
    if missing:
        raise RuntimeError(f"accepted differential case is missing required annotations {missing!r}: {source_path}")


def evaluate_case(result: CaseResult) -> dict[str, object]:
    ll_vs_ref_match = result.ll_accept == result.ref_accept
    expectation_match = result.ll_accept == result.expected_accept and result.ref_accept == result.expected_accept
    fingerprint_mismatches: dict[str, dict[str, int]] = {}
    for mode, expected in result.expected_fingerprints.items():
        actual = result.ll_fingerprints.get(mode, -1)
        if actual != expected:
            fingerprint_mismatches[mode] = {"expected": expected, "actual": actual}
    fingerprint_match = len(fingerprint_mismatches) == 0
    return {
        "family": result.family,
        "case": result.path.name,
        "path": str(result.path),
        "expected_accept": result.expected_accept,
        "expected_status": render_status(result.expected_accept),
        "llcontext_accept": result.ll_accept,
        "llcontext_status": render_status(result.ll_accept),
        "reference_accept": result.ref_accept,
        "reference_status": render_status(result.ref_accept),
        "ll_vs_ref_match": ll_vs_ref_match,
        "expectation_match": expectation_match,
        "fingerprint_match": fingerprint_match,
        "expected_fingerprints": result.expected_fingerprints,
        "llcontext_fingerprints": result.ll_fingerprints,
        "fingerprint_mismatches": fingerprint_mismatches,
    }


def write_json_report(
    out_path: Path,
    *,
    repo_root: Path,
    corpus_root: Path,
    opt_level: str,
    strict: bool,
    keep_temp: bool,
    temp_root: Path,
    ll_exe: Path,
    ref_exe: Path,
    evaluations: list[dict[str, object]],
    mismatches: int,
    expectation_mismatches: int,
    fingerprint_mismatches: int,
) -> None:
    report = {
        "tool": "run_lua_frontend_differential.py",
        "repo_root": str(repo_root),
        "corpus_root": str(corpus_root),
        "opt_level": opt_level,
        "strict": strict,
        "keep_temp": keep_temp,
        "temp_root": str(temp_root),
        "llcontext_harness": str(ll_exe),
        "reference_harness": str(ref_exe),
        "summary": {
            "cases": len(evaluations),
            "reference_mismatches": mismatches,
            "expectation_mismatches": expectation_mismatches,
            "fingerprint_mismatches": fingerprint_mismatches,
            "strict_failed": strict and (mismatches > 0 or expectation_mismatches > 0 or fingerprint_mismatches > 0),
        },
        "cases": evaluations,
    }
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    args = parse_args()
    repo_root = Path(__file__).resolve().parents[2]
    compiler_dir = repo_root / "compiler"
    frontend_path = repo_root / "Code" / "llcontext_lua" / "src" / "lua_frontend.llcontext"
    llcontext_harness = repo_root / "Code" / "benchmarks" / "lua_frontend_bench.c"
    reference_harness = repo_root / "Code" / "benchmarks" / "lua_reference_parse_harness.c"
    corpus_root = Path(args.corpus_root) if args.corpus_root else repo_root / "Code" / "llcontext_lua" / "test" / "differential_corpus"

    temp_root_obj = tempfile.TemporaryDirectory(prefix="lua_frontend_differential.")
    temp_root = Path(temp_root_obj.name)
    ll_out = temp_root / "ll"
    ref_out = temp_root / "ref"
    ll_out.mkdir(parents=True, exist_ok=True)
    ref_out.mkdir(parents=True, exist_ok=True)

    mismatches = 0
    expectation_mismatches = 0
    fingerprint_mismatches = 0

    try:
        ll_exe = build_llcontext_harness(compiler_dir, frontend_path, llcontext_harness, ll_out, args.opt_level)
        ref_exe = build_reference_harness(reference_harness, ref_out, args.opt_level)

        if args.keep_temp:
            print(f"temp_root={temp_root}")

        results: list[CaseResult] = []
        for family, source_path, expected_accept, expected_fingerprints in iter_cases(corpus_root):
            ll_accept = llcontext_accepts(ll_exe, family, source_path)
            ref_accept = reference_accepts(ref_exe, source_path)
            fingerprints = {
                mode: llcontext_fingerprint(ll_exe, source_path, mode)
                for mode in required_modes_for_case(family, expected_accept)
            }
            results.append(CaseResult(family, source_path, expected_accept, expected_fingerprints, ll_accept, ref_accept, fingerprints))

        evaluations: list[dict[str, object]] = []
        for result in results:
            evaluation = evaluate_case(result)
            evaluations.append(evaluation)
            ll_vs_ref = "MATCH" if evaluation["ll_vs_ref_match"] else "MISMATCH"
            if not evaluation["ll_vs_ref_match"]:
                mismatches += 1
            if not evaluation["expectation_match"]:
                expectation_mismatches += 1
            fp_status = "FP_MATCH" if evaluation["fingerprint_match"] else "FP_MISMATCH"
            if not evaluation["fingerprint_match"]:
                fingerprint_mismatches += 1
            extras = ""
            if result.ll_fingerprints:
                extras = " " + " ".join(f"{mode}_fp={value}" for mode, value in result.ll_fingerprints.items())
            if result.expected_fingerprints:
                extras += " " + " ".join(f"expected_{mode}_fp={value}" for mode, value in result.expected_fingerprints.items())
            print(
                f"{ll_vs_ref} {fp_status} family={result.family} case={result.path.name} "
                f"expected={render_status(result.expected_accept)} "
                f"llcontext={render_status(result.ll_accept)} "
                f"reference={render_status(result.ref_accept)}"
                f"{extras}"
            )

        print(
            f"SUMMARY cases={len(results)} reference_mismatches={mismatches} "
            f"expectation_mismatches={expectation_mismatches} "
            f"fingerprint_mismatches={fingerprint_mismatches} corpus_root={corpus_root}"
        )

        if args.json_out:
            write_json_report(
                Path(args.json_out),
                repo_root=repo_root,
                corpus_root=corpus_root,
                opt_level=args.opt_level,
                strict=args.strict,
                keep_temp=args.keep_temp,
                temp_root=temp_root,
                ll_exe=ll_exe,
                ref_exe=ref_exe,
                evaluations=evaluations,
                mismatches=mismatches,
                expectation_mismatches=expectation_mismatches,
                fingerprint_mismatches=fingerprint_mismatches,
            )
    finally:
        if not args.keep_temp:
            temp_root_obj.cleanup()

    if args.strict and (mismatches > 0 or expectation_mismatches > 0 or fingerprint_mismatches > 0):
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
