# Lua Frontend Reference Parse Comparison

- Capture date: `2026-04-16T11:01:18Z`
- Commit: `5cc3505722187dd5e070ce4e7977e712f845675b`
- Git status during capture: `dirty`
- Host: `Torarins-MacBook-Air.local`
- Machine: `Darwin arm64`

## Command

```bash
./compiler/scripts/run_lua_frontend_reference_benchmark.py \
  --opt-level=-O3 \
  --parse-iterations 20 \
  --repeats 3 \
  --json-out /tmp/lua-reference-benchmark-2026-04-16-mixed.json
```

## Scope

- Manifest: `Code/benchmarks/lua_frontend_corpus_manifest.txt`
- Mode compared: `parse`
- llcontext harness: `Code/benchmarks/lua_frontend_bench.c`
- Reference harness: `Code/benchmarks/lua_reference_bench.c`
- Shared inputs: 11
- Real corpus inputs with results: 10
- Synthetic generator: `mixed_lua_module`
- Synthetic size: `226174` bytes (`286` generated chunks, approx. `4004` statements)

## Aggregate Summary

All-common aggregate includes the generated mixed synthetic input. Real-corpus aggregate excludes that synthetic case and remains the better headline number for the curated checked-in corpus.

| Aggregate | llcontext Avg MiB/s | Reference Avg MiB/s | ll/reference |
| --- | ---: | ---: | ---: |
| All common inputs | 108.48 | 67.26 | 1.613x |
| Real corpus only | 119.24 | 61.62 | 1.935x |

## Per-Input Summary

| Input | llcontext Avg MiB/s | Reference Avg MiB/s | ll/reference |
| --- | ---: | ---: | ---: |
| synthetic | 0.94 | 123.65 | 0.008x |
| strings_comments | 145.13 | 49.87 | 2.910x |
| numerics_operators | 85.90 | 44.90 | 1.913x |
| tables_calls | 106.92 | 36.53 | 2.927x |
| control_flow | 149.73 | 59.74 | 2.506x |
| labels_gotos | 137.33 | 42.21 | 3.253x |
| closures_globals | 133.51 | 48.77 | 2.738x |
| locals_recursion | 100.14 | 48.35 | 2.071x |
| closure_pipeline | 111.26 | 82.71 | 1.345x |
| control_label_matrix | 112.47 | 108.30 | 1.039x |
| lexer_stress | 109.98 | 94.85 | 1.159x |

## Notes

- The bundled C reference build emits the existing macOS `tmpnam` deprecation warning from `Code/lua/loslib.c` during compilation.
- Parse-mode acceptance now uses an explicit parse-status helper rather than inferring success from checksum sign or statement count.
- The llcontext parse benchmark path now uses the length-aware parse checksum helper, which removes repeated `strlen` work from the hot loop and matches the reference harness more closely.
- Lexer-mode acceptance now also uses an explicit lexer-status helper, and env/closure/label/analysis benchmark modes now key acceptance off checked-status instead of fingerprint sign.
- The old `closure_pipeline` skip was a harness bug, not a grammar bug. That case remains accepted and benchmarks at `1.345x` versus the C reference in this capture.
- The new synthetic workload is intentionally more Lua-shaped than the old numeric-only stress file, but it is still a large generated stress case and remains unrepresentative of the tiny curated real-Lua corpus.