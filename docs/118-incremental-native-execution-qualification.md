# Incremental HTTP compilation through native execution

The opt-in `compiler/bench/incremental_native_server.py --execute` mode now
links and executes every measured object. Its generated `main` calls every
`workN` function and checks each unchanged function's exact expected result.
It then returns the edited `work0` result, which the runner checks against the
expected process status. One body edit changes only the first function. Both
fresh and retained compilations must return the expected status. A nonempty
object is no longer enough to qualify these execution-enabled runs.

Single-edit records separate HTTP compile, final link and process execution time,
and report latency through execution. They include response decoding, temporary
object writing and cleanup in that final interval. Concurrent batch throughput
continues to measure compilation only; native checks follow outside its timer
and each batch records how many artifacts were verified. Linker and first-launch
OS costs are therefore visible without being mislabeled as compiler-only costs.
Without `--execute`, the prior source-to-object timing path remains available.

Example (use a source-matched compiler binary):

```sh
python3 compiler/bench/incremental_native_server.py /path/to/elisac /path/to/results \
  --execute --functions 256 --samples 3 --concurrency 2 --batches 2
```

## Local evidence, 18 October 2026

The matched per-function-check run used a compiler built from an archive of committed
revision `7cf85dc0600967090b1a68e2a3ec6410d3994960`; unrelated worktree edits
were excluded. The source-archive SHA-256 is
`73bcc9e69fcc2434177254d145cddf23efdb6e79d84e8a041115e55269431003`, the
compiler SHA-256 is `cd291fef574eb45f373832a240ca4c25d01d3bcf8fa3926c4841c080d9c0843e`,
and the updated runner SHA-256 is
`42747d0de3f1f5f397133e9c4a0ffc2a29c88219217c2bd37f09de77b92e3fa4`. Its
`main` checks each unchanged function's exact result individually, then returns
the edited `work0` result for comparison with the expected process status. It uses
macOS arm64, O0, eight codegen workers, three alternating novel-edit pairs, and
two two-request concurrent batches. All 15 measured objects link and execute;
each program calls and checks all 256 generated functions. The process status
check for `work0` is modulo 256; values in this run are below 256.

| Metric | Fresh | Retained |
| --- | ---: | ---: |
| Median HTTP source-to-object | 388.5 ms | 322.0 ms |
| Median single edit through execution | 568.6 ms | 502.0 ms |
| Median two-request compile batch | 560.2 ms | 396.9 ms |
| Compile requests/second from batch median | 3.57 | 5.04 |

Cold retained compilation takes 570.4 ms, or 657.7 ms through execution. The
individual edit timings are fresh 378.1–394.2 ms and retained 317.9–322.6 ms.
These short shared-host samples demonstrate working retained edit-to-executable
behavior on this synthetic workload. They do not establish a stable
production-project speedup or attribute the difference to any single recent
optimization. General function-body reuse, real project qualification and the
full fast-incremental-compiler objective remain unfinished.

The earlier archived run used the same compiler snapshot but its `main` called
only `work0`; its timings remain historical compiler data, but it did not check
execution of every generated function. The updated run above supersedes those
execution-coverage claims. A separate negative-control compiler returns a
valid but stale native object: the first expected result passes, then the edited
result is rejected. This confirms the harness detects that concrete
stale-artifact failure rather than merely accepting linkable bytes. Python
syntax checks pass.

Updated results and server log are in
`build/verification/incremental-server-execute-per-function-20261018/`.
The source archive, compiler binary, original runner, negative-control
source/logs and historical run are in
`build/verification/incremental-server-native-execution-20261018/`.
