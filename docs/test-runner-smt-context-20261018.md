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


## Qualified packed-store follow-up

Commit `ea51f519` fixes recovered implicit store arguments for qualified enums.
If expression metadata is missing, lowering uses the exact store type from the
callee parameter. Existing expression store types retain priority. Sanitized
synthetic names are not reversed to guess enum identity.

The focused qualified-store regression and existing independent qualified-store
capture test pass (0.372s, no skips). With both fixes, the external self-hosted
parser fixture `test/parity/when_disjoint_diagnostics.elisa` executes both tests:
diagnostic order/spans and every overlapping pair. The runner reports
`passed=2 skipped=0 failed=0` (50.622s including test setup/compilation).
Evidence is retained under `build/verification/qualified-packed-store-20261018/`.
This used the live external parser source without a full dependency snapshot;
it is a behavioral check, not a frozen bootstrap gate or a rebuild benchmark.
The temporary Go adapter was removed after execution. Shared integration remains
pending coordination with the current Core build owner.
