# Growing a struct field through a helper

`helper(&m.bits, x)` — handing a container FIELD of a region-param struct ref
to a function that grows it — is rejected with "cannot infer region parameter".
Growing the same field *in place* (`m.bits.push(x)`) is fine, and so is
forwarding a bare container parameter. Only the path through a field fails.

## Where it is

`forwardsParamIdent` in `compiler/src/semantic/analyzer_region_param_inference.go`
decides whether an argument forwards a parameter, and so whether the callee's
region must be threaded through the caller. It peels `&x`, parens and casts,
and stops at a field or index access — so `&m.bits` does not read as forwarding
`m`, the caller never becomes region-polymorphic over it, and there is no
region to supply.

## Why the obvious fix is wrong

Peeling `FieldExpr` and `IndexExpr` there makes the good case work, and it
breaks `TestScopedArenaGrowthStructFieldStillRejected`:

```elisa
def grow(out: mutable darray[i64]&, v: i64) -> void:
    region scratch(1024):
        in scratch:
            out.push(v)          # grows the CALLER's container into a
                                 # function-scoped arena: dangling at return
def use(h: mutable Holder&) -> void:
    grow(&h.items, 7)
```

That program is a use-after-free and is currently rejected — but it is rejected
*because the caller cannot infer a region*, not because anything looks at where
`grow` actually allocates. Teaching the caller to supply `h`'s region removes
the error and the dangling write goes unreported. Measured: the peel turns that
test from REJECTED to accepted.

So the gate is standing on the inference gap. Closing the gap needs the
scoped-arena growth rejected on its own terms first — a check in `grow` that a
region-carrying container parameter is never grown under an `in <scoped
arena>:` — and only then is the peel safe.

## Working around it

Pre-size and index-assign rather than push, which is what every other module in
`nw-core` does:

```elisa
Ints::put(&work.seen, at, value)     # writes an element: no region needed
work.seen_n <- at + 1
```

`nw-core/src/tasks/syllogism.elisa` keeps its name buffer this way for exactly
this reason.
