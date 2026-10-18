# Preserve SMT selection when compiling generated test runners

`-emit test` analyzes user source, builds a dispatch runner containing that
source, then analyzes the generated runner. The second analysis previously used
only target/debug options, silently disabling SMT. A recursive ghost function
could pass initial analysis but fail its decreases proof in the generated runner,
preventing every test from executing.

The CLI SMT selection now reaches generated-runner analysis. `-nosmt` remains
respected. Both the generated-runner artifact key and source-level early-cache
key include this setting, so neither layer can bypass the selected proof mode
by reusing an executable compiled under the opposite setting. This change does
not alter other generated-harness proof or permission policies.

A nine-line ghost recursion plus `@test assert true` reproduced the failure on
the frozen f295 compiler (`/tmp/elisa-runner-smt-before.txt`). Focused tests on the
candidate passed without skips in 2.196s (`/tmp/elisa-runner-smt-tests.txt`):

- CLI SMT mode executes the test and reports one pass.
- CLI no-SMT mode rejects the decreases obligation.
- Both executable-cache keys differ between SMT modes.
- Existing loaded-source snapshot and foreign-include cache checks pass.

This checkpoint is isolated. Full parser diagnostic execution and shared Core
integration remain separate steps; no Stage1 build or speed claim follows from
this test-framework correctness fix.
