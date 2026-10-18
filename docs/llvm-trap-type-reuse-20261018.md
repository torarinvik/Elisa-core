# Reuse declared trap function types during LLVM lowering

Each `emitTrapUnreachable` previously allocated a new `semantic.FuncType` and
passed it to the pointer-keyed function-type lowering cache. Each trap site
therefore retained a different signature and cache entry despite using the same
module-local `llvm.trap` declaration. Overflow-heavy O0 bodies repeat this path.

The call now uses `LLVMGlobalGetValueType(trapFn)` after `ensureTrapFunction`.
The declaration belongs to the current LLVM module/context; no handles cross
compilations. Calls still use `buildCall` and terminate with `LLVMBuildUnreachable`.
Other checked arithmetic intrinsics already use this LLVM type accessor.

Verification:

- Seven existing overflow/dereference/watchdog tests: PASS 0.437s, log
  `/tmp/elisa-trap-type-tests.txt`.
- New 64-term checked-add IR regression: all 63 void trap calls, 63 unreachable
  terminators, one declaration; PASS 0.411s, log `/tmp/elisa-trap-type-parity.txt`.
- Existing signed-overflow debug/release IR checks and native edited-object
  execution: PASS 0.895s in the same parity log. The signed test checks IR only.
- New native execution test links an exported Elisa add function to a C driver
  supplying runtime operands. O0 `LLONG_MAX + 1` must signal SIGILL or SIGTRAP;
  O2 must wrap to LLONG_MIN; both must calculate `20 + 1` normally. PASS 0.697s,
  log `/tmp/elisa-trap-execution-exported.txt`.

The first native fixture lacked an export, so O2 removed it and linking failed.
The corrected test explicitly exports the C ABI symbol and retains the same
runtime assertions. No production change was needed for that fixture failure.

Performance qualification is pending. A pinned old test binary and candidate
source hashes are recorded under `build/verification/trap-type-reuse-20261018/`.
The intended comparison uses identical completed-cache body-edit requests, fixed
iteration counts, and reversed before/after order. This checkpoint is isolated;
no broad compiler speedup is claimed.

## Fixed-workload comparison

Three before/after pairs, reversing order in the middle pair, ran 20 novel
fixed-width body edits each on pinned binaries. Both paths enabled the completed
object cache, reran semantic analysis, emitted one changed shard and reused seven
per edit. All benchmark assertions passed; no other compiler/solver job was
observed before each arm. Host scheduling variation remains visible.

| Median per edited request | Before | After |
| --- | ---: | ---: |
| Whole request | 46.037 ms | 46.032 ms |
| LLVM IR build | 8.480 ms | 7.054 ms |
| Allocated bytes | 11,467,070 | 8,635,894 |
| Allocations | 57,227 | 53,037 |

Allocated volume fell 24.7% (2.83 MB/request), allocation count fell 7.3%, and
IR phase median fell 16.8%. Whole-request medians are effectively unchanged;
there is no demonstrated total latency gain. Individual before times ranged
45.411–51.728 ms and after 40.446–49.202 ms. This qualifies an allocation reduction
with neutral measured request throughput on the fixed overflow-heavy fixture,
not a project/compiler rebuild speed claim or a reduction in peak RSS.

Raw six-arm logs, comparison script and binary hashes are in
`build/verification/trap-type-reuse-20261018/comparison.json` and adjacent files.
Candidate source is `1706a8c0`; baseline binary was built at `e474122f`, with
no production backend changes between that baseline and candidate parent
`c70db789`. The candidate contains the single production trap-lowering change
plus focused tests/documentation. Shared Core integrated this gain by fast-forward to `74a9e3bf`. SHA256 checks
verified all seven unrelated dirty/untracked files were preserved byte-for-byte.
The repository hook retained the installed compiler. Raw comparison and test
logs plus `integration.json` were copied into the shared verification directory.
