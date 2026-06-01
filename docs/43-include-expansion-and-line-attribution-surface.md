# Include expansion and line-attribution surface

This document records the current include expansion behavior used before parsing/analyzing Elisa source.

## Accepted include directive forms

Current accepted forms are:

```elisa
include "mid.elisa"
# include "mid.elisa"
{$I mid.elisa}
{$INCLUDE 'mid.elisa'}
```

Notes:

- `# include "..."` and bare `include "..."` both work.
- Pascal form supports `I` and `INCLUDE` case-insensitively.
- Pascal form accepts quoted or unquoted path token when it has no whitespace.

## Path resolution

- Relative include paths are resolved from the including file directory.
- Absolute include paths are used verbatim.
- Include tracking uses absolute paths internally.

When project/dependency expansion supplies include directories, resolver probes:

1. `<including-dir>/<include-path>`
2. each configured include directory joined with `<include-path>`

If no candidate exists, current diagnostic shape is:

```text
include "<path>" not found from <including-dir> or configured include directories
```

## Expansion model

- Expansion is recursive.
- A file is included once per expansion pass (`included` set).
- Active recursion stack is tracked to detect include cycles.

Cycle diagnostic text currently starts with:

```text
cyclic include detected for <absolute-path>
```

## Indentation preservation

If an include appears in an indented block, inserted lines are prefixed with that indentation.

Special case:

- generated `#line` directives are kept at column zero and are not indented.

## Line attribution directives

Expansion inserts source-attribution lines:

```text
#line <line> <absolute-path>
```

Behavior:

- one directive at start of each file expansion
- after returning from an include, attribution resumes to parent file with `#line <next-line> <parent-abs-path>`

## Example

Source:

```elisa
# root.elisa
module M:
    include "leaf.elisa"

def main() -> int:
    return M::foo()
```

```elisa
# leaf.elisa
def foo() -> int:
    return 7
```

Expanded semantic content (with `#line` lines removed) is:

```elisa
module M:
    def foo() -> int:
        return 7

def main() -> int:
    return M::foo()
```

## Related docs

- `docs/35-pipeline-and-introspection-emits.md`
- `docs/41-project-deps-report-schema.md`
