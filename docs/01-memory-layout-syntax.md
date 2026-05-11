Below is the historical memory-layout mini spec. The current implemented spelling for explicit struct layout modes is suffix-based:

```elisa
struct PackedHeader layout packed:
    tag: u8
    len: u16

struct CHeader layout c:
    kind: u32
    flags: u32
```

See `docs/18-current-surface-ergonomics.md` for the current surface that also includes narrow integers, `bitfield`, `bitset`, `Flags[T]`, SOA stores, and `InlineVec`.

The older sections below remain useful as design notes for future layout modes.

---

# Memory Layout Syntax Spec

## 1. `aligned(N) struct`

```elisa
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

```elisa
packed struct PacketHeader:
    kind: u8
    size: u16
    id: u32
```

**Syntax meaning:**
Requests that the struct use packed layout with minimal padding.

---

## 3. `struct`

```elisa
struct Foo:
    a: u8
    b: i32
    c: u8
```

**Syntax meaning:**
Uses the default field order and layout, which currently follows C-compatible rules.
The old `repr(c) struct` spelling is no longer supported; use `struct` directly.

---

## 4. `reorderable struct`

```elisa
reorderable struct Foo:
    a: u8
    b: i32
    c: u8
```

**Syntax meaning:**
Allows the compiler to reorder fields to reduce padding or improve layout.

---

## 5. `cacheline struct`

```elisa
cacheline struct Counter:
    value: i64
```

**Syntax meaning:**
Requests that each instance begin on a cache-line boundary.

---

## 6. `isolate_cacheline` field modifier

```elisa
struct WorkerState:
    isolate_cacheline jobs_done: i64
    isolate_cacheline errors: i64
```

**Syntax meaning:**
Requests that the marked field be placed on its own cache line.

---

## 7. `hotcold struct` with `hot:` and `cold:` sections

```elisa
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

```elisa
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

```elisa
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

```elisa
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

```elisa
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

```elisa
region ParseArena
```

**Syntax meaning:**
Declares a named allocation region.

---

## 13. `struct T in Region`

```elisa
struct Expr in owner:
    kind: ExprKind
    left: owner Expr&?
    right: owner Expr&?

struct Box[T] in owner:
    value: T
    next: owner Box[T, owner]&?
```

**Syntax meaning:**
Declares a struct with an owner-region parameter using sugar for
`struct Expr[region owner]: ...`. If the struct also has ordinary type
parameters, keep those in brackets and put the owner-region sugar after them:
`struct Box[T] in owner:`.

The owner argument can be a named `region`, a region parameter, or a visible
`Arena` / non-null `Arena&` value:

```elisa
def make(owner: Arena) -> Expr[owner]:
    return Expr{
        kind: ExprKind.Leaf,
        left: null,
        right: null
    }
```

---

## 14. `layout ... in Region`

```elisa
layout soa struct Particle in owner:
    x: f32
    y: f32
    z: f32
    vx: f32
    vy: f32
    vz: f32
```

**Syntax meaning:**
Combines physical layout selection with an owner-region parameter, using sugar
for `layout soa struct Particle[region owner]: ...`.

---

# Suggested implementation order

If you want the cleanest climb up the mountain:

1. `aligned(N) struct`
2. `packed struct`
3. `struct` (default C layout)
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

```elisa
struct ...
packed struct ...
aligned(N) struct ...
reorderable struct ...
layout soa struct ...
hotcold struct ...
```

That subset already gives you a surprisingly juicy performance toolbox without summoning too many compiler demons.

I can turn this into a stricter formal spec next, with exact grammar like `StructDecl ::= ...` and rules for which modifiers may be combined.

---
