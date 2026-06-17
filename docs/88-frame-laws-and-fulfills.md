# 88 — Frame laws, `fulfills`, and discharge classes

Status: **design + brick 1.** Implements docs/85 §4 (discharge classes) and §13 (the
`law MovesPlayerOnly: changes …` + `fulfills` forms), completing docs/87 Stage 3 brick 87-4.
This is the first **non-value** law class and the gateway to Stages 4-5 (shape/measure laws).

## 1. Discharge classes (docs/85 §4)

Every law has a class fixing *how* it is discharged, so a uniform `law`/`is` surface never
implies uniform reliability. Today every law is implicitly **value** (a pure bool predicate,
fact-lattice discharge). This brick adds **frame** (`changes`/`preserves`, mutation-analysis
discharge). effect/shape/measure are later stages.

The class is **derived from the law's body shape** (no new keyword):

- **value law** — `law Positive(self: i64) = self > 0` (an `= <bool-expr>` body).
- **frame law** — `law MovesPlayerOnly(self: Render&) changes self.px, self.py` (a `changes`
  / `preserves` clause, no `=` body). The subject param names the frame's root.

A law may not be both (a frame clause *and* an `=` body) — that's a malformed law.

## 2. Frame laws

A frame law packages a reusable `changes`/`preserves` set, rooted at its subject param:

```elisa
law MovesPlayerOnly(self: Render&) changes self.px, self.py
```

It declares no behavior — it is a *named frame* to be applied with `fulfills`. The subject
must be a reference param (only ref state is caller-visible); the paths are validated exactly
like a function's own frame clause (docs/87 `resolveFramePaths`).

## 3. `fulfills` — applying a frame law to a function

A function declares it satisfies a frame law via `fulfills <param> is <Law>` (the same UFCS
binding as `is` everywhere: the law's subject binds to the named param):

```elisa
def clip_move(r: mutable Render&, dx: i32) fulfills r is MovesPlayerOnly:
    r.px <- r.px + dx        # ok
    # r.health <- 0          # ERROR: outside the frame MovesPlayerOnly grants on r
```

**`fulfills` desugars into the existing frame machinery (docs/87):** it rebinds the law's
paths from `self` to the named param (`self.px` → `r.px`) and *adds them to the function's
`changes` / `preserves` sets*. Enforcement is then exactly brick 87-1/87-2 — no new checker.
A function may combine its own `changes`/`preserves` clauses with one or more `fulfills`.

This is the §13 headline: a stray `r.health <- …` under `fulfills r is MovesPlayerOnly` is a
compile error, and the frame is named/reusable across functions.

## 4. What brick 1 covers / defers

**Covered:** discharge-class derivation; frame-law decls (parse + validate); `fulfills <param>
is <FrameLaw>` parsed, resolved, and expanded into the function's frame; the malformed-law and
wrong-class diagnostics (`fulfills` a value law is an error; a value `is` of a frame law is an
error).

**Deferred:**
- **`fulfills` discharge as an obligation across calls** — a caller relying on a callee's
  declared frame (the interprocedural consumption, docs/87 87-3).
- **Generic frame laws / multi-subject frames.**
- **effect/shape/measure classes** (docs/85 Stages 4-5) — the class enum is shaped to grow
  into them, but only value+frame exist now.

## 5. Soundness

`fulfills` only *expands* the function's declared frame; the enforcement (87-1/87-2) is
unchanged and already sound (catches all caller-visible write channels). A frame law is pure
declaration — it has no body to run. The wrong-class guards keep value and frame `is`
non-interchangeable, so a uniform surface never silently crosses discharge mechanisms.
