# Model char-to-u64 conversion in callee contracts

`char` lowers to LLVM i64. A postfix value conversion to u64 preserves its
64-bit representation, so a negative char wraps rather than becoming an
unbounded mathematical integer. The SMT translator previously handled direct
view-byte widening only through a caller-only stable-view gate. Byte reads in
callee clauses could not use that gate, so otherwise valid recursive byte-fold
postconditions were left unproved.

The existing stable-view identity shortcut remains first. Other by-value char
postfix conversions to u64 now use the existing exact modular reinterpretation
helper. This does not infer an operand type from caller names, widen the
accepted cast origins, or reinterpret reference addresses.

Focused semantic checks pass: 32 top-level tests, no skips, 0.862s after build.
They include an addition-based recursive byte fold across a callee contract,
wrong-byte and different-view rejection, char -1 converting to u64.max, rejection
of a false zero result, and existing conversion/view/quantifier controls. Logs
are in `build/verification/smt-char-u64-callee-20261018/`.

The XOR/FNV prefix identity still declines with the default solver budget.
This checkpoint fixes the conversion model; it does not claim that the FNV loop
invariants, full Stage1 bootstrap, or compiler incremental latency are solved.
No standalone Stage0 build or benchmark was performed for this checkpoint.
