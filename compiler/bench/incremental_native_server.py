#!/usr/bin/env python3
"""Measure novel source edits through a real local compile-server executable.

Includes HTTP, parsing, analysis, lowering and object generation. Excludes final
executable linking/execution. This synthetic workload is not project qualification.
"""
import argparse
import base64
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import platform
import socket
import statistics
import subprocess
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--functions", type=int, default=256)
    parser.add_argument("--samples", type=int, default=9)
    parser.add_argument("--concurrency", type=int, default=4)
    parser.add_argument("--batches", type=int, default=5)
    args = parser.parse_args()
    if min(args.functions, args.samples, args.concurrency, args.batches) < 1:
        parser.error("workload sizes must be positive")
    compiler = args.compiler.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=True)
    output = args.output.resolve()
    # The native cache uses eight object partitions. Hold the ordinary backend
    # at the same worker count so timing differences measure reuse rather than
    # a different partition topology. Preserve target/linker settings for audit.
    os.environ["ELISACORE_CODEGEN_JOBS"] = "8"
    result = {
        "compiler_sha256": hashlib.sha256(compiler.read_bytes()).hexdigest(),
        "script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        "functions": args.functions,
        "platform": platform.system() + "/" + platform.machine(),
        "codegen_jobs": 8,
        "target_cpu_env": os.getenv("ELISACORE_TARGET_CPU", ""),
        "linker_env": os.getenv("ELISA_LD", ""),
        "parallel_opt_env": os.getenv("ELISACORE_PARALLEL_OPT", ""),
        "parallel_opt_import_env": os.getenv("ELISACORE_PARALLEL_OPT_IMPORT", ""),
        "limitations": ["synthetic O0 source-to-object workload", "no final link/execution", "short shared-host samples"],
    }
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    url = f"http://127.0.0.1:{port}"
    server_log = (output / "server.stderr").open("w")
    server = subprocess.Popen(
        [str(compiler), "-emit", "serve", "-addr", f"127.0.0.1:{port}"],
        stdout=subprocess.DEVNULL, stderr=server_log,
    )

    def payload(factor, incremental, filename):
        functions = []
        for i in range(args.functions):
            term = f"(value{i} * 2 + delta{i} * 3)"
            expression = " + ".join([term] * 16)
            if i == 0:
                expression = expression.replace("value0 * 2", f"value0 * {factor}", 1)
            functions.append(f"def work{i}(value{i}: int, delta{i}: int) -> int:\n    return {expression}\n\n")
        return json.dumps({"mode": "obj", "filename": filename,
                           "source": "".join(functions), "incremental": incremental}).encode()

    def send(data):
        request = urllib.request.Request(url + "/api/v1/compile", data, {"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=90) as response:
            body = json.load(response)
        if not body.get("ok") or not base64.b64decode(body.get("object", "")):
            raise RuntimeError(body)

    def timed(data):
        start = time.perf_counter()
        send(data)
        return (time.perf_counter() - start) * 1000

    try:
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if server.poll() is not None:
                raise RuntimeError("compile server exited during startup")
            try:
                with urllib.request.urlopen(url + "/healthz", timeout=1):
                    break
            except OSError:
                time.sleep(0.05)
        else:
            raise RuntimeError("compile server startup timeout")
        filename = "benchmark-single.elisa"
        send(payload(2, False, filename))  # initialize LLVM and ordinary worker path
        result["cold_incremental_ms"] = timed(payload(2, True, filename))
        samples = []
        for i in range(args.samples):
            row = {"factor": i + 4}
            for retained in ([False, True] if i % 2 == 0 else [True, False]):
                row["incremental_ms" if retained else "fresh_ms"] = timed(payload(i + 4, retained, filename))
            samples.append(row)
            print(json.dumps({"single": row}), flush=True)
        result["single"] = samples
        result["fresh_median_ms"] = statistics.median(x["fresh_ms"] for x in samples)
        result["incremental_median_ms"] = statistics.median(x["incremental_ms"] for x in samples)

        batches = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
            warm = [payload(2, True, f"benchmark-concurrent-{i}.elisa") for i in range(args.concurrency)]
            list(executor.map(send, warm))
            for batch in range(args.batches):
                row = {"batch": batch, "requests": args.concurrency}
                for retained in ([False, True] if batch % 2 == 0 else [True, False]):
                    # Prepare client source/JSON before timing. Every logical unit
                    # receives a new edit; no complete prior revision can hit.
                    data = [payload(100 + batch * args.concurrency + i, retained,
                                    f"benchmark-concurrent-{i}.elisa") for i in range(args.concurrency)]
                    start = time.perf_counter()
                    list(executor.map(send, data))
                    row["incremental_ms" if retained else "fresh_ms"] = (time.perf_counter() - start) * 1000
                batches.append(row)
                print(json.dumps({"concurrent": row}), flush=True)
        result["concurrency"] = args.concurrency
        result["batches"] = batches
        for mode in ("fresh", "incremental"):
            elapsed = statistics.median(x[mode + "_ms"] for x in batches)
            result[mode + "_batch_median_ms"] = elapsed
            result[mode + "_requests_per_second"] = args.concurrency * 1000 / elapsed
        (output / "results.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps(result), flush=True)
    finally:
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait()
        server_log.close()


if __name__ == "__main__":
    main()
