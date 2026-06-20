# 92 — Sound Recursive Proofs

Status: **V1 landed in the semantic checker.** Recursive proof support is intentionally conservative:
it may decline true programs, but it must not prove a false one.

## 1. Soundness rule

Recursive reasoning is not implemented as global quantified axioms. The checker emits only:

- **Ground defining equations** for pure, total functions at concrete call sites.
- **Guarded facts**: callee `requires` clauses guard callee `ensure` clauses and defining equations.
- **Smaller-call IHs** only when a termination certificate proves the call is well-founded.

No recursive equation or inductive hypothesis is emitted unless the callee is pure, total, and called
inside its valid `requires` domain. If any part is uncertain, strict proof reports a decline and the
runtime/debug contract path remains the fallback where one exists.

## 2. Certificates

The semantic checker records proof-report subjects that identify why recursion was trusted:

- `direct numeric`: a direct self-call strictly decreases a numeric or lexicographic measure.
- `mutual numeric`: a mutually recursive SCC edge strictly decreases the caller/callee measures.
- `structural enum`: a self-call uses a match-bound recursive enum child.

These certificates authorize recursive IH injection and recursive equation instantiation. They are
also surfaced in `--explain` proof reports as `recursive IH`, `recursive equation`, `mutual
termination`, and `structural termination`.

## 3. Supported V1 shape

V1 is meant to prove practical linear recursion without pretending bounded integers are mathematical
integers:

- Direct countdowns such as `f(n - 1) + c`.
- Simple affine postconditions such as `result == n + c`, `result >= c`, and guarded lower bounds.
- Mutual even/odd-style bounded facts when every SCC edge has a verified decreasing measure.
- Structural recursion over recursive enum parameters when calls pass match-bound children.

Arithmetic remains overflow-aware. For example, `size(left) + size(right) + 1 >= 1` over bounded
integers is declined unless separate facts prove the addition cannot overflow.

## 4. Explicit V1 exclusions

The checker deliberately declines:

- Indirect recursion through function values.
- Method-dispatched recursion.
- Heap/reference structural measures.
- Same-argument recursive calls and omega traps.
- Recursive calls outside the callee `requires` domain.
- Partial arithmetic such as division where the domain is not proven.
- Nonlinear arithmetic as a required success case.

The project rule is simple: **correctness beats completeness**. Add proving power in small slices, with
adversarial false-claim tests beside every new positive.
