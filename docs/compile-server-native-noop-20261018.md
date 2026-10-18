# Compile-server completed native artifact cache

The incremental HTTP object path previously rebuilt LLVM IR and prepared all
partitions even when the native shard cache reused every partition. The new
process-local cache returns a completed base64 object after fresh semantic
analysis for an exact unchanged raw-source request. Body edits still use the
existing native partition cache. This is one iteration-speed improvement; it
does not implement dependency-directed semantic compilation.

## Admission and identity

The first supported path requires explicit filenames, incremental object mode,
raw source matching the loaded snapshot, and the constructor-owned native
session. Project/include file authorities, EASM, prebuilt AST/IR requests,
unknown compiler/LLVM/linker switches and non-system linker selection decline.
The frontend-only deprecated-warning suppression flag is allowed, remains keyed,
and diagnostics are recomputed on every request.

Identity covers exact source, all request fields, working directory and absolute
unit path, the existing compiler/target/clang build-artifact key, original merger
arguments, resolved merger executable contents, and the sorted exact environment.
Malformed UTF-8 configuration declines instead of JSON normalizing it. Hash
buckets compare exact source and configuration bytes, including under collisions.
The complete environment is retained in memory as part of that exact comparison;
entries must not be logged or persisted. Compiler and loaded LLVM code belong to
the same process lifetime; this cache imports no artifacts from other processes.

Only successful builds publish, after identity is recomputed and matches the
pre-build snapshot. Failed semantic/native requests never populate the cache.
The cache retains owned source/configuration/object strings, no AST or LLVM
handles. Mutex-protected LRU bounds are eight entries and 128 MiB of accounted
payload plus estimated entry overhead; this is not a whole-process RSS limit.
Unsupported requests retain the existing compilation path.

Frontend/API diagnostics are fresh on hits. Successful merger subprocess output
was already discarded by the ordinary backend, and this change does not promise
capture or replay of process-global LLVM stderr.

## Verification

Checkpoint: `20e8ac87`; current committed Core `f2957e93` merged as `f0d40520`.
Focused native-cache correctness passed without skips (1.188s before merge).
After merge the race suite passed without skips (8.039s):

```sh
GOMAXPROCS=1 GOMEMLIMIT=256MiB go -C compiler test -race ./src \
  -run 'TestServerNativeCache|TestCompileServerNativeArtifact|TestCompileServer.*NativeObjects|TestCompileServerAnonymous' \
  -count=1 -v
```

Coverage includes exact bytes, fresh warning/error checks, body-edit execution,
configuration and input-authority declines, deliberate digest collisions, LRU
and byte limits, four warmed concurrent requests for one identity, four distinct
identities, and unchanged native counters on completed-artifact hits.
The local log is `/tmp/elisa-native-cache-current-race.txt`.

## Performance qualification pending

`BenchmarkCompileServerNativeNoop` compares the full in-process request path:
`shards` disables only the completed-object cache; `completed` enables it. Both
use fresh semantic analysis with the same existing semantic/native sessions.
The fixed 32-function source does not grow with benchmark calibration. Two
warmups precede timing; identity preparation remains inside timed requests.
Assertions require one semantic analysis per request, exact output/diagnostics,
eight reused native shards per baseline request, and no native builds on
completed-object hits. Allocation and backend IR/native phase metrics accompany
total request latency. This is not network round-trip or cold-start latency.

A two-iteration correctness smoke passed on Darwin/arm64 Apple M5 with
`GOMAXPROCS=1 GOMEMLIMIT=256MiB`, after no compiler/solver processes were observed.
Log: `/tmp/elisa-native-noop-bench-smoke.txt`. It reported 42.270 ms/request,
9,648,784 B/request and 56,099 allocations for shards versus 8.270 ms/request,
6,247,512 B/request and 32,795 allocations for completed objects. Both arms passed
the analysis/output/native-counter invariants. These sequential two-iteration
samples are exploratory, not qualified performance evidence.

Ordinary subbenchmark calibration runs arms sequentially with independent N.
For qualification, select each arm separately on one pinned test executable with
an explicit fixed iteration count, alternate/reverse order across repeated pairs,
and record source/binary hashes. Run only after the shared exact-source compiler
gate is quiet. Cold-start/key setup cost and
changed-body behavior need separate measurements before broad performance claims.
Shared main integration is recorded below.

## Paired warm-request result

Three fixed ten-request pairs on one pinned test executable, in
shards/completed, completed/shards, shards/completed order, passed every output,
diagnostic, analysis-count and native-counter assertion. Each arm independently
prewarmed twice. Process inventory was checked before every arm and contained no
other compiler/solver/test process matched by the harness. This guards ordinary
contention, not system-wide scheduling noise.

| Median per request | Shard reuse | Completed object |
| --- | ---: | ---: |
| Whole request | 46.370 ms | 9.678 ms |
| Allocation volume | 9,639,017 B | 6,241,992 B |
| Allocations | 56,067 | 32,785 |
| Backend IR build | 9.799 ms | 0 |

For this fixed 32-function unchanged-source fixture, median latency improved
4.79x (79.1% lower); allocation volume fell 35.2%. Individual shard samples were
38.707–46.403 ms and completed-object samples 9.533–10.213 ms. Key preparation,
parsing, fresh semantic analysis and response construction are all included.
This does not establish changed-body, project-scale, network, or cold-start gains.

Raw logs, the harness and manifest are retained locally under
`build/verification/server-native-noop-20261018/`. Reproduce with Homebrew Python;
Apple's `/usr/bin/python3` injected SDKROOT and correctly caused admission to
skip. Those skipped attempts were rejected by the harness and are excluded.
The successful run removed no environment inputs. Tool resource settings were
GOMAXPROCS=1 and GOMEMLIMIT=256MiB.

Measured source commit: `b55aa41f7dd35c6d22b16671b3ab21c92ea778c6`.
Pinned test binary SHA256: `8076dad3a803ef2f27ebcad0d7a907ffe8fa0df3b45027df5f0389afa5bf9c25`.
Cache source SHA256: `5d3ec50d71493ffaead6ae1182af836f8e8306ce2dd7a1291ec184e12220bdc5`.
Benchmark source SHA256: `23f491e8f63b9f62fcbae51dbcbfc6b4a5dbe4e3f142ceb44aadd2a073dd8daf`.

Integrated into shared main by fast-forward to `334d1866`. SHA256 checks verified all five unrelated dirty/untracked files were preserved byte-for-byte; the installed compiler was retained by the repository hook. Integration metadata is in the local evidence directory. Shared-tree focused correctness validation is tracked separately from the isolated performance sample.

## Novel body-edit control (prepared)

`BenchmarkCompileServerNativeBodyEdit` uses a fixed 16-digit hexadecimal literal
and changes only that literal on each timed request. Both arms retain semantic
and native shard sessions; only the completed-object cache differs. Two warmups
precede timing. Assertions require one fresh analysis and native build per edit,
one emitted shard and seven reused shards per edit, changed nonempty object
bytes, and no completed-object hits. The generated source formatting cost is
included equally in both arms. This benchmark has been prepared but not run;
there is no changed-body latency or overhead claim yet.

## Environment retention hardening (prepared, v2)

The isolated v2 follow-up fingerprints the sorted, complete JSON environment with
SHA-256 before embedding it in retained configuration. It drops no environment
input, but avoids retaining plaintext credential copies for the LRU lifetime.
Environment collision resistance now uses SHA-256, like the existing compiler
and tool content identities; exact comparison remains for source/configuration
and outer hash buckets. A regression checks credential text is absent and a
credential change changes identity. This follow-up is not yet tested or promoted;
the measured and integrated implementation above remains v1.

Shared-tree focused tests completed successfully (3.369s, no skips), log
`/tmp/elisa-native-cache-shared.txt`. This ran with concurrent owners' dirty
parser/StoreFlow sources, and is supplemental integration evidence, not a new
exact-source performance qualification. The isolated v2/body-edit validation
remains pending while other compiler/Z3 processes are active.

The reusable `compiler/scripts/benchmark_compile_server_native.py` harness takes
an existing `--binary`, a fresh `--output` directory, `--benchmark Noop|BodyEdit`,
`--iterations`, and `--pairs`. It reverses arm order each pair, rejects skipped
or failed runs, records source/binary/environment fingerprints and per-arm logs,
and checks source/binary stability afterward. It does not build anything.
The harness CLI parses successfully; its complete paired execution has not yet
been validated. Avoid concurrent compiler work when using it.
