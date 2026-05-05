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

### Storage qualifiers as a separate axis

Reference types also carry a storage qualifier.

Let:

```text
σ ∈ { any, heap, stack, static }
```

and refine the reference family to:

```text
Ref(σ, T, nn)    written σ T&
Ref(σ, T, may)   written σ T&?
Ref(σ, T, null)  written σ T!
```

### Named generic pointer qualifiers

The reference storage/state axes can also be abstracted by generic parameters.

Declaration forms:

```text
refstorage r
refstate s
```

Example:

```text
struct Foo[refstorage r, refstate s]
```

Reference syntax then permits symbolic qualifiers:

```text
r T&[s]
```

which should be read as:

```text
Ref(r, T, s)
```

where `r` ranges over the built-in storage qualifiers

```text
{ any, heap, stack, static }
```

and `s` ranges over the pointer proof states

```text
{ nn, may, null }
```

### Mixed generic argument order

When ordinary type parameters and pointer qualifiers are mixed, instantiation order is the declaration order.

So:

```text
Foo[T, refstorage r, refstate s]
```

is instantiated as:

```text
Foo(U, heap, nn)
```

not by grouping all ordinary types first and all pointer qualifiers later.

### Nearest-reference attachment

For nested references, a symbolic state suffix attaches to the nearest preceding `&`.

So:

```text
T&&[s]
```

parses as:

```text
Ref(any, Ref(any, T, nn), s)
```

not as:

```text
Ref(any, Ref(any, T, s), nn)
```

Intended meaning:

- `any` means storage/provenance is intentionally erased or unspecified at the type level
- `heap` means the referenced object is heap-backed
- `stack` means the referenced object is one caller-frame/local stack slot
- `static` means the referenced object is static/global storage

The important design point is that storage qualifiers classify **one referenced object**, not cardinality.

So:

- `stack T&` means “a reference to one stack-resident object of type `T`”
- multi-element stack storage is modeled by the value type `Array(T, N)` / `T[N]`
- the array value may itself live on the stack, but that does not create a separate “stack array pointer” concept

This keeps storage provenance and aggregate shape as separate axes in the type system.

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

### Storage assignability relation

For ordinary assignment / return / parameter matching, explicit storage qualifiers are invariant.

For equal pointee type `T` and state `s`:

```text
Ref(σ, T, s) ≤ Ref(σ, T, s)
```

But, for distinct qualifiers:

```text
Ref(heap,   T, s) ≰ Ref(any,    T, s)
Ref(stack,  T, s) ≰ Ref(any,    T, s)
Ref(static, T, s) ≰ Ref(any,    T, s)
Ref(heap,   T, s) ≰ Ref(stack,  T, s)
Ref(heap,   T, s) ≰ Ref(static, T, s)
... and so on
```

So storage is not an ordinary subtyping lattice in the way nullness is.

This is why a `heap Box&` does not implicitly flow into an `Box&` parameter or return type: if you want to erase storage provenance, that must be an explicit cast, not an incidental assignment.

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

### Construction / inference rules for storage

Storage can also be inferred at construction sites.

Address-of a local or parameter:

```text
Γ(x) = T      x is a local or parameter
---------------------------------------
Γ ⊢ &x : Ref(stack, T, nn)
```

Address-of a global:

```text
Γ(g) = T      g is a global
---------------------------
Γ ⊢ &g : Ref(static, T, nn)
```

String literals:

```text
Γ ⊢ "text" : Ref(static, u8, nn)
```

The role of `any` is different: it is the explicit “do not track storage provenance here” spelling, commonly used at FFI boundaries, raw-pointer bridges, and deliberate casts.

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

Storage-changing casts are conceptually separate from nullness.
An explicit cast may deliberately erase or reinterpret storage classification when the implementation treats the runtime representation as compatible, but this should never happen implicitly through plain assignment.

So, informally:

```text
Ref(heap, T, nn)  -> Ref(any, T, nn)   may be allowed by explicit cast
Ref(stack, T, nn) -> Ref(any, T, nn)   may be allowed by explicit cast
Ref(static, T, nn)-> Ref(any, T, nn)   may be allowed by explicit cast
```

while ordinary assignment still requires storage equality for explicit user-written qualifiers.

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
