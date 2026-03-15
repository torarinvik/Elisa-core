## Formal typing view

Below is a more formal presentation of the same pointer system.

### Judgments

Use the following judgment forms:

```text
Γ ⊢ e : τ              expression e has type τ under environment Γ
Γ ⊢ τ1 ≤ τ2           τ1 is assignable/coercible to τ2
Γ ⊢ cond ⇒ Γ'         cond being true refines Γ into Γ'
Γ ⊢ cond ⇏ Γ'         cond being false refines Γ into Γ'
```

Here `Γ` is an environment mapping variables/paths to types and current pointer proofs.

### Pointer states as a family of types

For each pointee type `T`, define three reference-state types:

```text
Ref(T, nn)    written T&
Ref(T, may)   written T&?
Ref(T, null)  written T!
```

### Assignability relation

For equal pointee type `T`:

```text
Ref(T, nn)   ≤ Ref(T, may)
Ref(T, null) ≤ Ref(T, may)
Ref(T, nn)   ≤ Ref(T, nn)
Ref(T, may)  ≤ Ref(T, may)
Ref(T, null) ≤ Ref(T, null)
```

And the following do **not** hold:

```text
Ref(T, may)  ≤ Ref(T, nn)
Ref(T, null) ≤ Ref(T, nn)
Ref(T, nn)   ≤ Ref(T, null)
Ref(T, may)  ≤ Ref(T, null)
```

The null literal typing rule is:

```text
Γ ⊢ null : Null
Null ≤ Ref(T, may)
Null ≤ Ref(T, null)
Null ≰ Ref(T, nn)
```

### Use rules

Dereference-like operations require non-null proof.

Field access:

```text
Γ ⊢ e : Ref(S, nn)
fieldtype(S, f) = τ
-----------------------------
Γ ⊢ e.f : τ
```

Indexing:

```text
Γ ⊢ e : Ref(Array(T, n), nn)
Γ ⊢ i : Int
-----------------------------
Γ ⊢ e[i] : T
```

If the reference state is `may` or `null`, these rules do not apply.

### Assignment rule

Ordinary assignment is valid only when the source is assignable to the target type:

```text
Γ ⊢ lhs : τdst
Γ ⊢ rhs : τsrc
Γ ⊢ τsrc ≤ τdst
-----------------------------
Γ ⊢ lhs <- rhs : Γ[lhs ↦ refine(τdst, τsrc)]
```

Where `refine(τdst, τsrc)` updates the current proof state of the assigned lvalue.

Examples:

```text
refine(T&?, T&)  = T&
refine(T&?, T!)  = T!
refine(T&?, Null)= T!
refine(T&,  T&)  = T&
```

### Explicit typestate assignment

The two explicit typestate transitions are special typing forms.

Non-null transition:

```text
Γ ⊢ lhs : Ref(T, s)
Γ ⊢ rhs : τ
Γ ⊢ τ ≤ Ref(T, nn)
-----------------------------
Γ ⊢ lhs as & <- rhs : Γ[lhs ↦ Ref(T, nn)]
```

Null transition:

```text
Γ ⊢ lhs : Ref(T, s)
Γ ⊢ rhs : τ
Γ ⊢ τ ≤ Ref(T, null)
-----------------------------
Γ ⊢ lhs as ! <- rhs : Γ[lhs ↦ Ref(T, null)]
```

This is what makes patterns like safe-free precise.

### Flow refinement rules

If a nullable value is compared with null, the compiler may refine the environment.

True branch of `p != null`:

```text
Γ ⊢ p : Ref(T, may)
-----------------------------
Γ ⊢ (p != null) ⇒ Γ[p ↦ Ref(T, nn)]
```

False branch of `p != null`:

```text
Γ ⊢ p : Ref(T, may)
-----------------------------
Γ ⊢ (p != null) ⇏ Γ[p ↦ Ref(T, null)]
```

True branch of `p == null`:

```text
Γ ⊢ p : Ref(T, may)
-----------------------------
Γ ⊢ (p == null) ⇒ Γ[p ↦ Ref(T, null)]
```

False branch of `p == null`:

```text
Γ ⊢ p : Ref(T, may)
-----------------------------
Γ ⊢ (p == null) ⇏ Γ[p ↦ Ref(T, nn)]
```

The same rule may be applied to tracked field paths such as `list.data` when the compiler can model that path in the environment.

### Short-circuit refinement

For conjunction:

```text
Γ ⊢ a : Bool
Γ ⊢ a ⇒ Γ1
Γ1 ⊢ b : Bool
-----------------------------
Γ ⊢ a and b : Bool
```

Meaning: the right-hand side of `and` is checked in the environment refined by the left-hand side being true.

For disjunction:

```text
Γ ⊢ a : Bool
Γ ⊢ a ⇏ Γ1
Γ1 ⊢ b : Bool
-----------------------------
Γ ⊢ a or b : Bool
```

Meaning: the right-hand side of `or` is checked in the environment refined by the left-hand side being false.

### Guard-clause fallthrough

If the true branch exits, the false refinement survives after the statement.

```text
Γ ⊢ cond ⇒ Γt
Γ ⊢ cond ⇏ Γf
Γt ⊢ then : Exit
-----------------------------
Γ ⊢ if cond: then ; rest  ≡  Γf ⊢ rest
```

This formalizes patterns like:

```context
if p == null:
    return

# rest checked with p : T&
```

### Assertion rule

Assertions are runtime checks that also refine the environment for following code:

```text
Γ ⊢ cond : Bool
Γ ⊢ cond ⇒ Γ'
-----------------------------
Γ ⊢ assert cond : Γ'
```

So `assert p != null` is formally a proof-producing operation.

### Cast rule

Let casts preserve representation changes but never strengthen nullness without proof.

Safe cast rule:

```text
Γ ⊢ e : Ref(A, s1)
repr_cast(A, B)
s1 ≤ s2
-----------------------------
Γ ⊢ e.B(refstate s2) : Ref(B, s2)
```

Operationally, the state relation must not become stronger by cast alone.

So these are rejected:

```text
Ref(T, may)  -> Ref(U, nn)
Ref(T, null) -> Ref(U, nn)
```

even if the underlying pointer bit-pattern representation is identical.

### Safe free rule

The most characteristic rule in the system is:

```text
Γ ⊢ p : Ref(T, nn)
-----------------------------
Γ ⊢ sfree(p) : Ref(T, null)
```

Combined with explicit typestate assignment:

```text
Γ ⊢ p : Ref(T, nn)
-----------------------------
Γ ⊢ p as ! <- sfree(p) : Γ[p ↦ Ref(T, null)]
```

This is a very small form of dependent typing specifically for pointer validity transitions.

### Informal theorem-of-intent

The design goal can be stated as:

> If a program type-checks, every dereference-like pointer use is justified by either construction, control-flow proof, assertion, or explicit typestate transition.

That is the exact sense in which Contextlang gets “dependent type safety for pointers” without needing borrow checking or lifetime analysis.
