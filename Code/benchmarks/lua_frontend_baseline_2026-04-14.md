# Lua Frontend Baseline Note

- Capture date: 2026-04-14T12:15:31Z
- Commit: `9b2df7384d443a678ea43a798babf8e87eb31cdc`
- Git status during capture: dirty
- Host: `Torarins-MacBook-Air.local`
- Machine: `Darwin 25.4.0 arm64`

## Commands

```bash
bash ./compiler/scripts/capture_lua_frontend_baseline.sh \
  --out /tmp/llcontext-lua-baseline-check \
  --parse-iterations 1 \
  --sample-iterations 1 \
  --repeats 1
```

Underlying bundle commands recorded in `metadata.json`:

```bash
python3 ./compiler/scripts/run_lua_frontend_storage_benchmark.py \
  --opt-level=-O3 \
  --parse-iterations 1 \
  --sample-iterations 1 \
  --repeats 1 \
  --modes parse,checksum,lexer,env,closure,label,analysis

python3 ./compiler/scripts/run_lua_frontend_differential.py --strict
```

## Corpus And Modes

- Manifest: `Code/benchmarks/lua_frontend_corpus_manifest.txt`
- Default mode list: `parse,checksum,lexer,env,closure,label,analysis`
- Inputs in benchmark bundle: 11 total
  - 1 synthetic input
  - 10 manifest-driven corpus inputs

## Differential Summary

- Cases: 27
- Reference mismatches: 0
- Expectation mismatches: 0
- Fingerprint mismatches: 0

## Aggregate Benchmark Summary

All figures below are from `benchmark.json` aggregate summaries for the checked-in frontend at `-O3` with `parse_iterations=1`, `sample_iterations=1`, `repeats=1`.

| Mode | Avg MiB/s | Min MiB/s | Max MiB/s | Input Count |
| --- | ---: | ---: | ---: | ---: |
| parse | 42.675 | 2.44 | 99.18 | 10 |
| checksum | 45.433 | 2.58 | 86.98 | 10 |
| lexer | 118.662 | 0.00 | 247.96 | 11 |
| env | 40.665 | 2.47 | 82.65 | 11 |
| closure | 40.219 | 2.47 | 82.65 | 11 |
| label | 38.330 | 2.47 | 82.65 | 11 |
| analysis | 31.905 | 2.47 | 62.62 | 11 |

## Notes

- The benchmark bundle reported `skipped_labels=2`.
- The skipped entries were the existing `closure_pipeline` `parse` and `checksum` runs, where the current frontend rejects that corpus file in parser mode while still producing `lexer`, `env`, `closure`, `label`, and `analysis` measurements.
- This note captures the lightweight verification bundle only. The full generated bundle directory remains local and is intentionally not checked in.