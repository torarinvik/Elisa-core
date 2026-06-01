# C bind layout check and JSON manifest surface

This document records the current `-emit c-bind-check` and `-emit c-bind-check-json` behavior.

## Emit names and aliases

- `c-bind-check`
  - aliases: `cbind-check`, `c-bind`, `cbind`
- `c-bind-check-json`
  - aliases: `cbind-check-json`, `c-bind-json`, `cbind-json`, `ffi-manifest`, `abi-manifest`

## Source eligibility

Only structs with C bind metadata are checked:

- `@c_bind(...)`
- `@c_bind_prefix(...)`

## Text-mode output

Success lines:

- full layout:
  - `c-bind-check: <ElisaName> matches <CName> from <Header> (size=<n> align=<n> fields=<n>)`
- prefix layout:
  - `c-bind-check: <ElisaName> prefix matches <CName> from <Header> (fields=<n>)`

No bind structs case:

```text
c-bind-check: no @c_bind structs
```

## JSON mode output

Schema:

```json
{
  "version": 1,
  "target_triple": "<value>",
  "structs": [
    {
      "elisa_name": "Header",
      "c_name": "struct Header",
      "header": "/abs/or/system/header",
      "prefix": false,
      "elisa": {"size": 16, "align": 8},
      "c": {"size": 16, "align": 8},
      "fields": [
        {"name": "count", "elisa_offset": 4, "c_offset": 4}
      ]
    }
  ]
}
```

If there are no bind structs, JSON output still emits:

- `version: 1`
- `target_triple`
- empty `structs` array

## Validation rules

For normal `@c_bind` structs:

- size must match
- align must match
- each field offset must match

For `@c_bind_prefix` structs:

- size and align checks are skipped
- declared field offsets must still match

## Failure diagnostics

Current failures aggregate under:

```text
C binding layout check failed:
...
```

Mismatch lines can include:

- missing C probe result
- size mismatch
- align mismatch
- missing C `offsetof` result
- field offset mismatch

## Probe compilation/runtime details

- C probe is compiled with `cc` by default, or `CC` env var override.
- `CPPFLAGS` and `CFLAGS` are shell-split and appended.
- `-target-triple` influences target compile/run path.

## Elisa example

```elisa
@c_bind("/tmp/fixture.h", "struct Header")
struct Header layout c:
    tag: u8
    count: u32
```

```bash
go run ./src -emit c-bind-check sample.elisa
go run ./src -emit c-bind-check-json -target-triple x86_64-apple-darwin sample.elisa
```

## Related docs

- `docs/07-export-and-c-abi.md`
- `docs/08-ffi-and-c-bindings.md`
- `docs/34-cli-emit-mode-catalog.md`
