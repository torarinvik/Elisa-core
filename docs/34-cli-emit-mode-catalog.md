# CLI emit mode catalog

This note documents canonical `-emit` modes and accepted aliases in current
Elisa-core CLI parsing.

## Canonical modes

- `ast`
- `lowered`
- `semantic`
- `facts`
- `unsafe`
- `progress`
- `fmt`
- `doc`
- `iface`
- `deps`
- `deps-json`
- `ir`
- `interpret`
- `serve`
- `tests`
- `benches`
- `fixtures`
- `test`
- `test-runner`
- `llvm`
- `packed`
- `c-bind-check`
- `c-bind-check-json`
- `header`
- `bc`
- `obj`
- `c-archive`

## Accepted normalization aliases

Examples of accepted aliases that normalize to canonical modes:

- `lower`, `lowering`, `grammar-lowered` -> `lowered`
- `sema`, `semantics`, `lowered-semantic`, `typed-report` -> `semantic`
- `fact`, `fact-trace`, `trace-facts` -> `facts`
- `unsafe-summary`, `unsafe-report` -> `unsafe`
- `progress-summary`, `progress-report` -> `progress`
- `format`, `formatter` -> `fmt`
- `docs`, `reference` -> `doc`
- `interface`, `api` -> `iface`
- `dep`, `dependencies` -> `deps`
- `depsjson`, `dependencies-json` -> `deps-json`
- `frontend-ir`, `bundle` -> `ir`
- `run`, `interp` -> `interpret`
- `server` -> `serve`
- `test-list` -> `tests`
- `bench-list` -> `benches`
- `fixture-list` -> `fixtures`
- `run-test`, `run-tests` -> `test`
- `runner` -> `test-runner`
- `packed-info`, `packedinfo` -> `packed`
- `cbind-check`, `c-bind`, `cbind` -> `c-bind-check`
- `cbind-check-json`, `c-bind-json`, `cbind-json`, `ffi-manifest`, `abi-manifest` -> `c-bind-check-json`
- `bitcode` -> `bc`
- `object` -> `obj`
- `carchive`, `archive`, `static-archive`, `staticlib`, `static-lib` -> `c-archive`

## Filter support

`-filter` is accepted only for:

- `facts`
- `tests`
- `benches`
- `fixtures`
- `test-runner`
- `test`

Using `-filter` with other emit modes is rejected.

## Output path (`-o`) support

Current behavior:

- `-o` is rejected for `ast`, `tests`, `benches`, `fixtures`, `test`,
  `interpret`, and `c-bind-check`
- `-o` is honored by generated text and artifact modes such as `fmt`, `doc`,
  `deps`, `deps-json`, `iface`, `semantic`, `facts`, `test-runner`, `llvm`,
  `packed`, `header`, `bc`, `obj`, and `c-archive`
- `lowered` and `ir` write file artifacts by default and also accept explicit
  `-o` overrides
- `c-bind-check-json` emits JSON to stdout in current runner flow

## Example invocations

```sh
go run ./src -emit semantic path/to/file.elisa
go run ./src -emit sema path/to/file.elisa
go run ./src -emit fact-trace -filter "kind=eq:widen" path/to/file.elisa
go run ./src -emit runner -filter beta path/to/file.elisa
```

For HTTP server behavior behind `-emit serve`, see
[37-compile-server-api-surface.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/37-compile-server-api-surface.md).
