# Modules and typed ID handles

This note captures the current house style for dense table handles.

The important rule is: use `id[T]` for integer-backed handles whose raw storage should not be accidentally mixed with other handles.

```elisa
module Pascal.Semantic:
    extern Symbol
    extern Scope

    type SymbolId = id[Symbol]
    type ScopeId = id[Scope]
```

`SymbolId` and `ScopeId` can both lower to the same compact integer representation, but they are not assignment-compatible. To cross the abstraction boundary deliberately, unwrap with `!`:

```elisa
def symbol_index(symbol_id: SymbolId) -> usize:
    return (!symbol_id - 1).usize()
```

Use module-local aliases for short names. The full names remain inspectable as `Pascal.Semantic.SymbolId` and `Pascal.Semantic.ScopeId`, while code inside the module can use the concise forms.

```elisa
module SML:
    extern Name
    type NameId = id[Name]

module Perl:
    extern Name
    type NameId = id[Name]
```

These two `NameId` aliases do not collide because their canonical names are `SML.NameId` and `Perl.NameId`.

Use `using` when a file intentionally works in one module's vocabulary:

```elisa
using SML

def raw_name_id(name_id: NameId) -> u32:
    return !name_id
```

Keep aliases shared when the underlying identity really is shared. For example, Pascal currently keeps:

```elisa
type TypeNameId = NameId
```

That is intentional because Pascal value names and type names are both handles into the same interned identifier table. `TypeNameId` is a readability alias, not a distinct handle family. If a future phase gives type names a separate table, it should become:

```elisa
extern TypeName
type TypeNameId = id[TypeName]
```

The golden rule still applies: high-level aliases should wrap the low-level representation, not hide it. You should be able to inspect the raw handle with `!`, but ordinary code should pass the typed alias.
