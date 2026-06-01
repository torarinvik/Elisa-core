# Module interface generation contract (`-emit iface`)

This document captures the current implemented transformation used by `-emit iface`.

## File naming

- input `foo.elisa` generates interface filename `foo.elisai`
- blank source filename falls back to `.elisai`

## Top-level traversal

- declarations are visited in original source order
- each declaration is transformed or filtered
- namespaces and static-if branches recurse with the same rules

## Current declaration transformation rules

- `def` (`FuncDecl`)
  - omitted when `static`
  - omitted when annotated `@internal`
  - otherwise converted to `extern def` shape (`ExternFuncDecl`) with signature metadata preserved
- `extern def`
  - omitted when annotated `@internal`
  - otherwise preserved
- `global`
  - converted to `extern` variable declaration
- `interface`
  - keeps associated type members
  - keeps extern function members
  - drops unsupported/non-interface members
- `impl`
  - keeps associated type members
  - converts non-static, non-`@internal` impl functions to extern members
  - keeps non-`@internal` extern members
- `namespace`
  - recursively filtered
  - omitted when it becomes empty after filtering
- `static if`
  - preserved structurally
  - each branch body is recursively filtered
- `static assert`
  - omitted
- `static assert` block
  - omitted
- `static generate`
  - omitted

## Internal annotation matching

`@internal` filtering currently checks annotation name text equal to `internal`.

## Elisa example

Input:

```elisa
struct Box[T]:
    value: T

@internal
def hidden() -> void:
    pass

def visible(x: i64) -> i64:
    return x

global counter: i64 = 0

impl Box[i64]:
    def take(self: Box[i64]) -> i64:
        return self.value

    @internal
    def secret(self: Box[i64]) -> i64:
        return 0
```

Generated interface surface includes:

```elisa
struct Box[T]:
    value: T

extern visible(x: i64) -> i64
extern counter: i64

impl Box[i64]:
    extern take(self: Box[i64]) -> i64
```

## Related docs

- `docs/35-pipeline-and-introspection-emits.md`
- `docs/33-reference-doc-generation-surface.md`
