Below is a syntax-only mini spec, ordered from **easiest to implement** to **hardest**.
This ordering is based on compiler effort, not usefulness.

---

# Memory Layout Syntax Spec

## 1. `aligned(N) struct`

```context
aligned(16) struct Vec4:
    x: f32
    y: f32
    z: f32
    w: f32
```

**Syntax meaning:**
Requests that the struct itself be aligned to `N` bytes.

---

## 2. `packed struct`

```context
packed struct PacketHeader:
    kind: u8
    size: u16
    id: u32
```

**Syntax meaning:**
Requests that the struct use packed layout with minimal padding.

---

## 3. `repr(c) struct`

```context
repr(c) struct Foo:
    a: u8
    b: i32
    c: u8
```

**Syntax meaning:**
Requests that field order and layout follow C-compatible rules.

---

## 4. `reorderable struct`

```context
reorderable struct Foo:
    a: u8
    b: i32
    c: u8
```

**Syntax meaning:**
Allows the compiler to reorder fields to reduce padding or improve layout.

---

## 5. `cacheline struct`

```context
cacheline struct Counter:
    value: i64
```

**Syntax meaning:**
Requests that each instance begin on a cache-line boundary.

---

## 6. `isolate_cacheline` field modifier

```context
struct WorkerState:
    isolate_cacheline jobs_done: i64
    isolate_cacheline errors: i64
```

**Syntax meaning:**
Requests that the marked field be placed on its own cache line.

---

## 7. `hotcold struct` with `hot:` and `cold:` sections

```context
hotcold struct Entity:
    hot:
        x: f32
        y: f32
        vx: f32
        vy: f32
        hp: i32

    cold:
        name: str
        lore: str
        quest_flags: [32]i32
```

**Syntax meaning:**
Splits fields into hot and cold groups inside one logical type.

---

## 8. `layout aos struct`

```context
layout aos struct Particle:
    x: f32
    y: f32
    z: f32
    vx: f32
    vy: f32
    vz: f32
```

**Syntax meaning:**
Requests array-of-structs physical layout.

---

## 9. `layout soa struct`

```context
layout soa struct Particle:
    x: f32
    y: f32
    z: f32
    vx: f32
    vy: f32
    vz: f32
```

**Syntax meaning:**
Requests struct-of-arrays physical layout.

---

## 10. Combined layout modifiers

```context
layout soa hotcold struct Enemy:
    hot:
        x: f32
        y: f32
        vx: f32
        vy: f32
        hp: i32
        active: bool

    cold:
        name: str
        faction: str
        lore: str
```

**Syntax meaning:**
Combines SoA storage with hot/cold field grouping.

---

## 11. `layout tiled(N) struct`

```context
layout tiled(256) struct Particle:
    x: f32
    y: f32
    z: f32
    vx: f32
    vy: f32
    vz: f32
```

**Syntax meaning:**
Requests that instances be stored in fixed-size tiles of `N` elements.

---

## 12. `region Name`

```context
region ParseArena
```

**Syntax meaning:**
Declares a named allocation region.

---

## 13. `struct T in Region`

```context
region ParseArena

struct Expr in ParseArena:
    kind: ExprKind
    left: Expr?
    right: Expr?
```

**Syntax meaning:**
Declares that instances of the type belong to the named region.

---

## 14. `layout ... in Region`

```context
region SimArena

layout soa struct Particle in SimArena:
    x: f32
    y: f32
    z: f32
    vx: f32
    vy: f32
    vz: f32
```

**Syntax meaning:**
Combines physical layout selection with region-based allocation.

---

# Suggested implementation order

If you want the cleanest climb up the mountain:

1. `aligned(N) struct`
2. `packed struct`
3. `repr(c) struct`
4. `reorderable struct`
5. `cacheline struct`
6. `isolate_cacheline`
7. `hotcold struct`
8. `layout aos struct`
9. `layout soa struct`
10. combined layout modifiers
11. `layout tiled(N) struct`
12. `region Name`
13. `struct T in Region`
14. `layout ... in Region`

My blunt recommendation: the highest ROI starting subset is:

```context
repr(c) struct ...
packed struct ...
aligned(N) struct ...
reorderable struct ...
layout soa struct ...
hotcold struct ...
```

That subset already gives you a surprisingly juicy performance toolbox without summoning too many compiler demons.

I can turn this into a stricter formal spec next, with exact grammar like `StructDecl ::= ...` and rules for which modifiers may be combined.

---

# Pointer Typestate Mini-Spec

This is a small formal-ish spec for pointer nullness in Contextlang.

The intent is:

- keep the language low-level
- keep pointer usage ergonomic
- make null-safety explicit
- allow proof-by-control-flow for pointers without requiring a general dependent type system

In short:

> Contextlang does **not** have full dependent types, but pointers carry a lightweight proof state.

## Core pointer states

There are three pointer/reference states for a pointee type `T`:

```context
T&   # proven non-null
T&?  # may be null
T!   # proven null
```

Interpretation:

- `T&` means the compiler currently knows the pointer is usable.
- `T&?` means the pointer is storage/transport state and must be proven before use.
- `T!` means the compiler currently knows the pointer is null.

## Grammar sketch

```text
RefType ::= BaseType "&"
          | BaseType "&?"
          | BaseType "!"
```

Nested forms are allowed in the same way ordinary references are allowed:

```context
u8&&
void&&?
Node&?!   # if the parser allows nested suffix chaining, this means the outer ref is proven null
```

The important point is that each reference layer has its own nullness state.

## Meaning of `null`

The literal `null` is only assignable where null is legal by type:

- allowed: `T&?`
- allowed: `T!`
- rejected: `T&`

Example:

```context
p0: Node&? = null   # ok
p1: Node!  = null   # ok
p2: Node&  = null   # error
```

## Assignability rules

For the same pointee type `T`, assignability is:

```text
T&   -> T&?   allowed
T!   -> T&?   allowed
T&   -> T&    allowed
T&?  -> T&?   allowed
T!   -> T!    allowed

T&?  -> T&    rejected without proof
T!   -> T&    rejected
T&   -> T!    rejected
T&?  -> T!    rejected without proof
```

This forms a tiny pointer typestate lattice:

```text
    T&
     \
      T&?
     /
    T!
```

Where `T&?` is the widened “unknown / maybe-null” state.

## Use rule: storage may be nullable, use must be proven

This is the central rule.

Nullable pointer values are allowed to exist freely in locals, fields, globals, and parameters.
But operations that *use* the pointee require proof of non-nullness.

Operations requiring `T&` proof include:

- field access
- indexing
- pointer arithmetic that semantically uses the pointer value as an addressable base
- passing an argument to a function expecting `T&`
- returning a value where `T&` is required
- casts that would strengthen `T&?` into `T&`

So this is illegal:

```context
box: Box&? = maybe_box()
return box.value    # error
```

But this is legal:

```context
box: Box&? = maybe_box()
if box == null:
    return 0
return box.value
```

## Proof sources

The compiler may refine a `T&?` value into `T&` or `T!` when proof is available.

### 1. Direct null checks

```context
if p != null:
    # here p : T&

if p == null:
    # here p : T!
```

### 2. Guard-clause fallthrough

If a branch exits unconditionally, the opposite fact is known after it.

Examples:

```context
if p == null:
    return

# here p : T&
use(p)
```

```context
if p == null:
    panic("null")

# here p : T&
use(p)
```

### 3. Assertions

Assertions act as runtime-checked proofs for later code:

```context
assert p != null

# here p : T&
use(p)
```

This is especially useful for container invariants or low-level runtime code.

### 4. Short-circuit boolean conditions

Proof flows through boolean conditions in the obvious safe direction.

```context
if p != null and p.len > 0:
    # right-hand side sees p : T&
    ...
```

Similarly:

```context
if p == null or p.len == 0:
    ...
```

The compiler may analyze the right-hand side using the proof learned from the left-hand side.

### 5. Ternary expressions

Proof also applies inside ternary branches:

```context
src: u8& = value if value != null else ""
```

### 6. Explicit typestate assignment

Contextlang supports explicit typestate-changing assignment:

```context
ptr as & <- expr
ptr as ! <- expr
```

Intended meaning:

- `as & <-` means “store a value that must now be treated as proven non-null”
- `as ! <-` means “store a value that must now be treated as proven null”

Example:

```context
node as ! <- sfree_node(node)
```

## Safe free pattern

The canonical safe-free API shape is:

```context
def sfree[T](ptr: T&) -> T!:
    free(ptr.void&())
    return null
```

This forces the caller to prove the pointer is non-null before free, and returns a value proven null afterward.

Example usage:

```context
if node != null:
    node as ! <- sfree_node(node)
```

This is nice because it matches the real machine-level story:

- before free: valid pointer
- after free: not valid to use anymore
- compiler state becomes null-proven

## Illegal strengthening rule

The compiler must reject attempts to strengthen pointer proof without evidence.

Rejected examples:

```context
box: Box&? = maybe_box()
strong: Box& = box              # error
```

```context
box: Box&? = maybe_box()
return box.Box&()               # error
```

```context
call_requires_nonnull(box)      # error if box : Box&?
```

This rule is the heart of the system.
Without it, `T&` would stop meaning “proven non-null” and would collapse into a weaker hint.

## Cast rule

Casts may reinterpret representation, but should not conjure proof out of nowhere.

So:

- `void& -> Node&` is fine when the source is already proven non-null
- `void&? -> Node&` is rejected without proof
- `Node&? -> void&` is rejected without proof if the cast requires a proven non-null base
- `Node&? -> void&?` is representation-preserving and may be allowed if such syntax exists

The clean design principle is:

> casts change representation, not truth.

## Mutation rule

Assignments update the current known pointer state.

Examples:

```context
p: mutable Node&? = alloc_node()

if p != null:
    p as ! <- sfree_node(p)
    # now p : Node!
```

```context
p: mutable Node&? = null
p as & <- alloc_nonnull_node()
# now p : Node&
```

If a pointer-typed lvalue receives a wider state through ordinary assignment, the current proof may widen too.

Example:

```context
p: mutable Node& = alloc_nonnull_node()
q: Node&? = maybe_node()

p <- q   # rejected, because that would weaken T& without proof
```

But:

```context
p: mutable Node&? = alloc_nonnull_node()
# current flow state for p may be treated as Node& after assignment
```

## Recommended style

The cleanest usage discipline is:

### Use `T&?` for

- optional fields
- linked-list next pointers
- cache buckets
- globals that may be uninitialized
- container buffers that are absent until first allocation
- FFI values that may return null

### Use `T&` for

- function parameters that require a valid object
- return values guaranteed to succeed
- locals after proof
- pointer math bases known to be valid

### Use `T!` for

- post-free states
- explicit sentinel null results
- APIs that intentionally return “this is definitely null now”

## The intended programming model

The intended mental model is:

1. store pointers in `T&?`
2. prove them before use
3. use them as `T&`
4. transition them to `T!` when an operation guarantees nullness

That gives you most of the practical value of dependent typing for pointers, while keeping the language simple and predictable.

## Canonical examples

### Optional object use

```context
def read_box(box: Box&?) -> int:
    if box == null:
        return 0
    return box.value
```

### Safe free

```context
def release(node: mutable Node&?):
    if node != null:
        node as ! <- sfree_node(node)
```

### Asserted invariant

```context
assert list.data != null
memcpy(out.data.void&(), list.data.void&(), size)
```

### Illegal proof-skipping

```context
def bad(box: Box&?) -> int:
    return box.value   # error
```

```context
def also_bad(box: Box&?) -> Box&:
    return box.Box&()  # error
```

## Best compact slogan

If you want the whole feature in one sentence:

> **Contextlang pointers are typestated:** `T&` means usable, `T&?` means maybe usable after proof, and `T!` means definitely null.

If you want, I can also turn this section into a stricter EBNF-style compiler spec with explicit typing judgments like:

```text
Γ ⊢ p : T&?
Γ, p : T& ⊢ e : U
------------------
Γ ⊢ if p != null: e
```

which would make the proof rules even more formal.

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

---

## Thoughts on length-indexed arrays and strings

Your idea is very plausible, and it fits the same philosophy as pointer typestate:

> keep representation low-level, but let types carry the safety facts that matter.

I think the design splits naturally into **three** array/string families.

### 1. Static arrays

This is the easy win and is already very natural.

```text
Array(T, N)
```

or surface syntax like:

```context
T[N]
```

This is already a length-indexed type.
Its length is compile-time known, zero-overhead, and perfect for stack/local/static data.

### 2. Dynamic owned arrays with length in the type

This is the more ambitious part.

Conceptually:

```text
DArray[T, n]
```

where `n` is a value-level natural number tracked in the type.

Then resize operations become type transitions:

```text
resize : DArray[T, n] × m -> DArray[T, m]
```

That is elegant and very much in the spirit of the pointer system.

But it immediately raises one big question:

### What kind of index is `n`?

There are two realistic choices.

#### A. Fully dependent runtime index

```text
DArray[T, n]
```

where `n` is any runtime integer value.

This is maximally expressive, but it is also where compiler complexity rises fast:

- type equality now depends on symbolic arithmetic
- control-flow refinement may need facts like `i < n`
- function signatures become indexed over runtime values
- inference gets much harder

This is beautiful, but it is no longer “small extension” territory.

#### B. Opaque branded length tokens

Instead of true full dependence, you can make length-indexing existential/brand-based:

```text
exists n. DArray[T, n]
```

or operationally, “an array carries a statically tracked length identity, and resize returns a new identity”.

This gives you strong API safety without forcing the compiler to become an arithmetic theorem prover.

I think this is the sweet spot.

### 3. Strings as specialized arrays

Strings can follow the same pattern.

You could distinguish:

- `u8[N]` — fixed-size byte array
- `Str[N]` — UTF-8 or byte string known to have logical length `N`
- `DStr[n]` — dynamically allocated owned string with tracked length

Then concatenation could produce a new indexed type:

```text
concat : Str[A] × Str[B] -> Str[A + B]
```

or for dynamic owned strings, a resized result brand.

This is elegant, but arithmetic-on-types appears immediately if you want exact results like `A + B`.

So again, the real question is whether you want:

- exact arithmetic types
- or safe opaque length identities

## My recommendation

I would implement this in layers.

### Stage 1 — static arrays only

Treat fixed arrays as fully length-indexed and push that hard.

This gives you:

- compile-time bounds knowledge
- zero-overhead indexing checks where possible
- exact type-level size info
- very low implementation cost

### Stage 2 — owned dynamic arrays with explicit runtime length field, but abstract typed constructors

Keep runtime representation C-like:

```context
repr(c) struct DArray[T]:
    data: T&?
    len: usize
    cap: usize
```

But let the type system expose a stronger logical wrapper notion, something like:

```text
OwnedArray[T, n]
```

where operations like `push`, `resize`, `append_many`, `truncate`, `clear`, `concat`, and `strcat` produce new logical types.

This can still compile to the same C-like struct representation.

That preserves your zero-overhead requirement while keeping safety facts in the type layer.

### Stage 3 — avoid general spans/views unless they become necessary

I agree with your instinct that if the owned array/string type already carries length, many use-cases for spans disappear.

But I would not ban views conceptually.
I would just postpone them.

Because the moment you want:

- a subrange without allocation
- a borrowed window into larger storage
- parsing/tokenization over slices
- cheap substring/subarray APIs

some kind of non-owning view becomes very attractive.

So I would say:

- **you can delay spans**
- **you probably cannot eliminate the need forever**

unless every slice-like operation allocates or mutates in place.

## The biggest design fork

The biggest decision is this:

### Option 1: exact dependent lengths

Pros:

- mathematically beautiful
- very strong safety
- APIs like `append : Array(T, n) -> T -> Array(T, n+1)` are gorgeous

Cons:

- typechecker complexity rises sharply
- arithmetic normalization/equality becomes a major feature
- inference gets harder fast

### Option 2: lightweight indexed safety

Pros:

- keeps compiler small-ish
- preserves zero-overhead representation
- gives practical safety for most operations
- fits your pointer typestate philosophy very well

Cons:

- less expressive than full dependent types
- some exact arithmetic properties become opaque or library-mediated

For this language, I strongly recommend **Option 2 first**.

That is:

> use dependent-type ideas where they buy concrete systems safety, but stop before the compiler turns into a symbolic algebra engine.

## Best concise formulation

If I had to summarize your direction in one clean sentence, I’d say:

> Contextlang should stay as low-level as C in representation and control, but use lightweight dependent typing for pointer validity and shape/length facts instead of borrow checking or lifetime analysis.

I think that is a very strong language identity.

If you want, I can next turn the array/string part into the same kind of mini-spec I just did for pointers, with candidate surface syntax and an implementation ladder from “cheap and practical” to “fully dependent and wild.”

---

# Array and String Shape Mini-Spec

This section proposes a shape/length-typed array and string model for Contextlang.

The goal is the same as the pointer typestate goal:

- keep runtime representation simple and C-like
- keep operations zero-overhead where possible
- move important safety facts into the type system
- avoid borrow checking and lifetime analysis

In short:

> Arrays and strings should carry **shape facts** in the type in the same way pointers carry **validity facts** in the type.

## High-level design split

There are three natural families:

1. **static arrays** with compile-time length
2. **owned dynamic arrays** with type-tracked logical length identity
3. **strings** as specialized byte arrays / byte buffers with length facts

## Candidate surface syntax

I think the cleanest surface story is:

### Static arrays

```context
u8[16]
Node[4]
T[N]
```

This is already very good and should remain the canonical syntax for fixed-length arrays.

### Owned dynamic arrays

I would suggest one of these explicit forms:

```context
DArray[T, n]
OwnedArray[T, n]
```

I slightly prefer:

```context
DArray[T, n]
```

because it makes the “this is dynamic storage” distinction very obvious.

### Strings

For strings I would distinguish between logical string length and raw byte arrays:

```context
u8[N]       # raw fixed byte array
Str[N]      # fixed string / byte-string with known logical length
DStr[n]     # owned dynamic string with tracked logical length
```

If you want maximum minimalism, `Str[N]` and `DStr[n]` can just be library-level wrappers over `u8[N]` and `DArray[u8, n]`.

## Core type constructors

Formally, let the type layer have:

```text
Array(T, N)      fixed-size array, N compile-time known
DArray[T, n]     owned dynamic array, logical length index n
Str[N]           fixed string of logical length N
DStr[n]          owned dynamic string of logical length n
```

Where:

- `N` is a compile-time natural number
- `n` is a shape/length index for dynamic storage

The critical design choice is what kind of thing `n` is.

## Two possible models for dynamic lengths

### Model A — exact dependent runtime index

This is the mathematically strongest version.

```text
DArray[T, n]
```

where `n` is an actual runtime value appearing in the type.

Then you can write signatures like:

```text
push    : DArray[T, n] × T -> DArray[T, n + 1]
concat  : DArray[T, a] × DArray[T, b] -> DArray[T, a + b]
slice   : DArray[T, n] × i × j -> DArray[T, j - i]
```

This is gorgeous.

But it means the compiler must reason about:

- arithmetic normalization
- equality of symbolic expressions
- constraints like `i <= j <= n`
- inference of shape variables

That is a major jump in compiler complexity.

### Model B — lightweight indexed safety (recommended)

This is the version I recommend first.

Instead of giving the typechecker full arithmetic over runtime naturals, give each dynamic shape a tracked index identity.

Conceptually:

```text
DArray[T, shape_id]
```

where `shape_id` is a shape witness / logical length identity.

Operationally:

- the runtime value still contains a `len` field
- the compiler tracks that this particular value currently has some logical length identity
- operations that change length produce a *new* shape identity

So:

```text
resize : DArray[T, shape_in] × usize -> DArray[T, shape_out]
push   : DArray[T, shape_in] × T     -> DArray[T, shape_out]
concat : DArray[T, shape_left] × DArray[T, shape_right] -> DArray[T, shape_result]
```

where `shape_out`, `shape_result` are fresh post-operation shapes.

This avoids requiring the compiler to prove that `shape_result = shape_left + shape_right` at the type-equality level.

In examples below, ASCII witness names such as `shape_in`, `shape_out`, `shape_result`, and `shape_after` are preferred.
Greek-letter shorthands are acceptable as mathematical shorthand, but they are not required in source code.

It preserves the key safety idea:

> after an operation that changes length, the result has a different logical shape than the input.

That already buys a lot.

## Recommended runtime representation

I would keep representation brutally simple.

### Dynamic array representation

```context
repr(c) struct DArray[T]:
    data: T&?
    len: usize
    cap: usize
```

### Dynamic string representation

```context
repr(c) struct DStr:
    data: u8&?
    len: usize
    cap: usize
```

This is excellent because it is:

- trivial to FFI
- trivial to debug
- trivially zero-overhead as data layout
- compatible with the pointer typestate story (`data` is optional storage and must be proven before raw use)

So the type-level safety lives above a very ordinary low-level runtime shape.

## Static arrays

Static arrays are the easiest and strongest part of the design.

### Typing

```text
Γ ⊢ e : Array(T, N)
```

### Indexing rule

```text
Γ ⊢ e : Array(T, N)
Γ ⊢ i : Int
-----------------------------
Γ ⊢ e[i] : T
```

If `i` is a compile-time constant, the compiler may reject out-of-bounds at compile time.

If `i` is dynamic, you can choose one of two policies:

1. unchecked indexing like C
2. checked indexing in safe surface forms, unchecked in explicit low-level forms

For Contextlang, I would keep the low-level spirit and make this a policy choice independent of the type system.

### Array construction

```text
Γ ⊢ e1 : T ... Γ ⊢ eN : T
----------------------------------------
Γ ⊢ [e1, ..., eN] : Array(T, N)
```

## Owned dynamic arrays

### Typing judgment

For the recommended lightweight model:

```text
Γ ⊢ e : DArray[T, shape_id]
```

where `shape_id` is a logical shape witness.

### Length observation

At runtime:

```text
len(e) : usize
```

At the type level, `shape_id` means “the current logical length fact associated with this value”, not necessarily a normalized arithmetic term.

### Resize rule

Resize changes shape identity:

```text
Γ ⊢ a : DArray[T, shape_in]
Γ ⊢ m : usize
--------------------------------
Γ ⊢ resize(a, m) : DArray[T, shape_out]
```

where `shape_out` is fresh.

This matches your idea exactly:

> if you resize, you must construct/cast to a dynamic array of a different length.

The important part is that this cast is **logical and zero-overhead**, not a runtime reinterpretation hack.

### Push / append rule

```text
Γ ⊢ a : DArray[T, shape_in]
Γ ⊢ x : T
--------------------------------
Γ ⊢ push(a, x) : DArray[T, shape_out]
```

Again `shape_out` is fresh, because the logical shape changed.

### Concatenation rule

```text
Γ ⊢ a : DArray[T, shape_left]
Γ ⊢ b : DArray[T, shape_right]
--------------------------------
Γ ⊢ concat(a, b) : DArray[T, shape_result]
```

`shape_result` is fresh.

The exact arithmetic relation can remain part of library semantics/documentation instead of core type equality.

## Strings

Strings should mirror arrays closely.

### Fixed strings

```text
Γ ⊢ s : Str[N]
```

This is useful for:

- string literals
- compile-time known buffers
- APIs where exact fixed size matters

### Dynamic strings

```text
Γ ⊢ s : DStr[shape_id]
```

with exactly the same logical-shape story as `DArray[u8, shape_id]`.

### Relationship to byte arrays

You can define:

```text
Str[N]  ≈ Array(u8, N+1)  # if you include trailing zero in representation
Str[N]  ≈ Array(u8, N)    # if logical string length excludes terminator and representation policy is separate
```

I would keep the logical length separate from terminator policy.

That is:

- `Str[N]` means logical content length `N`
- whether a trailing `0` exists is a representation convention, not the type-level meaning

That keeps the model cleaner.

## Do you still need spans/views?

Not immediately.

If owned arrays and strings already carry length, you can get very far without separate view types.

That said, I would frame it like this:

- **first implementation:** no spans/views required
- **long-term:** likely still useful for subranges and non-owning windows

So I agree with your instinct as an implementation priority:

> do not start with spans.

But I would not bake in the claim that they will never be needed.

## Formal safety intent

The array/string equivalent of the pointer theorem-of-intent is:

> If a program type-checks, shape-changing operations produce new logical shapes explicitly, and fixed-size shape facts are never silently forgotten.

That is the key idea.

In other words:

- pointers track **validity facts**
- arrays/strings track **shape facts**

This is a very coherent type story.

## Cheap-and-practical implementation ladder

I would implement this in the following order.

### Stage 1 — strengthen static arrays

Do this first.

- keep `T[N]`
- make the type checker treat the length as fully part of the type
- add better compile-time constant index checking
- allow array literals and exact array-type matching

This is high value and relatively cheap.

### Stage 2 — library/runtime-owned dynamic arrays

Add a runtime struct like:

```context
repr(c) struct DArray[T]:
    data: T&?
    len: usize
    cap: usize
```

Then expose compiler-level logical shape wrappers incrementally.

At first, this can even be mostly API-discipline plus type wrappers.

### Stage 3 — logical post-operation shape change

Teach the typechecker that operations like:

- `resize`
- `push`
- `append_many`
- `truncate`
- `clear`
- `concat`
- `strcat`

produce a new logical shape identity.

This gets you the safety effect you want without arithmetic normalization.

### Stage 4 — optional arithmetic indexing

Only if the language really wants it later, add exact arithmetic forms like:

```text
append : Array(T, n) × T -> Array(T, n+1)
concat : Str[A] × Str[B] -> Str[A+B]
```

This is beautiful, but it should be a later stage, not the starting point.

## Recommended surface-language summary

If I had to propose the most practical version today, it would be:

### Keep

```context
T[N]
```

for static arrays.

### Add

```context
DArray[T, n]
DStr[n]
```

as logical length-indexed owned containers.

### And define core APIs like

```text
resize      : DArray[T, shape_in] × usize -> DArray[T, shape_out]
push        : DArray[T, shape_in] × T -> DArray[T, shape_out]
append_many : DArray[T, shape_in] × DArray[T, chunk] -> DArray[T, shape_out]
truncate    : DArray[T, shape_in] × usize -> DArray[T, shape_out]
clear       : DArray[T, shape_in] -> DArray[T, shape_out]
concat      : DStr[shape_left] × DStr[shape_right] -> DStr[shape_result]
```

where each shape-changing operation returns a new logical shape.

That gives you the flavor you want:

- low-level representation
- no borrow checker
- no lifetime analysis
- dependent-style safety facts exactly where they matter

## Best concise slogan

If pointers are typestated by validity, arrays and strings should be typestated by shape.

That gives Contextlang a very crisp identity:

> C-like memory model, with lightweight dependent typing for validity and shape.

---

# Compiler Implementation Plan for Shape-Typed Arrays and Strings

This section turns the array/string design into a concrete compiler roadmap.

The goal is **not** to implement full dependent typing immediately.
The goal is to get the high-value safety properties first while keeping the frontend simple enough to evolve.

## Guiding implementation principle

Implement this as:

- **exact shape typing for fixed arrays**
- **logical shape identities for dynamic owned arrays/strings**
- **no arithmetic normalization in the first implementation**

In one sentence:

> make shape part of the type, but make shape *equality* cheap.

## Recommended rollout order

There is a very clear best order.

### Phase 1 — strengthen existing fixed arrays

This should be the MVP.

Why first:

- syntax already exists: `T[N]`
- the parser already understands array type forms
- the semantic system already has `ArrayType`
- it gives immediate value with relatively low compiler churn

Ship in this phase:

- treat array length as an exact type-level property everywhere
- reject assigning `T[4]` to `T[5]`
- support exact array literals / construction typing if desired
- improve compile-time constant index diagnostics

This phase is cheap and gives a lot of shape safety immediately.

### Phase 2 — add owned dynamic array/string surface types

Add syntax and semantic meaning for:

```context
DArray[T, n]
DStr[n]
```

But initially interpret `n` as a **logical shape witness**, not a symbolic arithmetic term.

This phase should focus on:

- syntax
- type representation
- assignability rules
- runtime representation conventions

not on complicated inference.

### Phase 3 — teach shape-changing APIs to produce fresh post-state shapes

Once `DArray[T, n]` exists, teach the analyzer that specific operations return a *new* shape.

For example:

```text
resize : DArray[T, shape_in] × usize -> DArray[T, shape_out]
push   : DArray[T, shape_in] × T -> DArray[T, shape_out]
concat : DArray[T, shape_left] × DArray[T, shape_right] -> DArray[T, shape_result]
```

where `shape_out`, `shape_result` are fresh logical shape identities.

This captures the safety idea without needing arithmetic reasoning.

### Phase 4 — optional symbolic arithmetic later

Only later, if it proves worthwhile, add exact arithmetic shape forms like:

```text
n + 1
a + b
j - i
```

This should be an explicit later milestone, not part of the initial implementation.

## Concrete compiler plan by subsystem

## 1. AST plan

### Phase 1 AST changes

Fixed arrays may not need major AST changes if current `ArrayType` already stores:

- element type
- size expression

But the AST should preserve whether the size expression is:

- a compile-time constant literal
- a named constant
- a general expression

If not already represented cleanly, this is the moment to formalize that.

### Phase 2 AST additions

For dynamic arrays/strings, add distinct type nodes rather than overloading `GenericType` too much semantically.

Recommended AST additions:

```text
DynArrayType {
    Elem TypeExpr
    Shape TypeExpr or ShapeExpr
}

DynStringType {
    Shape TypeExpr or ShapeExpr
}
```

Why distinct nodes are better than generic-only treatment:

- clearer semantic handling
- clearer diagnostics
- easier to special-case shape evolution later
- avoids burying important language concepts in generic sugar

If you want lighter syntax implementation at first, parsing them as special generic names is acceptable, but semantically they should become dedicated internal forms.

## 2. Parser plan

### Phase 1 parser work

Very small.

Likely just:

- keep `T[N]`
- ensure array size expressions are preserved accurately
- optionally add array literal parsing if desired

### Phase 2 parser work

Teach the parser to recognize:

```context
DArray[T, n]
DStr[n]
```

Recommendation:

- parse them first as special named/generic forms
- lower them into dedicated AST nodes during parse or semantic resolution

That keeps syntax work small.

### Syntax recommendation

I would use:

```context
DArray[T, n]
DStr[n]
```

not something more magical.

This keeps the feature visually explicit and predictable.

## 3. Semantic type representation plan

### Phase 1 semantic work

Strengthen existing fixed arrays.

Current semantic `ArrayType` likely already contains:

- element type
- size summary

Improve it to distinguish exact shape identity more rigorously.

Recommended representation:

```text
ArrayType {
    Elem Type
    Size ShapeTerm
}
```

Where `ShapeTerm` in phase 1 can simply be:

- integer literal
- resolved named constant
- opaque textual fallback for diagnostics

### Phase 2 semantic additions

Add explicit semantic types:

```text
DynArrayType {
    Elem Type
    Shape ShapeWitness
}

DynStringType {
    Shape ShapeWitness
}
```

Where `ShapeWitness` is intentionally lightweight.

Recommended initial `ShapeWitness` forms:

```text
ConstShape(n)
NamedShape(name)
FreshShape(id)
OpaqueShape(text)
```

This is enough to express:

- exact fixed lengths
- named lengths
- post-operation fresh dynamic lengths
- diagnostics for unresolved/opaque forms

without symbolic arithmetic.

## 4. Assignability and equality rules

### Fixed arrays

Require exact equality of:

- element type
- shape term

So:

```text
Array(T, 4)  ≠ Array(T, 5)
Array(T, N)  = Array(T, N)
```

### Dynamic owned arrays

Require exact equality of:

- element type
- shape witness

So after a resize-like operation, the returned value is *not* interchangeable with the input type unless explicitly rebound.

That is a feature, not a bug.

It is exactly the safety guarantee you asked for.

## 5. Builtin/API knowledge plan

To make dynamic shapes useful without deep dependence, the analyzer should learn a small number of shape-transforming operations specially.

Example categories:

- `resize`
- `push`
- `append_many`
- `concat`
- `strcat`
- `truncate`
- `clear`

The easiest implementation strategy is:

- keep a table of known shape-transforming functions
- when the analyzer sees those calls, synthesize a fresh result shape witness

This is similar in spirit to how many compilers treat intrinsics specially.

It is much cheaper than making every function truly dependently typed.

## 6. String plan

Strings should reuse the same machinery as arrays as much as possible.

Recommended internal rule:

- `DStr[shape_id]` is semantically very close to `DynArrayType{Elem: u8, Shape: shape_id}`
- `Str[N]` is semantically very close to `Array(u8, N)` plus string-specific intent

Whether you expose them as separate semantic types or thin wrappers is mostly an ergonomics decision.

My preference:

- keep separate semantic string types for diagnostics and language clarity
- internally reuse array/shape machinery as much as possible

## 7. Diagnostics plan

This feature will only feel good if diagnostics are excellent.

Important errors to support clearly:

- mismatched fixed lengths
- using an old dynamic-shape value where a post-resize shape is required
- indexing with compile-time out-of-bounds constants
- illegal implicit weakening/forgetting of shape facts

Example good diagnostic style:

```text
cannot assign DArray[u8, row] to DArray[u8, shape_after]
note: resize returns a fresh logical shape for shape_out
```

And when the mismatch comes from comparing two separate fresh-producing calls, a second note should explain that they do not unify implicitly:

```text
argument 2 to "same" expects DArray[i32, shape_after#1], got DArray[i32, shape_after#2]
note: grow returns a fresh logical shape for shape_after
note: separate calls that produce fresh shapes do not share the same logical shape identity
```

That kind of message teaches the model, not just the failure.

## 8. Testing plan

The test plan should also be phased.

### Phase 1 tests

- exact equality for fixed arrays
- rejecting mismatched fixed lengths
- constant-index bounds diagnostics
- fixed-array literals/initialization if added

### Phase 2 tests

- parsing `DArray[T, n]` and `DStr[n]`
- type equality for dynamic shape witnesses
- shape witness preservation across plain assignment

### Phase 3 tests

- `resize` returns a new shape witness
- `push` returns a new shape witness
- `concat` returns a fresh result witness
- old-shape values rejected where new-shape values are expected

### Example regression style

```context
def grow(a: DArray[u8, row]) -> DArray[u8, shape_after]:
    return resize(a, 16)
```

and:

```context
def bad(a: DArray[u8, row]) -> DArray[u8, row]:
    return resize(a, 16)   # should fail if resize returns fresh shape_after
```

That is exactly the kind of “dependent-ish” safety check the language should advertise.

## 9. Example/runtime plan

Do not rewrite all runtime code around dynamic shape typing immediately.

Recommended rollout:

1. keep runtime structs plain and C-like
2. add a few example programs using the new typed wrappers
3. only later migrate runtime/container helpers if the model feels good in practice

This reduces risk and keeps experimentation cheap.

## 10. Recommended MVP boundary

If I were choosing the concrete first implementation boundary, it would be:

### MVP

- exact fixed-array typing for `T[N]`
- improved constant-index checking
- syntax support for `DArray[T, n]` and `DStr[n]`
- semantic representation for dynamic shape witnesses
- no arithmetic shape expressions yet
- no full generic dependent inference

### First post-MVP

- known builtins/API table for shape-changing operations
- fresh witness generation on `resize` / `push` / `concat`
- stronger diagnostics

### Later

- arithmetic shape expressions
- richer subrange/view story
- optional proofs for index constraints

## Best practical recommendation

If I had to turn all this into one concrete engineering instruction, it would be:

> implement exact fixed arrays first, then implement dynamic arrays/strings as C-like runtime structs with lightweight logical shape witnesses, and only later consider symbolic arithmetic on shapes.

That gives you the dependent-style safety you want while keeping the compiler tractable.
