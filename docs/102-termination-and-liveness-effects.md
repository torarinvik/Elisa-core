# docs/102 — Termination & liveness: total correctness as a first-class, explicit property

Goal: extend Elisa's exactness from the **static** domain (types, values, machine contracts) to the
**dynamic** domain — *does this program make progress / terminate*. Today liveness is implicit and silent:
a loop that should terminate and a loop meant to run forever are typographically identical, and the
codebase discharges 61 of them with a single blanket `trusted Unsafe.AssumeProgress`. A silent non-
terminating loop is the purest ambiguity there is — exactly what Elisa exists to abolish. This doc makes
**termination a declared, checked property**, the way safety already is.

Motivating incident: a from-scratch-emulator boot wedged in a silent guest busy-spin that burned ~6 minutes
per diagnostic run before it could even be *located* (`elisa-shad-ps4-from-scratch`,
`verification_findings/gap3-reframe-guest-spin.md`). Refinements prove "if it returns, the answer is
right" (partial correctness). They say nothing about "it returns." This doc adds that half.

## STATUS (2026-06-23): most of this ALREADY EXISTS — it is DORMANT, not missing

A code audit after the first draft found that the trichotomy below is largely **already implemented**, under
different names. This doc is therefore an **activation plan**, not a greenfield design. The honest mapping:

| docs/102 concept | Already implemented as | Where |
|---|---|---|
| `decreasing <measure>` (proven terminating) | **`decreases <measure>`** — direct + mutual + structural recursion **and** `while`-loop variants; affine + SMT discharge; lexicographic tuples | `semantic/analyzer_termination.go` |
| `diverges` (intentional infinite) | **`trusted Unsafe.NonProgress`** (per-loop) | `semantic/progress_safety.go:74,131` |
| `Unsafe.UnprovenTermination` (propagatable give-up) | **`Unsafe.AssumeProgress`** (a normal, propagatable effect) and **`decreases * "reason"`** (mandatory-reason opt-out) | `analyzer_termination.go:41-53`, `progress_safety.go:124` |
| liveness as a tracked property | **a full `Progress` effect family** (`Tick/Yield/CheckCancel/Deadline/Budget`) + per-`while` `ProgressObligationLoop` + recursion-cycle obligations + `@main_thread` blocking protection (`Unsafe.BlockMain`) | `progress_safety.go`, `analyzer_builtins.go:33` |

So the prover, the trichotomy, the propagatable give-up effect, *and* the must-declare obligation are all
**built**. The reason none of it caught the crt0 spin and the 61 sites accumulated:

1. **The whole subsystem is OFF by default.** `enforceProgressSafety` is an opt-in build option
   (`analyzer.go:745`); when off, no obligation is checked at all.
2. **Even when on, the loop rule is a WARNING, not an error** (`progress_safety.go:74`).
3. **It is not wired into the strict-policy escalation** — `strictPolicy`/`perfStrict`/`concurrencyStrict`
   exist (`project_system…:197-199`) but there is **no `progressStrict`**.

`decreasing` is the `ensure` of termination — and it already exists as `decreases`. The work is not new
machinery; it is **activation + escalation + vocabulary**.

## The trichotomy: every loop/recursion declares its intent

A loop or recursive call the verifier cannot *prove* terminating must pick exactly one of three. An
unannotated, unprovable loop is a **compile error** — you must state intent. This is liveness as a
must-declare property, mirroring how safety is must-declare.

### 1. Proven terminating — `decreasing <measure>`  *(default; inferred where possible)*

```elisa
while lo < hi:
    decreasing hi - lo          # measure: a value in a well-founded order
    ...
```

The verifier proves the measure (a) **strictly decreases** on every back-edge / recursive call and (b) is
**bounded below** (well-founded — naturals, structural size, or a lexicographic tuple). The check is
usually affine (`m - 1 < m`); nonlinear measures escalate to the SMT tier. `decreasing` is a leading loop
statement, consistent with `requires`/`ensure` placement.

**Inference is mandatory for adoption.** Counted loops (`for i in 0 ..< n`), structural recursion over a
shrinking container, and `while x > 0: x <- x - k` have an obvious measure the verifier infers with **no
annotation**. `decreasing` is required *only* when inference fails. Otherwise 61 sites become 61
annotations and nobody adopts it.

### 2. Intentionally infinite — `diverges`  *(declared, propagated, NOT unsafe)*

```elisa
def dispatch_loop() -> never diverges:
    while true: ...
```

A correct event loop / scheduler / the guest-exec dispatch loop runs forever *by design* and is perfectly
**safe** — no soundness hole. So non-termination is a **control-flow property** (like Rust's `!`/`never`,
or EASM `control: noreturn`), **not** an `Unsafe.*` effect. Tagging it `Unsafe` would re-pollute the
soundness family with non-soundness noise — the same mistake we declined when we rejected an `Asm.*` family
(docs/101 §4). `diverges` propagates as a control-flow fact: a caller past a `diverges` call is itself
non-returning. The point is only that non-termination must be **requested**, never silent.

### 3. Terminates, unprovable — `Unsafe.UnprovenTermination`  *(a normal, propagated effect)*

```elisa
def wait_guest_ready() -> void can Unsafe.UnprovenTermination:
    while not guest_flag():        # bounded by something the verifier cannot see
        ...
```

The honest residual: a loop that *does* terminate for a reason outside the verifier's view (a guest-set
flag, a hardware timeout, a Collatz-shaped measure). This is the **only** case that stays `Unsafe`, because
it is the only one where you are actually *opting out of a guarantee*.

Crucially, it is an **ordinary effect**: by default it **propagates via `can`**, so the non-termination
risk is visible in every signature up the call graph and auditable at every call site — exactly as
`Unsafe.SegmentMutation` shows "this touches the segment." You use `trusted Unsafe.UnprovenTermination`
**only where tracking is undesirable** — i.e. at the boundary where a real external bound is established and
propagating further gives callers no actionable leverage:

```elisa
def run_boot_under_watchdog() -> s32:
    trusted Unsafe.UnprovenTermination("bounded by run_with_stall_guard.sh wall-clock watchdog"):
        wait_guest_ready()         # the trust is justified BECAUSE the guard exists
```

The `trusted` boundary is therefore **co-located with the actual liveness guard** — the effect discipline
and the guard-rail program (the stall guard, docs in the emulator repo) converge on the same point.

## `trusted` discipline (the audit)

`trusted` is where exactness deliberately stops — an axiom. It is justified **iff** (a) the effect is
genuinely discharged (a real external bound, or a safe-interface encapsulation) **and** (b) propagating
further is pointless. A lint enforces this, targeting the patterns measured in the emulator (334 `trusted`
vs 36k `can`; concentrated on the least-checkable effects):

1. **Clusters** — N `trusted SameEffect` across N sites ⇒ a *missing safe abstraction*. (98 `trusted
   Unsafe.PointerCast` should be **one** audited `GuestPtr.to_host()` + 97 safe calls — the same collapse
   as the EASM boundary-thunk template.)
2. **Trusting a provable property** — `trusted X` where the discharge ladder *could* prove X ⇒ "prove it,
   don't vouch." (Targets `Unsafe.StaleRef`, much of which the storage-view chokepoint already proves.)
3. **Required reason** — every `trusted` carries a one-line justification; an un-justified axiom is the
   drift-prone confidence we are abolishing.
4. **Liveness-debt register** — every surviving `can`/`trusted Unsafe.UnprovenTermination` is an un-met
   progress contract, listed so the debt is explicit, not invisible.

## Migration: reclassify the 61 `trusted Unsafe.AssumeProgress`

`Unsafe.AssumeProgress` is **removed**; each site becomes one of the three intents. Expected distribution
and payoff:

- **Most → inferred `decreasing`** (counted/structural loops; no annotation).
- **A few → `diverges`** (the real event/dispatch loops).
- **Residual → `can Unsafe.UnprovenTermination`** propagating to a small number of audited `trusted`
  boundaries.
- **Bug-finder:** any loop that *cannot* prove `decreasing` and is *not* intentionally infinite is a latent
  spin — the exact crt0 class — surfaced **at compile time**. The relocation/init-walk loops are the first
  audit targets.

## Staging (ACTIVATION, not construction)

The machinery exists; the work is turning it on and making it bite, staged to manage blast radius.

1. **Wire `progressStrict` into the strict policy** — *DONE.* Added `progressStrict`/`-Wprogress` as a
   dedicated dial alongside `perfStrict`/`concurrencyStrict` (both CLI parsers + the three option structs),
   wired `EnforceProgressSafety := progressStrict || strictPolicy` (`emit_runner.go:524`). It rolls up under
   `-Wstrict` (existing behavior preserved) and is settable alone (`TestParseArgsProgressDialIsIsolated`).
   Dormant by default; nothing fires until `-Wprogress`/`-Wstrict` is set.
1b. **`decreases` now discharges the progress obligation** — *DONE.* Running `-Wprogress` empirically (a
   local binary, tiny fixtures) surfaced a real gap: the termination prover and the progress-safety checker
   were *separate*, so a loop with a proven `decreases <measure>` was still warned "no progress evidence."
   A proven-terminating loop is the *strongest* progress evidence, so it now discharges the obligation
   (`progress_safety.go`, `TestProgressSafetyAllowsLoopWithDecreasesMeasure`). This is a prerequisite for
   escalation — otherwise every correctly-annotated `decreases` loop would falsely fail. Also confirmed the
   correct idiom: `trusted Unsafe.NonProgress` must **wrap** the loop (inside the body it's firewalled).
2. **Escalate the loop/recursion obligation to an error under full `-Wstrict`** — *DONE.* Warning under
   `-Wprogress` (observe), hard **error** under `-Wstrict` (`enforceUnsafePermissions`), for both the loop
   and recursion-cycle obligations (`progress_safety.go`). Tests: `…ErrorsUnderStrict`. This is the
   must-declare rule made real — the change that catches a silent-spin class at compile time.
2b. **Prefer `can` (propagate) over `trusted` (firewall)** — *DONE.* The diagnostics now guide toward
   `can Unsafe.AssumeProgress` / `can Unsafe.NonProgress` (which *propagate* the unsafe assumption up the
   call graph and discharge the obligation), reserving `trusted` for the boundary where propagating no
   longer helps. Confirmed `can Unsafe.AssumeProgress` both discharges and propagates to the signature
   (`…CanAssumeProgressPropagates`). The 61-site `trusted`→`can` migration follows this discipline.
3. **Measure inference for the counting-loop shape** — *DONE.* `isCountingLoop` (`progress_safety.go`)
   discharges `while v </<=/>/>= bound:` with a monotone `v ± c` step (positive int literal) moving toward
   the bound, v/bound not otherwise reassigned. Conservative — a wrong discharge only drops a warning,
   never soundness (explicit `decreases` is the sound `-Wstrict` fallback). Tests:
   `…InfersCountingLoop`, `…KeepsWrongDirectionLoop`. **Measured impact on the emulator: 248 → 88 progress
   warnings (65% auto-discharged with zero annotations).** The remaining 88 are the genuinely-interesting
   loops — bounded by something the compiler can't see — and are the real migration target.
4. **Vocabulary decision (clarity vs churn)** — the existing `AssumeProgress`/`NonProgress` names work but
   blur "proven" vs "assumed." Optionally alias to `UnprovenTermination`/`diverges` for the docs/102
   reading; weigh against renaming churn across the registry + the 61 sites.
5. **`trusted` audit** — *AUDIT DONE; automated lint remains.* 5 parallel agents reviewed the 238
   real-source `trusted` sites: **~150 converted to `can`** (propagate the unsafe effect up the call
   graph), **~58 kept** at genuine leaf/boundary primitives (guest↔host pointer translation, `*_from_handle`
   converters, bounds-checked `buf_read_*`, the errno singleton, FFI bridges, FsGuard-gated guest-entry,
   test roots). Verified clean (0 errors, 0 progress warnings, full semantic suite green). The audit
   surfaced 7 **scatter clusters** (same effect firewalled N times = a missing shared primitive) — recorded
   in the emulator's `verification_findings/trusted-audit-scatter-clusters.md` (the `GuestHostPointerCast`
   boundary wrappers → one `boundary_cast`, `*_from_handle` → `handle_as[T]`, the `StaleRef`
   store-borrowed-handle pattern, arena-cstr, cxa-guard, etc.). The *automated* lint (flag clusters /
   provable-property / required-reason continuously) is the remaining piece.

   **Compiler fix found by this migration:** a *wrapping* `can Unsafe.NonProgress`/`AssumeProgress` did not
   discharge a loop's progress obligation (only `trusted` did, via a `trusted`-named depth counter) — so
   converting a wrapping `trusted`→`can` silently re-flagged the loop. Fixed by adding granted-depth
   counters that `can` increments (mirroring the existing `can Unsafe.StaleRef` granted-depth), so `can`
   now discharges everywhere `trusted` does *and* propagates. Test: `…WrappingCanDischargesLoop`. This makes
   the "can everywhere trusted works" discipline actually hold.
6. **Emulator migration** — *DONE.* `-Wprogress` flagged **88** un-storied loops; counting-loop inference
   auto-resolved 160 of the original 248, and **5 parallel agents** annotated the remaining 88: mostly
   `decreases` (prove), `can Unsafe.AssumeProgress` / `can Unsafe.NonProgress` (propagate) for the
   genuinely-unprovable (spinlocks, condvar waits, LEB128/C-string scans, the exit sink), and **zero new
   `trusted`** for anything propagatable — the discipline held. Central verification: **88 → 0 progress
   warnings, 0 errors.** The emulator now passes `-Wprogress` clean; under `-Wstrict` the obligation is a
   hard error, so no new un-storied loop can be added. Follow-up (non-blocking): some `decreases` measures
   the static prover can't fully model degrade to a runtime check (informational warnings) — either
   strengthen the prover or convert those to `can Unsafe.AssumeProgress`.

## Non-goals

- **Tagging intentional infinite loops `Unsafe`** — they are safe-by-design; use `diverges`.
- **Forcing explicit measures everywhere** — inference handles the common cases; explicit `decreasing` is
  for where inference fails.
- **Proving liveness of concurrent/external systems** — `decreasing` is intraprocedural total correctness;
  cross-thread/external progress stays in the propagated-effect + external-guard regime.
