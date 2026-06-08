# Project scaffolding surface (`init` / `init-lib`)

This document captures current scaffolding behavior for Elisa project bootstrap commands.

## Commands

```sh
elisacore init <name> [--path <dir>] [--strict]
elisacore init-lib <name> [--path <dir>]
```

## `init` output shape

Current `init` scaffolds `<path>/<name>/` with:

- `project.json`
- `src/main.elisa`
- `build/`
- `lib/`
- `native/`
- `test/`

`src/main.elisa` is a starter executable surface and `project.json` includes a default build target.

`init --strict` writes `warnings.strict: true` on the generated `app` target,
which enables the unified strict preset for the project from the first build.

## `init-lib` output shape

Current `init-lib` scaffolds `<path>/<name>.elisalib/` with:

- `manifest.json`
- `src/<name>.elisa`
- `src/<name>.elisai`
- `native/`
- `README.md`

The generated manifest includes an `interface` field pointing at:

```text
src/<name>.elisai
```

## Typical starter Elisa snippets

```elisa
def main() -> int:
    return 0
```

```elisa
extern core_seed() -> int
```

## Related docs

- `docs/38-project-cli-surface.md`
- `docs/59-project-manifest-and-resolution-contract.md`
