# CLI argument normalization and defaults

This document captures argument parsing and defaulting behavior for `elisacore` compiler-mode invocations.

## Emit normalization

- `-emit=<mode>` and `-emit <mode>` are both accepted.
- Unknown emit values are passed through unchanged, then rejected later if unsupported.

Selected current aliases:

- `facts`: `fact`, `fact-trace`, `trace-facts`
- `semantic`: `sema`, `semantics`, `lowered-semantic`, `typed-report`
- `lowered`: `lower`, `lowering`, `grammar-lowered`
- `packed`: `packed-info`, `packedinfo`
- `c-bind-check-json`: `cbind-check-json`, `c-bind-json`, `cbind-json`, `ffi-manifest`, `abi-manifest`

## Optimization flags

Accepted forms:

- `-O0`, `-O2`, `-O3`
- `-O=0`, `-O=2`, `-O=3`
- `-O 0`, `-O 2`, `-O 3`

Unsupported values return an error:

```text
unsupported optimization level "<value>" (expected O0, O2, or O3)
```

## Default optimization by emit mode

If no explicit `-O` is provided:

- `bc`, `obj`, `c-archive` default to `O3`
- all other emits default to `O0`

## Filter option parsing

- `-filter=<value>` and `-filter <value>` are both accepted.
- empty filter is treated as no filter.
- non-empty filter is only allowed for emits:
  - `facts`
  - `tests`
  - `benches`
  - `fixtures`
  - `test-runner`
  - `test`

## Linker/native flag parsing

Accepted forms:

- `-link=<flag>` or `-link <flag>`
- `-L=<dir>` or `-L <dir>` (normalized into `-L<dir>`)
- `-l<name>` or `-l <name>` (normalized into `-l<name>`)

## Removed packed ABI flag

`-packed-abi` is intentionally rejected.

Current diagnostic:

```text
-packed-abi has been removed; use canonical packed lowering or enum-level @packed_profile(...) instead
```

## Packed lowering default

CLI defaults to canonical packed lowering profile when no override is provided.

## Address default

If not provided, `-addr` defaults to:

```text
127.0.0.1:8080
```

## Elisa example command set

```bash
go run ./src -emit llvm -O3 sample.elisa
go run ./src -emit facts -filter "mode=eq:summary" sample.elisa
go run ./src -emit obj -L ./native/lib -lSDL2 -o sample.o sample.elisa
```

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/35-pipeline-and-introspection-emits.md`
- `docs/36-c-archive-output-surface.md`
