# Preserve XOR congruence in the fast SMT abstraction

The earlier bit-vector abstraction replaced each distinct read-back by a fresh
integer constant. Different terms then lost congruence even when their inputs
were proved equal. The recursive annotation-prefix one-byte identity could not
prove under the default budget after char conversion itself was modeled.

For the exact generated shape `bv2nat(bvxor(int2bv_W(a), int2bv_W(b)))`, matching
widths 1–64 now select one uninterpreted `Int × Int → Int` function per width.
The actual XOR operation is an interpretation of that function, so this remains
a weakening: UNSAT proves the original query; SAT/unknown goes to existing
fallback. No XOR identities or collision assumptions are asserted. Observed
applications get unsigned range bounds after the original declarations.

Quantified applications keep their input dependence inside the binder and omit
hoisted range facts. Unsupported reads in quantified queries decline abstraction
instead of replacing binder-dependent terms by a constant. Other unsupported,
nested, malformed and mixed-width shapes retain the previous fallback behavior.

Forty focused top-level semantic tests pass without skips (2.293s after build):
congruence and unrelated-input controls, quantifier scope and false identity,
malformed/unsupported forms, existing mask bounds and bit-vector budget checks,
character conversions, view/cast controls, and a recursive XOR byte-prefix step
with wrong-byte and different-view rejection.

The full production annotation row/pair strict probe still reports its three
loop invariants. This checkpoint proves the single-step identity, not the loops,
full Stage1 bootstrap, or a compiler throughput improvement. Source-matched
Stage1 verification remains required. Evidence is under
`build/verification/xor-congruence-20261018/`.
