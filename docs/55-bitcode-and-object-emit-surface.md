# Bitcode and object emit surface (`-emit bc` / `-emit obj`)

This document captures current artifact-mode behavior for LLVM bitcode and object output.

## Commands

```sh
go run ./src -emit bc path/to/file.elisa
go run ./src -emit obj path/to/file.elisa
go run ./src -emit bc -o out/module.bc path/to/file.elisa
go run ./src -emit obj -o out/module.o path/to/file.elisa
```

## Output path behavior

- `-emit bc` default output path uses input basename with `.bc`
- `-emit obj` default output path uses input basename with `.o`
- `-o` overrides destination path
- parent directories are created automatically when needed

## Stream behavior

- these emits write binary artifacts to files
- they do not stream artifact payload bytes to stdout

## Optimization default

Without explicit `-O`:

- both `bc` and `obj` run at default optimization level `O3`

Explicit `-O0`, `-O2`, `-O3` overrides this behavior.

## Target behavior

- object emission honors `-target-triple` when provided
- bitcode/object generation uses packed-lowering profile plumbing consistent with compiler backend defaults

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/44-cli-argument-normalization-and-defaults.md`
- `docs/35-pipeline-and-introspection-emits.md`
