# Working on the Stage0 compiler

Start with the [repository README](../README.md) for prerequisites and a first Elisa program. See the [language and CLI notes](language-and-cli.md) for syntax examples, project manifests, and benchmark recipes.

## Source layout

| Path | Purpose |
| --- | --- |
| [`src/`](src/) | CLI, lexer, parser, semantic analysis, and LLVM code generation |
| [`runtime/elisacore_std/`](runtime/elisacore_std/) | Elisa runtime and standard library |
| [`test/`](test/) | Go-based compiler fixture suites |
| [`../Code/test_programs/`](../Code/test_programs/) | Elisa programs used by integration tests |
| [`scripts/`](scripts/) | Build tooling, validation, and benchmark drivers |
| [`githooks/`](githooks/) | Optional local Git hooks |

The runtime entry point is [`elisacore_runtime.elisa`](runtime/elisacore_std/elisacore_runtime.elisa).

## Build and test

Run these commands from `compiler/`.

| Command | Purpose |
| --- | --- |
| `make build` | Build the worktree-local compiler at `bin/elisac` |
| `make test` | Run the fast compiler and fixture suites |
| `make test-prover` | Run semantic analysis and SMT solver tests |
| `make test-full` | Run all tests, including the slower native integration tests |
| `make help` | List supported Make targets |

Build and install targets enforce a five-second wall-clock limit. To use a specific Z3 executable, set `ELISA_Z3` at runtime. See the [incremental build design](../docs/117-incremental-build-system.md) for compiler cache details.

The [contributor instructions](../AGENTS.md) cover source-file size and rebuild timing.
