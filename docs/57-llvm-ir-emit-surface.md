# LLVM IR emit surface (`-emit llvm`)

This document captures the current compiler-mode LLVM text IR emit behavior.

## Command

```sh
go run ./src -emit llvm path/to/file.elisa
go run ./src -emit llvm -O3 path/to/file.elisa
go run ./src -emit llvm -o module.ll path/to/file.elisa
```

## Current behavior

- analyzes source and lowers through LLVM backend
- emits textual LLVM IR
- stdout by default, or file output when `-o` is provided

## Optimization and lowering profile

- LLVM emission uses active optimization level (`-O0`, `-O2`, `-O3`)
- default optimization level for `llvm` is `O0` when no explicit `-O` is supplied
- packed lowering profile is applied through backend generation path

## Input forms

- accepts `.elisa` source input
- accepts `.elisair` frontend bundle input through loader path

## Failure behavior

- parse/semantic/backend failures return nonzero
- stderr includes `error: ...` diagnostics

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/35-pipeline-and-introspection-emits.md`
- `docs/51-frontend-ir-bundle-contract.md`
