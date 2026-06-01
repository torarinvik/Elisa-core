# Notice emission and deprecation suppression surface

This document records how compiler notices are emitted and how deprecation notices can be suppressed.

## Default notice behavior

After semantic analysis, notice lines from `result.Notices()` are written to stderr.

This happens in:

- normal analyze paths
- loaded-program analyze paths
- parse-only emits that explicitly call semantic warning emission (`ast`, `fmt`, `doc`)

Errors are still emitted normally and stop successful compilation paths.

## Suppression environment variable

`ELISACORE_SUPPRESS_DEPRECATED_WARNINGS=1` enables targeted suppression.

Suppression rule:

- only notice lines containing the substring `deprecated` are suppressed
- non-deprecation notices are still emitted

If the variable is unset or set to any value other than `1`, no suppression is applied.

## Scope of suppression

The suppression helper is used from CLI and shared program-loading/analyze paths, so behavior is consistent across those paths.

## Example

```bash
ELISACORE_SUPPRESS_DEPRECATED_WARNINGS=1 go run ./src -emit semantic sample.elisa
```

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/35-pipeline-and-introspection-emits.md`
- `docs/37-compile-server-api-surface.md`
