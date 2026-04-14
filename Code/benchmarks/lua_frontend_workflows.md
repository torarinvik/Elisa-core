# Lua Frontend Benchmarking And Differential Workflows

This document describes the developer-facing tools around the llcontext Lua
frontend. Lua behavior checks live in the llcontext test runner, while these
tools help with parity and performance work outside the regular test suite.

## Test Boundary

- Lua semantic and analysis behavior:
  - `bash Code/llcontext_lua/test/run_tests.sh`
- Host compiler safety:
  - `cd compiler && go test ./src/parser ./src/semantic ./src/backend ./src/frontendir ./src`

## Smoke Test

To run a low-cost end-to-end smoke pass across benchmark, differential,
bundle capture, and bundle comparison tooling:

```bash
bash ./compiler/scripts/smoke_lua_frontend_reports.sh
```

This smoke run intentionally uses tiny iteration counts and `--skip-real-corpus`
for fast validation, while still exercising:

- `run_lua_frontend_storage_benchmark.py`
- `run_lua_frontend_differential.py`
- `profile_lua_frontend.sh`
- `capture_lua_frontend_baseline.sh`
- `compare_lua_frontend_reports.py`

In VS Code, the same path is available as the `lua frontend reporting smoke`
task.

## Differential Sweep

The curated differential corpus lives under:

- `Code/llcontext_lua/test/differential_corpus/`

Run the developer-facing comparison against the C reference parser with:

```bash
python3 ./compiler/scripts/run_lua_frontend_differential.py
```

To also write a machine-readable report:

```bash
python3 ./compiler/scripts/run_lua_frontend_differential.py --json-out /tmp/lua-differential.json
```

Notes:

- The script compares accept/reject behavior between:
  - the llcontext frontend
  - the C reference harness built from `onelua.c`
- It prints extra llcontext fingerprints for closure/env/label-heavy families.
- Accepted corpus annotation policy is intentionally family-specific:
  - control-flow accepts require `llcontext-analysis-fp`, plus `llcontext-label-fp` when labels or gotos are involved
  - closure/global accepts require `llcontext-env-fp`, `llcontext-closure-fp`, and `llcontext-analysis-fp`
  - lexical/numeric/operator/table-call accepts require `llcontext-analysis-fp` only
  - reject cases remain accept/reject parity checks with no fingerprint requirement
- With `--json-out`, it also writes a structured report with run metadata,
  per-case statuses, llcontext/reference outcomes, expected fingerprints,
  observed llcontext fingerprints, and mismatch summaries.
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

To also write a machine-readable report:

```bash
python3 ./compiler/scripts/run_lua_frontend_storage_benchmark.py --json-out /tmp/lua-benchmark.json
```

Useful options:

- `--modes parse,checksum,lexer,env,closure,label,analysis`
- `--parse-iterations 20`
- `--sample-iterations 5000`
- `--repeats 3`
- `--skip-real-corpus`
- `--opt-levels=-O0,-O2,-O3`

Supported modes:

- `parse`
- `checksum`
- `lexer`
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
- With `--opt-levels`, the script benchmarks each listed optimization level in
  one run and tags console summaries plus JSON entries with the active level.
- The benchmark JSON also records per-opt-level benchmark executable paths under
  `current_benches_by_opt_level` and `inline_benches_by_opt_level` so downstream
  tooling can correlate each run with the binary that produced it.
- With `--json-out`, it also writes a structured report with run metadata,
  per-input/per-mode measurements, aggregate summaries, and skipped-run
  reasons.
- The default benchmark/profile/baseline mode set is
  `parse,checksum,lexer,env,closure,label,analysis`.
- `sample` remains available explicitly with `--modes sample` or as part of a
  custom comma-separated mode list.

## Profile Bundle

To generate a profiling bundle with benchmark logs and, optionally,
differential logs:

```bash
bash ./compiler/scripts/profile_lua_frontend.sh --out /tmp/llcontext-lua-profile
```

This writes:

- `benchmark.log`
- `benchmark.json`
- `differential.log` unless `--skip-differential` is used
- `differential.json` unless `--skip-differential` is used
- `metadata.json`
- `README.txt`

Useful options:

- `--opt-level -O3`
- `--opt-levels=-O2,-O3`
- `--parse-iterations 20`
- `--sample-iterations 5000`
- `--repeats 3`
- `--keep-temp`
- `--skip-real-corpus`
- `--skip-differential`

The profiling bundle now captures both human-readable logs and the matching
structured JSON artifacts, making it suitable for both manual inspection and
post-run comparison tooling.

## Baseline Capture

To capture a lightweight bundle with structured differential and benchmark
artifacts in one stable folder:

```bash
bash ./compiler/scripts/capture_lua_frontend_baseline.sh --out /tmp/llcontext-lua-baseline
```

Useful options:

- `--opt-level -O3`
- `--opt-levels=-O2,-O3`
- `--parse-iterations 20`
- `--sample-iterations 5000`
- `--repeats 3`
- `--keep-temp`
- `--skip-real-corpus`
- `--skip-benchmark`
- `--skip-differential`

This writes a bundle containing:

- `benchmark.log`
- `benchmark.json` when benchmark capture is enabled
- `differential.log`
- `differential.json` when differential capture is enabled
- `metadata.json`
- `README.txt`

The baseline bundle is lighter-weight than the profiling bundle and is aimed at
repeatable comparison artifacts rather than profiler-heavy investigation.

Both bundle types stamp `metadata.json` with timestamp, repo/git state, host
details, key settings, and the underlying command lines used to generate the
artifacts.

When `--keep-temp` is used, both wrappers also pass it through to the
underlying benchmark and differential generators and record that choice in
`metadata.json`, which makes debugging failed CI or local runs easier.

## CI Bundle Compare

For CI, the stable pattern is:

1. Capture and store one approved baseline bundle artifact.
2. Capture a candidate bundle with the same settings.
3. Compare the candidate against the stored baseline with CI thresholds.

Capture a baseline bundle with explicit pinned settings:

```bash
bash ./compiler/scripts/capture_lua_frontend_baseline.sh \
  --out artifacts/lua-frontend-baseline \
  --opt-level -O3 \
  --parse-iterations 20 \
  --sample-iterations 5000 \
  --repeats 3
```

Then, in CI, capture a candidate bundle and compare it against that saved
baseline with the dedicated wrapper:

```bash
bash ./compiler/scripts/ci_compare_lua_frontend_baseline.sh \
  --baseline-dir artifacts/lua-frontend-baseline \
  --candidate-out /tmp/llcontext-lua-candidate \
  --opt-level -O3 \
  --parse-iterations 20 \
  --sample-iterations 5000 \
  --repeats 3 \
  --min-delta-pct 5
```

The CI wrapper reuses `capture_lua_frontend_baseline.sh` for candidate capture,
then runs `compare_lua_frontend_reports.py` with `--ci-bundle` so meaningful
changes fail the job while metadata-only drift stays non-failing unless you add
`--fail-on-metadata-change`.

## Compare Structured Reports

To compare two structured differential reports:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline/differential.json candidate/differential.json
```

To compare two full bundle directories (for example, profile or baseline bundles):

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py /tmp/llcontext-lua-baseline-a /tmp/llcontext-lua-baseline-b
```

To compare two structured benchmark reports:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline/benchmark.json candidate/benchmark.json
```

Optional machine-readable comparison output:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline/benchmark.json candidate/benchmark.json --json-out /tmp/lua-compare.json
```

To suppress small benchmark noise and only report larger throughput moves:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline/benchmark.json candidate/benchmark.json --min-delta-pct 5
```

You can also combine relative and absolute thresholds:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline/benchmark.json candidate/benchmark.json --min-delta-pct 5 --min-delta-mib-s 0.10
```

To use the comparison tool in automation and fail only on meaningful changes:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline/benchmark.json candidate/benchmark.json --min-delta-pct 5 --fail-on-changes
```

To make bundle comparisons fail on metadata drift as well:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline_bundle candidate_bundle --min-delta-pct 5 --fail-on-changes --fail-on-metadata-change
```

Convenience presets for the most common automation cases:

```bash
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline/benchmark.json candidate/benchmark.json --ci-benchmark
python3 ./compiler/scripts/compare_lua_frontend_reports.py baseline_bundle candidate_bundle --ci-bundle
```

- `--ci-benchmark` is shorthand for a benchmark-focused CI policy using `$5\%$` thresholding and `--fail-on-changes`
- `--ci-bundle` is shorthand for the same benchmark policy at the bundle level, while still ignoring metadata-only drift unless `--fail-on-metadata-change` is also set
- Explicit threshold flags like `--min-delta-pct` still override the preset defaults

The comparison tool auto-detects the report type from the `tool` field and
prints concise summary deltas, added/removed entries, and the most relevant
changed runs or cases. Benchmark threshold flags affect benchmark comparisons
only; differential comparisons remain exact. When you pass bundle directories,
the tool pairs and compares `metadata.json`, `benchmark.json`, and
`differential.json` when present in both bundles. When `--fail-on-changes` is
set, the command exits non-zero only if the remaining actionable changes survive
the configured benchmark thresholds; metadata differences only count when
`--fail-on-metadata-change` is also set.

Benchmark and differential comparisons also report configuration mismatches
separately before comparing results. That includes incompatible optimization
level sets, iteration counts, enabled input sets, and differential strictness,
which prevents silently treating unlike-for-like runs as ordinary performance or
parity diffs.

Verdict meanings:

- `clean`: no differences detected
- `threshold-clean`: differences existed, but only below the configured benchmark thresholds
- `changed`: meaningful benchmark/report differences or missing bundle components were detected
- `metadata-drift`: only metadata differences were detected

Bundle comparison JSON also includes `component_summary`, which records:

- `compared_components`
- `missing_components`
- `verdict_counts`
- `components_by_verdict`

This makes it easier for tooling to answer questions like "was this bundle only
metadata drift?" without walking every nested component manually.

Bundle comparison JSON also includes `action_summary`, which records the current
policy-aware view of the bundle:

- `actionable_component_count`
- `actionable_components`
- `non_actionable_changed_component_count`
- `non_actionable_changed_components`
- `missing_component_count`
- `missing_components`
- `failing_component_count`
- `failing_components`

This makes it easy to tell which components would actually matter under the
current comparison flags, including whether metadata drift is being ignored or
counted.

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
