# Project manifest and resolution contract

This document captures current `project.json` and dependency `manifest.json` resolution behavior.

## File names

- project file: `project.json`
- dependency manifest: `manifest.json`
- dependency library directory suffix convention: `.elisalib`

## Project JSON schema (current)

Top-level project fields:

- `version`
- `dependency-search-paths`
- `dependencies`
- `include-dirs`
- `foreign`
- `easm`
- `link-flags`
- `exec`
- `targets` (required map)

Per-target fields:

- `entry` (required)
- `emit`
- `run-emit`
- `output`
- `dependencies`
- `include-dirs`
- `foreign`
- `easm`
- `link-flags`
- `inherit-project-native`
- `exec`
- `opt`
- `target-triple`
- `packed-abi` (rejected; removed override)

## Dependency manifest schema (current)

Manifest fields:

- `provides` (required name)
- `entry`
- `interface`
- `dependencies`
- `include-dirs`
- `foreign`
- `easm`
- `link-flags`
- `exec`

## JSON validation mode

Project and manifest JSON decoding currently uses strict unknown-field rejection (`DisallowUnknownFields`) and rejects trailing data.

## Target selection rules

- explicit CLI target name is used when provided
- otherwise `default` target is used if present
- otherwise if there is exactly one target, that target is selected
- otherwise the lexicographically smallest target name is selected

## Source path constraints

Current target `entry`, manifest `entry`, and manifest `interface` must resolve to `.elisa` or `.elisai` files.

## Dependency discovery and ordering

Search roots come from `dependency-search-paths` (default `["lib"]`).

For dependency `name`, resolver probes:

- `<search>/name.elisalib/manifest.json`
- `<search>/name/manifest.json`

Dependency resolution is recursive, caches manifests by name, and rejects cycles.

Build order is dependency-first topological order.

## Native and include composition

Resolved target aggregates:

- project + target + dependency include dirs
- project + target + dependency foreign sources
- project + target + dependency EASM sources
- project + target + dependency link flags

All lists are deduplicated after composition.

`inherit-project-native: false` removes project-level `foreign`, `easm`, and `link-flags` from that target before dependency merge.

## Hook trust behavior

Hook sources:

- project `exec`
- target `exec`
- dependency manifest `exec`

Current rule:

- any hook requires `--trust=full`
- `--trust=include` is parsed but does not permit hook execution

## Build input assembly

Resolved compile source is assembled as:

- dependency `entry` when present, otherwise dependency `interface` when present
- then target entry

Each source is include-expanded with configured include directories and concatenated.

## Example

```json
{
  "version": "0.1.0",
  "dependency-search-paths": ["lib"],
  "dependencies": ["mathcore"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "run-emit": "interpret"
    }
  }
}
```

```elisa
def main() -> int:
    return 42
```

## Related docs

- `docs/38-project-cli-surface.md`
- `docs/41-project-deps-report-schema.md`
- `docs/43-include-expansion-and-line-attribution-surface.md`
