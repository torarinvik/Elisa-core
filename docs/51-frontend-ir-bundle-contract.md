# Frontend IR bundle contract (`.elisair`)

This document captures the current frontend bundle artifact contract used by `-emit ir`.

## Extension and mode

- bundle extension: `.elisair`
- emit command: `-emit ir`

## Bundle schema (version 1)

Current bundle payload fields:

- `Version` (int)
- `SourceFilename` (string)
- `ResolvedSource` (`[]byte`)
- `File` (`*ast.File`)

## Encoding behavior

- encoding uses Go `gob`
- encoding nil bundle returns:
  - `frontend IR bundle is nil`
- if `Version == 0`, encoder writes current bundle version (`1`)

## Decoding behavior

- decoding uses Go `gob`
- decoded version must equal `1`
- mismatched version returns:
  - `unsupported frontend IR version <n>`
- missing AST returns:
  - `frontend IR bundle is missing AST`

## Loader behavior in compiler CLI

When input extension is `.elisair`:

- file is decoded as frontend bundle
- `program.file` is taken from bundle AST
- `program.source` is taken from `ResolvedSource`
- active filename uses `SourceFilename` when non-empty
- if `SourceFilename` is blank, loader falls back to the `.elisair` path

This allows downstream emits to run from bundle-carried AST/source without reparsing source text from disk.

## AST type registration

Bundle encode/decode registers explicit AST concrete types for declarations, type expressions, expressions, grammar terms, patterns, and statements. New AST kinds require registration updates to stay serializable.

## Elisa example

```elisa
def helper(x: i64) -> i64:
    return x + 2

def main() -> i64:
    return helper(40)
```

```bash
go run ./src -emit ir -o sample.elisair sample.elisa
go run ./src -emit llvm sample.elisair
go run ./src -emit interpret sample.elisair
```

## Related docs

- `docs/35-pipeline-and-introspection-emits.md`
- `docs/44-cli-argument-normalization-and-defaults.md`
- `docs/37-compile-server-api-surface.md`
