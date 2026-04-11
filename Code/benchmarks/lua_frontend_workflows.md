# Lua Frontend Benchmarking And Differential Workflows

This document describes the developer-facing tools around the llcontext Lua
frontend. Lua behavior checks live in the llcontext test runner, while these
tools help with parity and performance work outside the regular test suite.

## Test Boundary

- Lua semantic and analysis behavior:
  - `bash Code/llcontext_lua/test/run_tests.sh`
- Host compiler safety:
  - `cd compiler && go test ./src/parser ./src/semantic ./src/backend ./src/frontendir ./src`

## Differential Sweep

The curated differential corpus lives under:

- `Code/llcontext_lua/test/differential_corpus/`

Run the developer-facing comparison against the C reference parser with:

```bash
python3 ./compiler/scripts/run_lua_frontend_differential.py
```

Notes:

- The script compares accept/reject behavior between:
  - the llcontext frontend
  - the C reference harness built from `onelua.c`
- It prints extra llcontext fingerprints for closure/env/label-heavy families.
- Differential cases can optionally pin expected llcontext semantic
  fingerprints with leading source comments like:

```lua
-- llcontext-env-fp: 1061
-- llcontext-closure-fp: 209
-- llcontext-analysis-fp: 6880
```

  Supported annotation modes are:
  - `env`
  - `closure`
  - `label`
  - `analysis`
- It is a developer tool first. By default it reports mismatches but does not
  fail the run unless `--strict` is passed.

## Benchmark Sweep

The curated real-Lua benchmark corpus lives under:

- `Code/benchmarks/lua_frontend_benchmark_corpus/`

and is listed by:

- `Code/benchmarks/lua_frontend_corpus_manifest.txt`

Run the benchmark sweep with:

```bash
python3 ./compiler/scripts/run_lua_frontend_storage_benchmark.py
```

Useful options:

- `--modes parse,env,closure,label,analysis`
- `--parse-iterations 20`
- `--sample-iterations 5000`
- `--repeats 3`
- `--skip-real-corpus`

Supported modes:

- `parse`
- `sample`
- `env`
- `closure`
- `label`
- `analysis`

Notes:

- The script always includes a synthetic benchmark input.
- Real-corpus inputs are treated as developer tooling. Unsupported inputs are
  reported as skips instead of failing the entire sweep.
- When the older inline-control AST variant is not available, the script
  degrades cleanly and benchmarks only the checked-in frontend.

## Profile Bundle

To generate a profiling bundle with benchmark logs and, optionally,
differential logs:

```bash
bash ./compiler/scripts/profile_lua_frontend.sh --out /tmp/llcontext-lua-profile
```

This writes:

- `benchmark.log`
- `differential.log` unless `--skip-differential` is used
- `README.txt`

Useful options:

- `--parse-iterations 20`
- `--sample-iterations 5000`
- `--repeats 3`
- `--skip-differential`

## Analysis Entry Points

The public source-visible Lua frontend wrappers live in:

- `Code/llcontext_lua/src/lua_frontend.llcontext`

Key wrappers include:

- `lua_frontend_env_ref_summary_report(...)`
- `lua_frontend_env_summary_report(...)`
- `lua_frontend_closure_summary_report(...)`
- `lua_frontend_label_scope_summary_report(...)`
- `lua_frontend_analysis_report(...)`

and the matching fingerprint exports:

- `lua_frontend_env_ref_fingerprint(...)`
- `lua_frontend_env_summary_fingerprint(...)`
- `lua_frontend_closure_summary_fingerprint(...)`
- `lua_frontend_label_scope_fingerprint(...)`
- `lua_frontend_analysis_fingerprint(...)`

These report paths are the preferred surface for Lua-side analysis tests.
