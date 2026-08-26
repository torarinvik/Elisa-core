# stage1 field-chain resolution was exponential — blocked the internal differential

## Status: FIXED. Linear now. Was not caused by this session's work, only made reachable by it.

## The fix

`src/semantic/resolve_types_infer.elisa`, the `Expr.Field` arm of
`infer_expression_type`.

The arm walked `object` TWICE: once via `infer_expression_type` at the top, and
again via `structural_type_id_of` further down (which recurses as well). The
second walk is only reached when the first did NOT resolve to a Named struct —
so a chain that resolves costs one walk per link, and a chain that FAILS costs
two, i.e. 2^n.

That is exactly why `struct Node: b: Node&` was fine and `a: i64` was not.

A prefix that is itself an unresolvable field access cannot resolve structurally
either, so the second walk is now skipped for that case alone. Field access
through a reference parameter is an `Ident`, not a `Field`, so it keeps the
structural path and its exact type.

## Measured, before and after

    links      before        after
       15       18 ms       272 ms*
       20       66 ms        13 ms
       25     1715 ms        13 ms
       30     ~25 s          13 ms
       60      --            14 ms
      200      --            15 ms
      400   ~2e91 s          20 ms

    (* first run, includes process/page-in warmup; steady state is ~13 ms)

FLAT, not just faster — the growth is gone, not reduced.

The oracle's own `chain.elisa` (400 links) through `parse_report`:
never finished  ->  **326 ms, rc=0**.

`test/parity/semantic_internal_diff.sh` never completes. It is NOT the
"multi-hour" check its header advertises being slow -- it is stuck on one row.

## The measurement

stage1 `-emit obj` on `def f(a) -> ...: return a` + N x `.b`:

    links   time        links   time
        5     21 ms        20      66 ms
       10     16 ms        22     208 ms
       15     18 ms        25    1715 ms
       18     29 ms

~1.75x per additional link -- exponential. The oracle's `chain.elisa` (row 726)
has 400 links, extrapolating to ~2e91 seconds. stage0 rejects the same file with
rc=1 essentially instantly.

Reduction: repro/nw_stage1_field_chain_exponential.elisa

## How this was found, including two wrong turns worth not repeating

1. Observed `build/parse_report` at 94 min CPU, 100%, RSS FLAT at 1.3 MB, flat
   memcmp-heavy stack. Concluded "spinning on one specific input". That was
   RIGHT, but it was an inference, not a measurement.

2. Swept all 3270 oracle rows under `timeout 5` and found ZERO hangs, then
   reported the hypothesis disproved. THE HARNESS WAS BROKEN:

       if ! timeout 5 "$RPT" < src; then
           rc=$?            # $? here is the status of `! timeout` -- always 0

   `$?` immediately inside the then-branch of `if ! cmd` is the status of the
   NEGATION (0), never 124. The detector could not fire. Verified afterwards:
   `if ! timeout 1 sleep 5; then echo $?; fi` prints 0.

   So a broken test was used to overturn a correct diagnosis. Any sweep that
   reports "zero failures found" should be sanity-checked against a KNOWN
   failure before its silence is believed.

3. What actually worked: trace the real script. A copy that logs each row before
   invoking, plus a watcher for a stalled row counter, named row 726 in minutes.

## Why it is not this session's doing

The failing path is field-chain resolution. None of the const-folding, string
escape, extern-signature, target-predefine or -O1 changes touch it, and the
blowup reproduces at 25 links in a file containing no consts, no escapes and no
externs.

It became REACHABLE only because fixing the stage0 `mutable sview&` crash
(SESSION_COMPILER_FIXES.md #3) let `parse_report` build at all. Before that, the
check died at tool-build time and never executed a row -- so the gate had been
GREEN on this check by never running it.

## The trigger is ERROR RECOVERY, not field access

Refined after the first reduction FAILED to reproduce -- worth recording, because
it moves where to look.

A chain on a type that HAS the field is fine: `struct Node: b: Node&` with 25
links compiles in 2.2s on stage1 vs 1.5s on stage0. A 1.5x difference, no blowup.

The pathological case is a chain on a type with NO such field. `a: i64` and i64
has no field `b`, so every one of the 25 links fails to resolve:

    stage1 1611 ms   vs   stage0 759 ms      (25 links, rc=1 both)

and the scaling table above is measured on that form. The oracle's chain.elisa is
this shape with 400 links.

## Where to look

The UNRESOLVED-field recovery path in stage1's semantic layer, not field
resolution generally. Suspect a recovery that, on failing to resolve `.b`,
re-explores the whole prefix expression (each link doubling the work), rather
than resolving to an error type once and propagating it. Resolving a failed field
access to a poisoned/error type that absorbs further accesses is the usual fix
and would collapse this to linear.

## Impact on the gate

Everything else is green: 187 ok / 1 FAIL, and that FAIL (project_report_smoke)
is now fixed and confirmed at 91 passed / 0 failed with a fresh seed. This check
is the only outstanding one, and it should be treated as RED-BY-TIMEOUT rather
than slow.
