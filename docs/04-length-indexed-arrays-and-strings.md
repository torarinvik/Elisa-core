## Thoughts on length-indexed arrays and strings

Your idea is very plausible, and it fits the same philosophy as pointer typestate:

> keep representation low-level, but let types carry the safety facts that matter.

I think the design splits naturally into **three** array/string families.

### 1. Static arrays

This is the easy win and is already very natural.

```text
array[T, N]
```

with the existing short fixed-array form still valid when you want it:

```elisa
array[u8, 16]
array[Node, 4]
array[T, N]

# low-ceremony equivalent for fixed arrays
T[N]
```

This is already a length-indexed type.
Its length is compile-time known, zero-overhead, and perfect for stack/local/static data.
That describes where the array value may live in memory, not a pointer qualifier; if you later borrow one element or one local object by reference, that is where `stack T&`, `static T&`, or `heap T&` enters the story.
`array[T, N]` is the explicit built-in spelling; `T[N]` remains the concise fixed-array form.

This is already a length-indexed type.
### 2. Dynamic owned arrays with length in the type

This is the more ambitious part.

Conceptually:

```text
darray[T, n]
```

where `n` is a value-level natural number tracked in the type.

If the operation may allocate, the practical surface should also admit failure explicitly:

```elisa
error ShapeOpError:
	AllocationFailed
```

Then resize operations become type transitions:

```text
resize : darray[T, n] × m -> darray[T, m] error[ShapeOpError]
```

That is elegant and very much in the spirit of the pointer system.

But it immediately raises one big question:

### What kind of index is `n`?

There are two realistic choices.

#### A. Fully dependent runtime index

```text
darray[T, n]
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
exists n. darray[T, n]
```

or operationally, “an array carries a statically tracked length identity, and resize returns a new identity”.

This gives you strong API safety without forcing the compiler to become an arithmetic theorem prover.

I think this is the sweet spot.

### 3. Strings as specialized arrays

Strings can follow the same pattern.

You could distinguish:

- `str[N]` — fixed string / byte string known to have logical length `N`
- `cstr[n]` — dynamically allocated owned string with tracked length `n`
- `u8[N]` — raw fixed-size byte array when you do not want string semantics

Then concatenation could produce a new indexed type:

```text
concat : str[A] × str[B] -> str[A + B]
```

And if you want the numeric code unit explicitly, the cast stays direct:

```elisa
def first_code(text: str[4]) -> i64:
	return text[0].i64()
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

Keep the runtime representation simple and low-level:

```text
darray[T, n]
```

with a compiler-known runtime carrier that stays plain-data internally.

But let the type system expose a stronger logical wrapper notion, something like:

```text
darray[T, n]
```

where operations like `push`, `resize`, `append_many`, `truncate`, `clear`, `concat`, and `strcat` produce new logical types. In the practical surface, the allocation-sensitive ones should usually be fallible, for example `push : darray[T, n] × T -> darray[T, n+1] error[ShapeOpError]` and `concat : cstr[a] × cstr[b] -> cstr[a+b] error[ShapeOpError]`.

This can still compile to the same plain-data struct representation.

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

> Elisa core should stay as low-level as C in representation and control, but use lightweight dependent typing for pointer validity and shape/length facts instead of borrow checking or lifetime analysis.

I think that is a very strong language identity.

If you want, I can next turn the array/string part into the same kind of mini-spec I just did for pointers, with candidate surface syntax and an implementation ladder from “cheap and practical” to “fully dependent and wild.”
