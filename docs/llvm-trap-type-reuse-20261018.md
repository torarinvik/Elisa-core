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
