# Test and benchmark CLI surface

This note documents implemented runner/list emit modes for annotated Elisa-core
functions.

## Annotation-driven lists

Given source:

```elisa
@test
def alpha_case() -> void:
    pass

@fixture
def shared_seed() -> int:
    return 7

@bench
def bench_hot_loop() -> void:
    pass
```

List modes:

```sh
go run ./src -emit tests path/to/file.elisa
go run ./src -emit benches path/to/file.elisa
go run ./src -emit fixtures path/to/file.elisa
```

Current behavior:

- each mode emits only the matching annotation kind
- output rows include function name and signature

## Filter support

List and runner modes accept `-filter`:

```sh
go run ./src -emit tests -filter alpha path/to/file.elisa
go run ./src -emit benches -filter hot path/to/file.elisa
go run ./src -emit test -filter "*beta*" path/to/file.elisa
```

Current behavior:

- substring and glob-like filters are supported in test execution/list flows
- filter patterns are comma-separated; each pattern is trimmed and matched case-insensitively
- patterns containing `*`, `?`, or `[` use glob matching; other patterns use substring matching
- `-filter` is accepted for `facts`, `tests`, `benches`, `fixtures`, `test-runner`, and `test`
- using `-filter` on unsupported emit modes is rejected

## Generated test-runner source

Use:

```sh
go run ./src -emit test-runner path/to/file.elisa
```

Current behavior:

- emits runner source that invokes selected `@test` functions
- emitted runner includes exported entry mapping to generated test main
- non-test helper functions are not invoked by generated runner body

Skip markers affect generated test-runner source:

```elisa
@skip(todo)
@test
def beta_case() -> void:
    pass
```

Current behavior:

- skipped tests are listed as skipped in runner output
- skipped cases are omitted from invocation calls

## Direct test execution mode

Use:

```sh
go run ./src -emit test path/to/file.elisa
```

Current behavior:

- executes selected tests and prints run, pass, skip, fail, and summary lines
- fail/skip mix still continues through the selected set
- summary includes selected, passed, skipped, and failed counters
- if no `@test` functions match, stdout prints a `[ NO TESTS ]` line and the command exits nonzero

When `ELISACORE_TEST_PHASE_DEBUG=1` is set, execution prints selected test phase
debug markers to stderr.

`ELISACORE_TEST_TIMING` enables timing lines for compile/run/suite phases, with
JSON output when set to `json` or `jsonl`.

Detailed filtering, phase-debug, and timing contract notes are documented in:

- `docs/48-selected-test-execution-filtering-and-observability.md`
