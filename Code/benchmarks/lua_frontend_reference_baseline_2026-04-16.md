# Lua Frontend Reference Parse Comparison

- Capture date: `2026-04-16T10:36:24Z`
- Commit: `e65eaa51bc538034fa20c2024972efa1532288d5`
- Git status during capture: dirty
- Host: `Torarins-MacBook-Air.local`
- Machine: `Darwin arm64`

## Command

```bash
./compiler/scripts/run_lua_frontend_reference_benchmark.py \
  --opt-level=-O3 \
  --parse-iterations 20 \
  --repeats 3 \
  --json-out /tmp/lua-reference-benchmark-2026-04-16-apples.json
```

## Scope

- Manifest: `Code/benchmarks/lua_frontend_corpus_manifest.txt`
- Mode compared: `parse`
- llcontext harness: `Code/benchmarks/lua_frontend_bench.c`
- Reference harness: `Code/benchmarks/lua_reference_bench.c`
- Shared inputs: 11
- Real corpus inputs with results: 10

## Aggregate Summary

All-common aggregate includes the synthetic shared input. Real-corpus aggregate excludes that synthetic case and is the more representative headline number for the curated benchmark corpus.

| Aggregate | llcontext Avg MiB/s | Reference Avg MiB/s | ll/reference |
| --- | ---: | ---: | ---: |
| All common inputs | 88.78 | 63.14 | 1.406x |
| Real corpus only | 97.45 | 58.64 | 1.662x |

## Per-Input Summary

| Input | llcontext Avg MiB/s | Reference Avg MiB/s | ll/reference |
| --- | ---: | ---: | ---: |
| synthetic | 2.05 | 108.22 | 0.019x |
| strings_comments | 110.35 | 45.11 | 2.446x |
| numerics_operators | 71.65 | 34.12 | 2.100x |
| tables_calls | 86.25 | 36.05 | 2.392x |
| control_flow | 123.17 | 60.45 | 2.037x |
| labels_gotos | 108.57 | 42.54 | 2.552x |
| closures_globals | 101.61 | 50.35 | 2.018x |
| locals_recursion | 82.88 | 37.56 | 2.207x |
| closure_pipeline | 98.31 | 74.17 | 1.325x |
| control_label_matrix | 91.68 | 108.05 | 0.849x |
| lexer_stress | 100.01 | 97.96 | 1.021x |

## Notes

- The bundled C reference build emits the existing macOS `tmpnam` deprecation warning from `Code/lua/loslib.c` during compilation.
- The parse comparison now uses the same syntax-parse checksum path on the llcontext side that the checked/frontend analysis path already accepts. `closure_pipeline` is no longer skipped.
- The previous skip was a benchmark-harness bug, not a frontend-grammar rejection: the harness treated any negative parse checksum as failure, but parse checksums are signed `i64` accumulators and accepted sources can legitimately produce negative values.
- The synthetic input is intentionally shared between both harnesses, but it is not representative of the curated real-Lua corpus and heavily favors the stock Lua parser.