# Static interfaces, extension methods, and UFCS

This note documents the current implemented surface for compile-time interfaces and receiver-style dispatch.

Like `18-current-surface-ergonomics.md`, this is a practical description of the language as accepted by the current compiler, not a forward-looking proposal.

## Static interfaces and associated types

Static interfaces describe compile-time capabilities and associated types.

```elisa
struct BuilderTag:
    tag: int

static interface Builder:
    type Node
    def make(value: int) -> Node

impl Builder for BuilderTag:
    type Node = int

    def make(value: int) -> int:
        return value

def build[B: Builder](value: int) -> B.Node:
    return B.make(value)
```

Current rules:

- `protocol Name:` declares a compile-time capability using the same implementation model as `static interface`
- `static interface Name:` remains the explicit low-level spelling for the same compile-time mechanism
- legacy `interface Name:` still parses for compatibility
- interface members may include associated types and method signatures
- `impl Name for Type:` provides the associated types and methods for one concrete type
- generic parameters may be interface-bounded with `T: InterfaceName`
- associated types are referenced through the bound parameter, for example `B.Node`
- zero-argument static-interface methods are still called as ordinary field-style calls such as `B.state()`

Current implementation model:

- static interface use is resolved at compile time and lowered through specialization-style rewriting rather than a runtime vtable carrier
- interface impl members may also carry annotations such as `@derive(...)`
- `override def` is accepted inside impls when the source wants to mark an intended override explicitly

## Span-like protocols

Parser and tree code can make custom span/range types participate in `left.span + right.span` by providing a visible `SpanLike` static-interface impl.

```elisa
struct Span:
    start: i64
    end: i64

protocol SpanLike:
    type Range
    def combine(left: Range, right: Range) -> Range

impl SpanLike for Span:
    type Range = Span

    def combine(left: Span, right: Span) -> Span:
        return Span{start: left.start, end: right.end}

def binary_span(left: Span, right: Span) -> Span:
    return left + right
```

Current rules:

- `SpanLike` is a compile-time protocol, not a runtime trait object
- the `+` span algebra surface only applies to same-type, non-numeric operands
- if a visible `SpanLike` impl exists for that span type, `left + right` lowers to the impl's `combine(left, right)` method
- legacy helper lookup remains accepted as a compatibility path for older frontends
- the low-level explicit form remains available when desired: `Span.combine(left, right)` or `combine_span(left, right)`

## Generic call specialization

The preferred current generic-call surface is direct bracket specialization on the callee.

```elisa
value: int = identity[int](7)
```

The older helper-like spelling still exists in some tests and older code:

```elisa
value: int = identity.specialize[int]()(7)
```

Current rule:

- direct `fn[T](...)` is the preferred surface when the callee is named directly
- formatter normalization rewrites the older `.specialize[T]()` spelling into the direct bracket form when it can do so safely

## Receiver-scoped extension impls

Receiver-scoped `impl Type:` blocks define extension methods.

```elisa
const enum Tok of i8:
    PLUS = 0

impl Tok:
    def score(self: Tok) -> i64:
        return 7

def read(tok: Tok) -> i64:
    return tok.score()
```

Current rules:

- `impl Type:` is the receiver-scoped extension form
- extension methods spell the receiver explicitly as the first parameter, usually `self: Type`
- method-call syntax like `tok.score()` is rewritten into an ordinary function call with the receiver inserted as argument zero
- named arguments, default arguments, and expression-block arguments still work after that receiver insertion and are reordered against the non-receiver parameters normally

Important precedence rule:

- if `obj.name` is already a real function-valued field access, `obj.name()` stays a field call and does not rewrite into an extension-method call

## UFCS over ordinary free functions

The compiler also supports UFCS-style rewriting for ordinary free functions whose first parameter is a value receiver.

```elisa
struct Box:
    value: i64

def scale(box: Box, delta: i64 = 1) -> i64:
    return box.value + delta

def read(box: Box) -> i64:
    return box.scale(5)
```

Current rules:

- if ordinary member lookup and extension-method lookup do not win first, the compiler may rewrite `value.func(args...)` into `func(value, args...)`
- this rewrite is based on the first parameter's type, so it is intentionally conservative rather than a broad “search anything named func” rule
- if the rewritten first parameter expects a compatible non-null ref, UFCS may autoref an addressable receiver into that ref parameter
- mutable ref receivers still require a writable receiver path, and broader coercions remain explicit
- ambiguity across candidates is rejected with a diagnostic that lists the visible candidate names

## Safe field and safe call chaining

Optional chaining works with fields, supported receiver-style calls, and argument-position transforms.

```elisa
struct Box:
    value: int

def read(maybe_box: Box?) -> int:
    _ = maybe_box?.value
    _ = maybe_box?.scale(2)
    return maybe_box?.value else 0

def score(value: int) -> int:
    return value + 1

def read_transform(maybe_value: int?) -> int?:
    return maybe_value?.(score)
```

Current rules:

- `expr?.field` is the safe field form
- `expr?.call(...)` is the safe call form
- `expr?.(fn)` is the safe transform form, lowering to `fn(expr)` only when `expr` is present
- `expr?.(self.fn)` works with the same extension-method and UFCS rules as ordinary receiver calls; the unwrapped optional payload is placed into the argument slot selected by the rewritten call
- successful type propagation yields an optional result, for example `i64?`
- safe call and transform chaining only work when the underlying dispatch path is otherwise valid and follows the same conservative receiver-autoref rules as ordinary receiver calls
