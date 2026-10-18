# Incremental HTTP compilation through native execution

The opt-in `compiler/bench/incremental_native_server.py --execute` mode now
links and executes every measured object. The generated program returns a value
that changes with each novel body edit. Both fresh and retained compilations
must return the independently computed expected exit status. A nonempty object
is no longer enough to qualify these execution-enabled runs.

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

The compiler was built from an archive of committed revision
`7cf85dc0600967090b1a68e2a3ec6410d3994960`; unrelated worktree edits were excluded.
The run stores the source-archive hash, compiler/script hashes and exact binary.
It uses macOS arm64, O0, eight codegen workers, three alternating novel-edit
pairs, and two two-request concurrent batches. All 15 measured artifacts pass
linking and expected-result checks.

| Metric | Fresh | Retained |
| --- | ---: | ---: |
| Median HTTP source-to-object | 1112.9 ms | 735.7 ms |
| Median single edit through execution | 1303.4 ms | 927.4 ms |
| Median two-request compile batch | 820.8 ms | 393.9 ms |
| Compile requests/second from batch median | 2.44 | 5.08 |

Cold retained compilation takes 1912.5 ms, or 2101.2 ms through execution. The
individual edit timings vary considerably (fresh compile 575.8–1217.7 ms,
retained 385.5–873.4 ms). These short shared-host samples demonstrate working
retained edit-to-executable behavior on this synthetic workload. They do not
establish a stable production-project speedup or attribute the difference to
any single recent optimization. General function-body reuse, real project
qualification and the full fast-incremental-compiler objective remain unfinished.

An eight-function smoke run also passes. A separate negative-control compiler
returns a valid but stale native object: the first expected result passes, then
the edited result is rejected (actual 80, expected 82). Thus the harness detects
at least this concrete stale-artifact failure rather than merely accepting
linkable bytes. Python syntax checks pass.

Exact results, source archive, compiler binary, runner and negative-control
source/logs are retained in
`build/verification/incremental-server-native-execution-20261018/`.
