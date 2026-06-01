# Packed lowering inspection surface (`-emit packed`)

This document records the current text contract for packed lowering inspection.

## Emit names

- Canonical: `packed`
- Accepted aliases: `packed-info`, `packedinfo`

## Report header

Output begins with:

```text
packed lowering
  contract: <value>
  canonical: <value>
  readonly publication gate: <value>
```

Current metadata values are:

- `contract: canonical-compiler-graph`
- `canonical: variant-sparse`
- `readonly publication gate: Frozen`

## No-packed-enum case

If no packed enums are present:

```text
  enums: none
```

## Per-enum section shape

For each packed enum:

```text
<EnumName>
  effective abi: <abi>
  profile: <profile>                           # only when declared
  declared abi override: <override>            # only when declared
  declared prefix override: <override>         # only when declared
  row bytes: <n>
  common prefix words: <n>
  side-table common words: <n>
  variants: <sorted, comma-separated names>
  common fields:
    - <field>: <type> inline row_field=<n>
    - <field>: <type> side_table word_offset=<n> words=<n>
```

If there are no common fields:

```text
  common fields: none
```

## Effective ABI behavior

- Default lowering baseline is canonical `variant-sparse`.
- Dense/index lowering paths still use dense index handle semantics.
- Explicit ABI strings are normalized through packed-ABI parsing.

Accepted ABI spellings map to:

- `dense-fixed` (also `dense_fixed`, `densefixed`, `fixed_dense`, `fixed-dense`)
- `index-soa` (also `index`, `soa`, `indexsoa`, `index_soa`)
- `variant-sparse` (also `variant`, `variantsparse`, `variant_sparse`, `sparse`)

## Elisa example

```elisa
@packed_profile(build_heavy)
packed enum Expr:
    common:
        @storage(side_table)
        span: i64
        kind: u32
    Lit(value: i64)
    End
```

```bash
go run ./src -emit packed sample.elisa
```

## Related docs

- `docs/18-current-surface-ergonomics.md`
- `docs/34-cli-emit-mode-catalog.md`
- `docs/44-cli-argument-normalization-and-defaults.md`
