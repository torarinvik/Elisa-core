# Pointer-free return store-flow qualification

Base: `61b449f58e64e1f0bf637d5586e283ae134caa0f`.

The existing argument-store summary classifies scalar returns using AST builtin names and tuples. A nominal scalar aggregate such as `Ast::Pos` therefore returns its source expression's reference provenance. Recording that position can incorrectly suggest that local analysis state is stored into a longer-lived symbol table.

An instrumented private clone reproduced both `ga_functions` argument-4 lifetime diagnostics in the exact retained Stage1 source-check-16 tree. The first contributing summary is `ga_defaults`: a default expression read from state reaches a diagnostic through scalar position extraction. Permanent diagnostic-name copies are not the remaining cause; `ga_access_slot` has no state-to-table edge.

The independent actual-CLI reproduction at `/tmp/storeflow-pos-probe.elisa` copies `expr_pos(expr): Pos` into a diagnostic containing only that scalar position and a string literal. Its destination table has an unused optional state-reference field, so its type permits storing a state address. The CLI rejects passing the local state despite no such store. The identical literal-position control `/tmp/storeflow-pos-literal-control.elisa` passes; removing the table's optional reference field also passes.

The proposed correction checks the already-resolved callee return type. Only positively verified scalar aggregates lose argument provenance. Reference-bearing aggregates, views, containers, generic or unresolved types, recursive cycles, and packed enum backing handles retain it. Qualified names and aliases use the resolved type rather than a new namespace lookup.

Focused remote tests accepted scalar structs, qualified structs, aliases, and scalar enums. Nested-reference structs, reference-bearing enum variants, and reference aliases all produced the expected lifetime error. The complete semantic suite passed (60.267 seconds). The actual CLI accepted the scalar-position reproduction and rejected the corresponding reference-bearing position.

A separate transparent-wrapper omission was reproduced with a helper returning a literal `sview`: wrapping the call in inline `can Memory.Allocate` caused a false local-reference store error. `returnBorrowFlowForExprInner` now delegates `CanExpr` to its underlying expression, preserving provenance independently of effect handling. Focused tests accept the literal view and reject a returned view into a local darray. The combined complete semantic suite passed (62.583 seconds).

The rebuilt experimental `/tmp/elisacore-pointerfree-can` accepts the literal-view CLI and rejects the valid local-view CLI with the intended argument-store lifetime diagnostic. The evolving Stage1 source tree acquired many inline grants during this investigation. Its earlier and later diagnostic totals are not a matched source comparison; they cannot establish a source-admission improvement or regression for this patch. A frozen full source comparison is supplementary and does not replace focused negative tests. No performance claim is made.
