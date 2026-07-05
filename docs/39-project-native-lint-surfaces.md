# Project native lint surfaces

This note documents current `project abi-lint` and `project easm-lint`
surfaces.

## ABI lint command

```sh
elisacore project abi-lint app --project /path/to/project
elisacore project abi-lint app --project /path/to/project --json
elisacore project abi-lint app --project /path/to/project --strict-contracts
```

## ABI lint report fields

Text mode includes:

- project path
- target name
- entry path
- emit and run emit
- target triple
- foreign sources
- scanned native files
- ABI contracts
- link flags
- issue list

JSON mode includes fields such as:

- `project`
- `target`
- `entry`
- `emit`
- `runEmit`
- `targetTriple`
- `foreign`
- `scanned`
- `linkFlags`
- `contracts`
- `issues`

## ABI lint issue examples

Current issue codes include:

- `inline-asm-positional-abi-operands`
- `inline-asm-stack-without-memory-clobber`
- `guest-entry-call-mangles-stack`
- `guest-entry-jump-not-noreturn`
- `guest-entry-no-scratch-register-parking`
- `missing-guest-entry-abi-contract` (strict mode)
- `guest-entry-target-not-x86_64` (strict mode)
- `native-source-read-failed`
- `native-include-read-failed`

Quoted native includes are scanned recursively for linting when files resolve on
disk.

Source types scanned include native `.c/.cc/.cpp/.cxx` plus header files
`.h/.hpp/.hh` reached by quoted include expansion.

## ABI lint suppressions

Inline asm blocks can declare explicit allow markers:

- `ELISA_ABI_LINT_ALLOW(<issue-code>)`
- `ELISA_ABI_LINT_ALLOW(all)`

These suppress matching lint checks for that block.

## Strict contracts mode

With `--strict-contracts`, guest-entry-like native runtime files require an ABI
contract marker such as:

```c
/* ELISA_ABI_CONTRACT guest_entry x86_64 ps4_process_entry noreturn */
```

Missing contract markers in guest-entry-like files become lint errors.

Without explicit target triple and with native foreign sources present, ABI lint
also reports an informational default-target issue (`target-triple-defaulted`).

## EASM lint command

```sh
elisacore project easm-lint app --project /path/to/project
elisacore project easm-lint app --project /path/to/project --json
```

Current behavior:

- scans target EASM files plus dependency EASM files
- reports target triple, file list, module summaries, exports, and issues
- returns non-zero when report contains error-severity issues

For the EASM source language and verifier contract itself, see
[40-easm-language-and-verifier-surface.md](40-easm-language-and-verifier-surface.md).

Text report includes sections like:

- `Target triple: ...`
- `EASM files (N):`
- `Module <name> target=<target>`
- `export <symbol> ...`
- `Issues:`
