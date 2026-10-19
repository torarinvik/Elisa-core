#!/usr/bin/env python3
"""Prepare an exact-source, stdlib-only SMT binder test/benchmark package."""

import argparse
import hashlib
import json
from pathlib import Path


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path, help="new directory for the atom package")
    parser.add_argument("--compiler-root", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    semantic = args.compiler_root / "src" / "semantic"
    source_path = semantic / "analyzer_smt_sview_index.go"
    source_bytes = source_path.read_bytes()
    source = source_bytes.decode("utf-8")
    signature = "func smtTermMentionsQuantifierBinder(term string) bool {"
    if source.count(signature) != 1:
        parser.error("expected exactly one production binder helper")
    start = source.index(signature)
    # The production file is gofmt-formatted; nested closing braces are indented.
    end = source.index("\n}\n", start) + 2
    function = source[start:end]
    go_version = next(line for line in (args.compiler_root / "go.mod").read_text().splitlines()
                      if line.startswith("go "))
    files = {
        "go.mod": ("module binderatom\n\n" + go_version + "\n").encode(),
        "binder.go": ('package semantic\nimport "strings"\n' + function + "\n").encode(),
        "binder_test.go": (semantic / "smt_binder_scan_test.go").read_bytes(),
        "binder_bench_test.go": (semantic / "smt_binder_scan_bench_test.go").read_bytes(),
    }
    output = args.output.absolute()
    output.mkdir(parents=True, exist_ok=False)
    for name, data in files.items():
        (output / name).write_bytes(data)
    manifest = {
        "source": str(source_path.resolve()),
        "source_sha256": digest(source_bytes),
        "function_sha256": digest(function.encode()),
        "files": {name: digest(data) for name, data in files.items()},
        "test_command": ["go", "-C", str(output), "test", "-count=1", "-timeout=15s"],
        "benchmark_command": ["go", "-C", str(output), "test", "-run=^$",
                              "-bench=^BenchmarkSMTBinderScan$", "-benchmem", "-count=3"],
        "scope": "exact helper only; production semantic integration is a separate gate",
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps({"output": str(output), "function_sha256": manifest["function_sha256"]}))


if __name__ == "__main__":
    main()
