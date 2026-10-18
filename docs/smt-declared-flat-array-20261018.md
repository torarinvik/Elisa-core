# Stable container identities with declared flat arrays

A ghost-call identity can mark a darray base as a stable container even when that same symbol is already declared as an Int-valued SMT array. The callee array binding previously rejected every stable-container base, suppressing element reads in otherwise modelable recursive step contracts. Trace evidence showed `v_lines` and `v_method_refined` rejected with `lengthOnly=false, stable=true`.

The binding now permits a stable-container term only when its exact emitted SMT name is present in the query's flat-array declarations. Prefix extraction, map lookup and an exact `smtVar(name) == term` check avoid scanning all declarations. Opaque container identities without a flat-array declaration still decline; length-only bindings always decline. No new array is fabricated and no new equality between separate representations is assumed.

The current self-hosted `assoc_index_bound_marker.elisa` extraction (SHA-256 `96d234db22e689fd0578b10d18ff8e6e0e6af9492f599dc6e78a1385b25f8443`) proves without any source changes. Complete early-return candidates for that helper and associated-index membership also prove with explicit checked recursive-step contracts. The original membership helper still has an unproven invariant due to its body shape; it is not qualified by this fix. No shared source is modified and no Stage1 or incremental speed claim follows.

Permanent controls check exact declared identity, opaque/length-only rejection, partial-name rejection and unbound callee roots. Complete lookup fixtures must prove, and incorrect hit results must be rejected. The early-return source candidates are retained as fixtures for coordinated production application, not silently installed into the self-hosted tree.

Verification: 41 top-level focused tests passed with no skips, package time 12.850 seconds. Raw suite log: `build/verification/declared-flat-array-20261018/regressions.txt`. The complete membership candidate's structural source-bound gate also passes in memory; no shared files were written. The coordinated two-file candidate is retained at `membership-early-return.patch` in that evidence directory. Independent proof-product replay has not run.

Independent static review found no unsound identity/sort match: the exact flat-array declaration is required, length-only remains an unconditional decline, and record/view container projection theories do not replace the integer/bool array element theory. The reviewer inspected the guard and controls but did not rerun tests.
