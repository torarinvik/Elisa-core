# Reference doc generation surface (`-emit doc`)

This document captures the current generated-reference contract for `-emit doc`.

## Command

```sh
go run ./src -emit doc path/to/file.elisa
```

## Header and summary

Generated output starts with:

- `# Reference: <basename>`
- `Generated from <full path>`
- `## Summary`
- top-level declaration count
- formatter note indicating structural AST rendering

## Section mapping by declaration kind

Current generator emits dedicated sections for:

- namespace
- permission
- error set
- using
- constant
- const enum
- global
- struct
- enum
- packed enum
- function
- extern function
- extern variable
- extern type
- exported type
- exported function
- exported global
- static if
- static assert
- static assert block
- static generate

Unsupported/unknown declarations fall back to a generic `Declaration` section with a surface headline.

## Section content rules

- each section includes a `declaration:` line derived from the first non-annotation source headline
- struct sections include annotations and field list
- enum sections include annotations, optional common fields, and variant list
- function sections include annotations and body statement count
- namespace sections recurse into nested declarations
- static sections include compact structural counts or condition text

## Formatting helpers and limitations

- declaration formatting comes from unparser output
- comments are not preserved in generated reference output
- annotation lines are preserved as formatted `@name(args...)` entries
- heading depth increases inside namespaces and is capped at markdown level `######`

## Elisa example

```elisa
namespace util:
    const LIMIT: i64 = 16

struct Pair:
    left: i64
    right: i64

@test
def build_pair(value: i64) -> Pair:
    return Pair(value, value)
```

Representative emitted sections include:

- `## Namespace util`
- `## Struct Pair`
- `## Function build_pair`
- declaration lines and field/annotation lists

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/35-pipeline-and-introspection-emits.md`
