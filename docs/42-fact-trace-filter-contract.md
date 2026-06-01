# Fact-trace filter contract (`-emit facts` / `-emit fact-trace`)

This document records the current implemented filter contract for fact reports.

## Supported emits and aliases

- Canonical emit: `facts`
- Accepted aliases: `fact`, `fact-trace`, `trace-facts`

## Filter grammar

Filter input is parsed as terms separated by comma or whitespace.

Each term must be:

```text
key=operator:value
```

Both `key` and `operator` are normalized to lowercase before matching.

## Supported keys

- `alias`
- `class`
- `detail`
- `effect`
- `format`
- `function`
- `kind`
- `mode`
- `path`
- `reason`
- `region`
- `source`
- `sourcekind`
- `store`
- `target`
- `verb`

## Supported operators

- `contains`
- `eq`
- `regex`

## Matching semantics

- Multiple terms for the same key are OR-matched.
- Different keys are AND-matched.
- `verb` behaves like `kind`.
- `function` filters by function name before report rendering.
- `mode=eq:summary` enables summary mode.
- `mode=eq:compact` is also accepted as summary mode.
- `format=eq:json` enables JSON output mode.

## Transform-key behavior

- `kind` / `verb`: compares transform kind.
- `class`: matches any transform class.
- `target` / `path`: matches transform target text.
- `source`: matches transform source text.
- `sourcekind`: matches transform source kind.
- `reason`: matches transform reason text.
- `detail`: matches `name=value` pairs from transform detail fields.
- `alias`: matches target text or formatted transform text.
- `effect`: matches target text and requires `effects` class.
- `region`: matches target text and requires `region-deps` class.
- `store`: matches target or source text and requires `store-deps` class.

## Snapshot-only fallback matching

When transform filtering produces zero transforms for a function, the function can still be included for these keys:

- `alias`
- `effect`
- `target`
- `path`
- `region`
- `store`

That fallback matches against alias sets, effect summary, and fact snapshot candidates.

## Contract line in text output

Text output starts with:

- `=== facts ===`
- a `contract:` line containing version, order, summary selector, json selector, supported matchers, and supported filters.

Current version string is `fact-trace-v2`.

## JSON output shape

JSON mode emits:

- `version`
- `mode` (`full` or `summary`)
- `format` (`json`)
- `filters`
- `matchers`
- `functions[]`

Each function item includes:

- `name`
- `snapshot`
- `exits`
- `aliases`
- `effects`
- `summary`
- `transforms` (full mode)
- `text_summary` (full mode)

## Diagnostics for invalid filters

Current parse/validation errors include:

- malformed term missing `=`
- malformed term missing `operator:value`
- empty key/value or empty operator/value
- unsupported key
- unsupported operator
- invalid regex pattern

## Elisa example and filter examples

```elisa
struct Player:
    health: i32

def evolve(mut p: Player) -> Player:
    p.health = p.health + 1
    return p
```

```bash
go run ./src -emit facts -filter "function=contains:evolve,mode=eq:summary" sample.elisa
go run ./src -emit facts -filter "kind=eq:recompute,class=eq:typestate" sample.elisa
go run ./src -emit facts -filter "target=regex:^p\\.,format=eq:json" sample.elisa
```

## Related docs

- `docs/22-value-fact-core.md`
- `docs/34-cli-emit-mode-catalog.md`
- `docs/35-pipeline-and-introspection-emits.md`
- `docs/37-compile-server-api-surface.md`
