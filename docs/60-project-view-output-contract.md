# Project view output contract (`elisacore project view`)

This document captures the current text output contract for `project view`.

## Command

```sh
elisacore project view [target] [--project <dir|project.json>]
```

## Output sections

Current output includes:

- `Project: <project-file-path>`
- `Targets (<count>):` list
- selected target block

Selected target block fields:

- `Selected target: <name>`
- `Entry: <path>`
- `Build emit: <mode>`
- `Run emit: <mode>`
- `Target triple: <value-or-<host-default>>`
- optional `Output: <path>` when configured
- optional `Optimization: O<n>` when explicitly configured
- `Include dirs:` list
- `Dependency search paths:` list
- `Resolved dependencies:` list
- optional `Foreign sources:` list
- optional `EASM sources:` list
- optional `Link flags:` list
- `Exec hooks: project=<n> target=<n> dependencies=<n>`

## Target summary list

The initial target list is sorted by target name and prints rows:

```text
- <target> entry=<entry> emit=<emit-or-default> run=<run-emit-or-default>
```

Defaults in this list:

- build emit default `llvm`
- run emit default `interpret`

## Dependency display

Each resolved dependency row includes:

- dependency name and directory
- optional interface path line
- optional foreign source list
- optional EASM source list

## Related docs

- `docs/38-project-cli-surface.md`
- `docs/59-project-manifest-and-resolution-contract.md`
