# Immutable constant types in pre-analysis view frames

Whole-body read-only frame inspection may run before expression types are recorded. A numeric conversion of an immutable module constant previously declined because the frame gate could not recover its operand type. The gate now carries the inspected function declaration and consults its exact declaration symbol to derive the owning namespace. Only immutable primitive constants qualify. Lexical bindings (including unknown local shadows) and authoritative expression types retain priority. A missing or mismatched declaration symbol declines without borrowing the caller namespace.

The extracted production annotation pair hash probe confirms its whole-body gate changes from false to true. This does not yet discharge the complete fold: the exact row/pair probe continues to report three loop invariants. There is no Stage1 or performance qualification from this change.

Focused controls cover pre-analysis numeric conversion, non-numeric and mutable constants, caller namespace collisions, unknown inspected declarations, local shadows, and existing view-frame proof rejection cases. See the saved verification log for the terminal result.

Verification: 14 top-level focused tests passed, no skips, package time 0.592 s. Raw evidence: `build/verification/view-frame-constant-types-20261018/`.
