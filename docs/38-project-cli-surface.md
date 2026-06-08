# Project CLI surface

This note documents current Elisa-core project command behavior.

## Commands

```sh
elisacore init <name> [--path <dir>]
elisacore init-lib <name> [--path <dir>]
elisacore build [target] [--project <dir|project.json>] [options]
elisacore run [target] [--project <dir|project.json>] [options]
elisacore test [target] [--project <dir|project.json>] [options]
elisacore bench [target] [--project <dir|project.json>] [options]
elisacore project view [target] [--project <dir|project.json>]
elisacore project deps [target] [--project <dir|project.json>] [--json]
elisacore project abi-lint [target] [--project <dir|project.json>] [--json] [--strict-contracts]
elisacore project easm-lint [target] [--project <dir|project.json>] [--json]
```

## Scaffolding

`init` creates a project root with:

- `src/` (including starter `main.elisa`)
- `build/`
- `lib/`
- `native/`
- `test/`
- `elisacore.project.json` with default `app` target

`init-lib` creates `<name>.elisalib/` with manifest and starter library source.

Detailed scaffolding contract notes:

- `docs/61-project-scaffolding-surface.md`

## Target config highlights

Project target resolution supports:

- `entry`
- `emit`
- `run-emit`
- `output`
- `target-triple`
- `opt`
- `warnings.strict`
- `warnings.perf`
- `warnings.concurrency`
- `foreign`
- `easm`
- `link-flags`
- dependency and include composition from project plus libraries
- strict JSON decoding rejects unknown fields and trailing garbage in project/manifest files

`-emit` on project commands overrides target `emit`.

Example project file shape:

```json
{
  "version": "0.1.0",
  "dependency-search-paths": ["lib"],
  "dependencies": ["mathcore"],
  "include-dirs": ["shared"],
  "foreign": ["native/app_runtime.c"],
  "link-flags": ["-lm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "run-emit": "interpret",
      "output": "build/app.ll",
      "opt": "O0",
      "warnings": {
        "strict": true,
        "perf": true,
        "concurrency": true
      }
    }
  }
}
```

## Build and run semantics

- `build` uses target `emit` and `output`
- `run` uses target `run-emit`
- `test` runs runner emit behavior on selected target entry
- `bench` lists benchmark-annotated functions for selected target

## Dependency and view surfaces

`project view` prints resolved target information including dependency and link
metadata.

Detailed `project view` output contract:

- `docs/60-project-view-output-contract.md`

`project deps` prints resolved source and native dependency inputs; `--json`
emits a structured dependency report including:

- target name
- emit and run-emit modes
- resolved dependency manifests
- foreign source list
- link flags

Full JSON field schema is documented in
[41-project-deps-report-schema.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/41-project-deps-report-schema.md).

Native lint command details for `project abi-lint` and `project easm-lint` are
documented in
[39-project-native-lint-surfaces.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/39-project-native-lint-surfaces.md).

## Option behavior highlights

- `-filter` is supported for project `test` and `bench` flows
- `--json` is supported on `project deps`, `project abi-lint`, and `project easm-lint`
- `-link`, `-L`, and `-l` flags are appended to resolved target link flags
- `-target-triple` overrides target triple for the current command
- `-O0`, `-O2`, and `-O3` override target optimization for the current command
- `-Wstrict` enables the shipped strict analysis levers together: unsafe
  permission enforcement, progress-safety analysis, `-Wperf`, and
  `-Wconcurrency`
- `-Wperf` and `-Wconcurrency` promote the same warning classes as target
  `warnings.perf` and `warnings.concurrency`
- Target warning policy can only make a build stricter: `warnings.perf: true`
  promotes performance-friction diagnostics to errors and
  `warnings.concurrency: true` promotes legacy raw-concurrency diagnostics to
  strict errors
- `warnings.strict: true` applies the same preset as `-Wstrict` for that target

## Trust gates for hooks

Project and target exec hooks are gated by trust mode:

- `--trust=none`
- `--trust=include`
- `--trust=full`

Without sufficient trust, hook execution is rejected. With `--trust=full`, hook
commands run and are traced on stderr.

Current hook rule is strict: project, target, and dependency `exec` hooks all
require `--trust=full`. `--trust=include` is parsed but does not execute hooks.

Detailed schema and resolution rules are documented in:

- `docs/59-project-manifest-and-resolution-contract.md`

## Native integration behavior

Resolved project targets can include foreign C sources and link flags for build,
run, and test flows. JSON dependency output reflects inherited and target-local
native inputs, including when target config opts out of project-wide native
inheritance.

Per-target native inheritance can be controlled with target settings such as
`"inherit-project-native": false` so project-wide `foreign` and `link-flags`
inputs are excluded for that target.
