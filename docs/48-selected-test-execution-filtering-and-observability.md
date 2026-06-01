# Selected test execution filtering and observability

This document records the current `-emit test` selection, filtering, and debug output surface.

## Name filter matching

Test name filtering uses lowercase matching over function names.

Filter behavior:

- filter string is split on commas
- each pattern is trimmed
- empty pattern entries are ignored
- if a pattern contains `*`, `?`, or `[`, it is treated as a glob pattern
- glob matching uses `path.Match`
- otherwise pattern uses substring matching
- a test is selected when any pattern matches

## Skip annotations

Selected tests can still be marked skipped by annotation:

- `@skip`
- `@ignore`

Skip reason:

- annotation args joined by `, `
- fallback reason `annotation-requested` when no args are given

## No-tests behavior

If no `@test` functions match the filter:

- stdout prints:
  - `[ NO TESTS ] no @test functions matched filter "<filter>"`
- command returns nonzero exit code

## Phase debug output

Environment variable:

- `ELISACORE_TEST_PHASE_DEBUG`

Enabled when value is non-empty and not one of:

- `0`
- `false`
- `off`

Phase lines are printed to stderr as:

```text
[ phase    ] <phase> <detail>
```

Current selected-test phases:

- `selected_tests read_source`
- `selected_tests select_cases`
- `selected_tests compile_dispatch`
- `selected_tests run_cases`

`-emit test` also emits:

- `emit_test selected_test_execution`

## Timing output

Environment variable:

- `ELISACORE_TEST_TIMING`

Enabled when value is non-empty and not one of:

- `0`
- `false`
- `off`

Modes:

- text mode: `[ timing   ] <label> key=value ...`
- json mode when value is `json` or `jsonl`: one JSON object per line with `<metric>_ms` fields and booleans

## Output examples

```elisa
@test
def alpha_case() -> void:
    pass

@test
@skip("slow-path")
def beta_case() -> void:
    pass
```

```bash
ELISACORE_TEST_PHASE_DEBUG=1 go run ./src -emit test -filter "*alpha*" sample.elisa
ELISACORE_TEST_TIMING=jsonl go run ./src -emit test sample.elisa
```

## Related docs

- `docs/32-test-runner-cli-surface.md`
- `docs/34-cli-emit-mode-catalog.md`
- `docs/44-cli-argument-normalization-and-defaults.md`
