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

No latency result has been measured for this implementation. Run paired,
interleaved fixed-iteration arms only after the shared exact-source compiler
gate is quiet, recording source and binary hashes. Cold-start/key setup cost and
changed-body behavior need separate measurements before broad performance claims.
The isolated branch is not yet promoted into shared main.
