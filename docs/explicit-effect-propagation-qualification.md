# Explicit effect propagation correction

Base: clean61b449f58e64e1f0bf637d5586e283ae134caa0f. Work is isolated in
codex/explicit-effect-propagation; the qualified predecessor is unchanged.

A bounded remote regression reproduced directory-dependent loss of explicit
tracked Unsafe effects: under EnforceUnsafePermissions, the ordinary source
retained can Unsafe.PointerCast and an unrelated nested can Unsafe.MutableGlobal,
but identical elisacore_std source erased both. Both inference filters are removed.
Only explicitly named trusted scopes suppress effects. The same-member nested-can
and unrelated-member tests verify selective suppression remains intact.

The remaining runtime-directory Unsafe local-grant exemptions are also removed.
A strict pointer-forging regression now rejects the same ungranted operation in
ordinary and runtime sources. Source identity remains available for specialized
runtime surface restrictions; it does not grant authority or erase inferred rows.

Protocol effect conformance now runs after permission inference. Before the fix,
an actual CLI fixture with a pure Readable protocol and a headerless method using
local can Global.Read was accepted despite its inferred Global.Read row. The
regression rejects that impl under a pure protocol and accepts it under an explicit
protocol Global.Read bound. Concrete method headers remain optional; bodyless
protocol declarations retain declared effect bounds. Signature contracts do not
authorize a function body. A regression preserves the required body-local grant.
Grouped Global{Read,Write} and expanded member syntax have identical semantics and
stable formatter round trips.

All qualification is on the isolated Vast source tree. The targeted regressions
pass; the complete semantic suite passes in60.592s with GOMAXPROCS2, go-p1 and
parallel2. Logs: /tmp/propagation-semantic2.log on Vast.

Runtime migration is a separate unfinished follow-up. Converting the71 prior
convenience Global suppression scopes to tracked can exposed101 actual transitive
call sites; adding narrow call grants made default runtime admission pass before
the subsequent protocol/Unsafe-validation changes. Strict admission without the
Unsafe directory exemption reports1420 missing-grant diagnostics:821 errors and
599 call warnings. Single-member categories are403 PointerCast,208 MutableGlobal,
165 UncheckedIndex,147 RawExtern,120 Alias and26 PointerArithmetic;351 diagnostics
have mixed rows. This evidence precedes deliberate operation-level Unsafe boundary
migration. No blanket trusted runtime grant is introduced.

The separate source-presentation follow-up uses adjacent family-member sugar in
canonical formatting and user-facing grant suggestions, for example
`can[Global{Read,Write}, Unsafe.PointerCast]`. Internal permission/fact row
serialization is unchanged. Whole-family refs, singleton members, nonadjacent
source order, type specializations and `via` clauses remain distinct; duplicate
members are preserved. Exact-output and parse/format round-trip regressions pass,
as do targeted Global diagnostics and the complete parser suite (3.621 s).
Logs: /tmp/grouped-presentation-tests2.log and /tmp/grouped-parser-full.log on Vast.
