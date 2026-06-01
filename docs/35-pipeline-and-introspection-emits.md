# Pipeline and introspection emit surfaces

This note documents implemented non-runner emit modes used for pipeline
artifacts and compiler introspection.

## Frontend IR bundle (`-emit ir`)

```sh
go run ./src -emit ir -o sample.elisair sample.elisa
go run ./src -emit llvm sample.elisair
go run ./src -emit interpret sample.elisair
```

Current behavior:

- emits a frontend bundle artifact (`.elisair`)
- bundle can be consumed by `llvm` and `interpret` emits
- full bundle schema/version and loader notes: `docs/51-frontend-ir-bundle-contract.md`

## Lowered grammar source (`-emit lowered`)

```sh
go run ./src -emit lowered grammar_frontend.elisa
```

Current behavior:

- writes lowered standalone source to default lowered output path
- lowered output omits original grammar declaration surface and contains
  generated lowered helper functions

## Semantic report (`-emit semantic`)

```sh
go run ./src -emit semantic grammar_frontend.elisa
```

Current behavior:

- report includes lowered section plus semantic section
- semantic section includes function signatures, return-isolation, and fact
  records such as snapshots, exits, groups, and blocks

## Facts report (`-emit facts`) with filters

```sh
go run ./src -emit facts -filter "function=contains:fact_core_rules" path/to/file.elisa
```

Current behavior:

- emits fact-trace contract report sections
- supports key-value filter expressions through `-filter`

## Module interface emit (`-emit iface`)

```sh
go run ./src -emit iface -o module.elisai module.elisa
```

Current behavior:

- emits interface surface with exported declarations
- omits implementation bodies and `@internal` declarations
- emitted interface re-parses as valid interface source
- full transform rules: `docs/50-module-interface-generation-contract.md`

## Source dependency manifest (`-emit deps-json`)

```sh
go run ./src -emit deps-json root.elisa
```

Current behavior:

- outputs JSON dependency report with:
  - root file
  - ordered included file list
- supports both `# include "..."` and `include "..."` include forms

## Source dependency list (`-emit deps`)

```sh
go run ./src -emit deps root.elisa
```

Current behavior:

- outputs one absolute dependency path per line
- preserves include traversal order
- deduplicates already-seen files
- detects cyclic include chains and reports an error

## Output behavior notes

- binary-output modes such as `bc` and `obj` write artifacts to files and do
  not stream binary payloads to stdout
- emits like `header` and `iface` with `-o` write to file and keep stdout empty

## Detailed emit-contract references

- `docs/53-ast-emit-text-surface.md`
- `docs/52-formatter-emit-surface.md`
- `docs/58-semantic-report-emit-contract.md`
- `docs/42-fact-trace-filter-contract.md`
- `docs/31-unsafe-report-and-budget-surface.md`
- `docs/45-progress-report-output-surface.md`
- `docs/56-interpret-emit-surface.md`
- `docs/57-llvm-ir-emit-surface.md`
- `docs/54-c-header-emit-contract.md`
- `docs/55-bitcode-and-object-emit-surface.md`
