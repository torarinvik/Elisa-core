# C archive output surface

This note documents implemented `-emit c-archive` behavior in Elisa-core.

## Command

```sh
go run ./src -emit c-archive -O0 -o libexample.a path/to/source.elisa
```

## Expected artifact set

Current `c-archive` emit produces:

- static archive output (`.a`)
- ABI manifest JSON (`.elisa-abi.json`)
- generated C header (`.h`)
- unsafe summary text (`.unsafe.txt`)

The emit writes artifacts to files and does not stream archive bytes to stdout.

## Example exported surface

```elisa
def first_byte_impl(text: u8&) -> i64:
    trusted Unsafe.UncheckedIndex:
        return text[0.usize()].i64()

export func elisa_first_byte(text: u8&) -> i64 = first_byte_impl
```

## Manifest expectations

Current manifest includes runtime and export metadata such as:

- runtime inclusion marker
- exported function list
- ABI contract text for checked-in C or C++ header compatibility

Use this output mode for native embedding workflows that need one archive plus
machine-readable ABI metadata.
