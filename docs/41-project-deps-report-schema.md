# Project dependency report schema

This note documents the current JSON schema shape emitted by:

```sh
elisacore project deps <target> --project <dir|project.json> --json
```

## Top-level fields

Current report includes:

- `project`
- `target`
- `entry`
- `emit`
- `runEmit`
- `output`
- `targetTriple`
- `includeDirs`
- `dependencySearchPaths`
- `sources`
- `foreign`
- `easm`
- `linkFlags`
- `dependencies`

## Dependency entries

Each item in `dependencies` currently includes:

- `name`
- `manifest`
- `entry`
- `interface`
- `includeDirs`
- `foreign`
- `easm`
- `linkFlags`
- `sources`

## Source ordering behavior

- `sources` is deduplicated
- source order follows dependency/interface and target-entry traversal order
- include expansion follows project include resolution rules

## Example skeleton

```json
{
  "project": "/abs/path/elisacore.project.json",
  "target": "app",
  "entry": "/abs/path/src/main.elisa",
  "emit": "llvm",
  "runEmit": "interpret",
  "targetTriple": "x86_64-apple-darwin",
  "includeDirs": ["/abs/path/shared"],
  "dependencySearchPaths": ["/abs/path/lib"],
  "sources": ["/abs/path/lib/mathcore.elisalib/src/mathcore.elisai", "/abs/path/src/main.elisa"],
  "foreign": ["/abs/path/native/app_runtime.c"],
  "easm": ["/abs/path/easm/spin.easm"],
  "linkFlags": ["-lm"],
  "dependencies": [
    {
      "name": "mathcore",
      "manifest": "/abs/path/lib/mathcore.elisalib/elisacore.manifest.json",
      "entry": "/abs/path/lib/mathcore.elisalib/src/mathcore.elisa",
      "interface": "/abs/path/lib/mathcore.elisalib/src/mathcore.elisai",
      "includeDirs": ["/abs/path/lib/mathcore.elisalib/shared"],
      "foreign": ["/abs/path/lib/mathcore.elisalib/native/mathcore_runtime.c"],
      "easm": ["/abs/path/lib/mathcore.elisalib/easm/clock.easm"],
      "linkFlags": [],
      "sources": ["/abs/path/lib/mathcore.elisalib/src/mathcore.elisai"]
    }
  ]
}
```

## Related docs

- command-level overview: [38-project-cli-surface.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/38-project-cli-surface.md)
- native lint reports: [39-project-native-lint-surfaces.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/39-project-native-lint-surfaces.md)
