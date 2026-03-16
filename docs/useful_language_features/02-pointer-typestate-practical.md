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

## Error handling pairs well with pointer proofs

Typed errors are a good fit for the common pattern “nullable FFI result at the edge, proven non-null pointer in the rest of the program”.

Using the current explicit storage qualifiers, a practical wrapper looks like this:

```context
error MemoryError:
    OutOfMemory

extern alloc_node() -> heap Node&?

def require_node() -> heap Node& error[MemoryError]:
    node: heap Node& = alloc_node() else raise MemoryError.OutOfMemory
    return node

def make_node_value() -> int error[MemoryError]:
    node: heap Node& = try require_node()
    node.value <- 42
    return node.value

def make_node_value_or_zero() -> int:
    return try make_node_value() else 0
```

That keeps the low-level boundary honest (`alloc_node` may return null), preserves the fact that the pointer is heap-backed once it succeeds, and lets the rest of the example work with a proven non-null pointer plus explicit propagation and recovery.

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
