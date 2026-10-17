#!/usr/bin/env python3
"""Measure exact-proof reuse separately from whole-function incrementality."""
import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import statistics
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--functions", type=int, default=512)
    parser.add_argument("--pairs", type=int, default=7)
    args = parser.parse_args()
    if args.functions < 2 or args.pairs < 1:
        parser.error("functions must be >=2 and pairs positive")
    binary, root = args.compiler.resolve(), args.output.resolve()
    root.mkdir(parents=True, exist_ok=True)
    if any(root.iterdir()):
        parser.error("output directory must be empty")
    compiler_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
    source = root / "workload.elisa"
    base = (
        "law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi\n"
        "type Small = i64 is Bounded[2, 10]\n"
        + "".join(
            f"def mul_{i}(a: Small, b: Small) -> i64 is Bounded[4, {100+i}]:\n"
            "    return a * b\n" for i in range(args.functions)
        )
    )
    rows = []

    def run(label, cached, text):
        source.write_text(text)
        env = os.environ.copy()
        env["ELISACORE_BUILD_CACHE"] = "0"
        env.pop("ELISA_SMT_TRACE", None)
        env.pop("ELISA_SMT_RLIMIT_LOG", None)
        if cached:
            env["ELISACORE_SMT_CACHE_DIR"] = str(root / "cache")
        else:
            env.pop("ELISACORE_SMT_CACHE_DIR", None)
        start = time.perf_counter()
        result = subprocess.run(
            [str(binary), "--explain", "-emit", "semantic", str(source)],
            env=env, capture_output=True, timeout=120,
        )
        elapsed = time.perf_counter() - start
        stem = f"{len(rows):03d}-{label}"
        (root / (stem + ".out")).write_bytes(result.stdout)
        (root / (stem + ".err")).write_bytes(result.stderr)
        lines = result.stderr.decode().splitlines()
        profiles = [line for line in lines if "SMT tier:" in line]
        diagnostics = "\n".join(line for line in lines if "SMT tier:" not in line)
        if result.returncode or len(profiles) != 1:
            raise RuntimeError(f"invalid measurement {stem}; inspect saved logs")
        row = dict(label=label, seconds=elapsed, profile=profiles[0],
                   stdout_sha256=hashlib.sha256(result.stdout).hexdigest(),
                   diagnostics_sha256=hashlib.sha256(diagnostics.encode()).hexdigest())
        counters = re.search(r"SMT tier: (\d+) obligations, (\d+) proven, (\d+) declined, (\d+) cached;", profiles[0])
        persistent = re.search(r"; (\d+) persistent$", profiles[0])
        if counters is None:
            raise RuntimeError("missing proof counters")
        row.update(zip(("obligations", "proven", "declined", "cached"), map(int, counters.groups())))
        row["persistent"] = int(persistent.group(1)) if persistent else 0
        if label == "warm" and (row["persistent"] == 0 or row["cached"] != row["obligations"]):
            raise RuntimeError("warm run did not avoid all solver queries")
        if label == "body-edit-warm" and (row["persistent"] == 0 or row["cached"] >= row["obligations"]):
            raise RuntimeError("body edit did not both reuse and re-solve queries")
        if not cached and row["persistent"]:
            raise RuntimeError("disabled cache produced a persistent hit")
        rows.append(row)
        return row

    run("prime-disabled", False, base)
    run("prime-cache", True, base)
    for pair in range(args.pairs):
        arms = [("disabled", False), ("warm", True)]
        if pair % 2:
            arms.reverse()
        for label, cached in arms:
            run(label, cached, base)
    point = base.rfind("return a * b")
    edited = base[:point] + base[point:].replace("return a * b", "return a * b + 1", 1)
    edit_warm = run("body-edit-warm", True, edited)
    edit_cold = run("body-edit-disabled", False, edited)
    groups = [rows[:-2], [edit_warm, edit_cold]]
    for group in groups:
        if len({(row["stdout_sha256"], row["diagnostics_sha256"]) for row in group}) != 1:
            raise RuntimeError("clean/cached output mismatch; inspect saved logs")
    if hashlib.sha256(binary.read_bytes()).hexdigest() != compiler_hash:
        raise RuntimeError("compiler changed during measurement")
    medians = {label: statistics.median(row["seconds"] for row in rows if row["label"] == label)
               for label in ("disabled", "warm")}
    report = dict(scope="synthetic proof workload; not full incremental compilation",
                  compiler_sha256=compiler_hash, functions=args.functions,
                  pairs=args.pairs, medians_seconds=medians, rows=rows)
    (root / "measurements.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(medians, indent=2))


if __name__ == "__main__":
    main()
