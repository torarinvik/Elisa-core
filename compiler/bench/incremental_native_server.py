#!/usr/bin/env python3
"""Measure novel source edits through a real local compile-server executable.

Includes HTTP, parsing, analysis, lowering and object generation. With --execute,
link/run checks follow each measured object and single-edit end-to-end latency
is reported separately. Concurrent batch timing remains compile-only. This
synthetic workload is not project qualification.
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
import shutil
import tempfile
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
    parser.add_argument("--execute", action="store_true", help="link and execute each measured object; report single-edit end-to-end latency")
    parser.add_argument("--clang", default=os.getenv("ELISA_CLANG", "clang"))
    args = parser.parse_args()
    if min(args.functions, args.samples, args.concurrency, args.batches) < 1:
        parser.error("workload sizes must be positive")
    if args.execute:
        args.clang = shutil.which(args.clang)
        if not args.clang:
            parser.error("native execution requires an available clang")
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
        "limitations": ["synthetic O0 source-to-object workload", "native execution verified" if args.execute else "no final link/execution", "short shared-host samples"],
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
        if args.execute:
            functions.append("def main() -> int:\n    return work0(1, 1)\n")
        return json.dumps({"mode": "obj", "filename": filename,
                           "source": "".join(functions), "incremental": incremental}).encode()

    def send(data):
        request = urllib.request.Request(url + "/api/v1/compile", data, {"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=90) as response:
            body = json.load(response)
        if not body.get("ok"):
            raise RuntimeError(body)
        artifact = base64.b64decode(body.get("object", ""), validate=True)
        if not artifact:
            raise RuntimeError("compiler returned an empty object")
        return artifact

    def qualify(artifact, factor):
        # All sixteen terms use (1 * 2 + 1 * 3), except the first edited
        # multiplication. Native process exit status retains the low byte.
        expected = (factor + 78) % 256
        with tempfile.TemporaryDirectory(prefix="native-", dir=output) as directory:
            obj = Path(directory) / "edit.o"
            executable = Path(directory) / "edit"
            obj.write_bytes(artifact)
            start = time.perf_counter()
            linked = subprocess.run([args.clang, str(obj), "-o", str(executable)],
                                    capture_output=True, timeout=90)
            link_ms = (time.perf_counter() - start) * 1000
            if linked.returncode:
                raise RuntimeError("native link failed: " + linked.stderr.decode(errors="replace"))
            start = time.perf_counter()
            executed = subprocess.run([str(executable)], capture_output=True, timeout=90)
            execute_ms = (time.perf_counter() - start) * 1000
            if executed.returncode != expected:
                (output / "failed-native-object.o").write_bytes(artifact)
                raise RuntimeError(f"native result {executed.returncode}, expected {expected}, factor {factor}")
            return {"link_ms": link_ms, "execute_ms": execute_ms, "expected_exit": expected}

    def timed(data, factor):
        start = time.perf_counter()
        artifact = send(data)
        compile_ms = (time.perf_counter() - start) * 1000
        native = qualify(artifact, factor) if args.execute else {}
        if native:
            native["end_to_end_ms"] = (time.perf_counter() - start) * 1000
        return compile_ms, native

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
        result["cold_incremental_ms"], result["cold_native"] = timed(payload(2, True, filename), 2)
        samples = []
        for i in range(args.samples):
            row = {"factor": i + 4}
            for retained in ([False, True] if i % 2 == 0 else [True, False]):
                mode = "incremental" if retained else "fresh"
                row[mode + "_ms"], native = timed(payload(i + 4, retained, filename), i + 4)
                row.update({mode + "_" + key: value for key, value in native.items()})
            samples.append(row)
            print(json.dumps({"single": row}), flush=True)
        result["single"] = samples
        result["fresh_median_ms"] = statistics.median(x["fresh_ms"] for x in samples)
        result["incremental_median_ms"] = statistics.median(x["incremental_ms"] for x in samples)

        if args.execute:
            for mode in ("fresh", "incremental"):
                result[mode + "_end_to_end_median_ms"] = statistics.median(x[mode + "_end_to_end_ms"] for x in samples)
            result["clang"] = args.clang

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
                    artifacts = list(executor.map(send, data))
                    mode = "incremental" if retained else "fresh"
                    row[mode + "_ms"] = (time.perf_counter() - start) * 1000
                    if args.execute:
                        factors = [100 + batch * args.concurrency + i for i in range(args.concurrency)]
                        list(executor.map(qualify, artifacts, factors))
                        row[mode + "_native_verified"] = len(artifacts)
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
