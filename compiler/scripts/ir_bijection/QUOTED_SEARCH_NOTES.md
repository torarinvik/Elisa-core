# Bounded quoted-token search and token reuse — source-only candidate

quoted_end now uses bytes.find for the first raw quote, LF/CR within that bound, and only the backslash positions before that bound. Supported LLVM quote escapes are two hex digits, not backslash-quote, so a raw quote is always a terminator. Doubled backslashes still consume exactly2 bytes. Hex consumes exactly3. The scanner never accepts an escape crossing the raw quote. Invalid escape versus newline error ordering is preserved by checking only escapes preceding the first raw newline, before reporting that newline. Missing raw quotes still validate preceding escapes/newlines in the original order before unterminated failure. No regex/backtracking or string rewriting is introduced.

module_index already had significant tokens for each module line. It now reuses those tokens for the EXACT same private/internal linkage predicate through internal_global_names; standalone internal_globals still lexes/group-by-line as before. No linkage rule or ABI permission changes.

Tests drafted: exhaustive small byte alphabet up to4 suffix bytes compared to a frozen previous scanner, including exact error messages; bounded first terminator with invalid suffix afterward; exact literal bytes and1MiB ordinary literal; standalone/shared token linkage parity and module index parity. Not run by author.

Root benchmark plan: compare quoted_end on1MiB ordinary strings, escaped-backslash-heavy strings and short names; separately compare module_index on immutable real input under external30s cap. Then run existing+new tests and the real checkpoint verifier with a NEW checkpoint path after source change. Checkpoint source-hash binding intentionally rejects all old v1/v2 checkpoints; do not rekey them. These changes may reduce startup/scanner cost but no timing or qualification gain has been measured.

Root validation: all49 tests pass. Real warm Stage1 IR module-index A/B/B/A
observations are4.433567/2.574209/2.592342/4.475762seconds, with exactly equal
676-type and64,457-private-global sets. External30second cap. Raw artifact
`/tmp/elisa-ir-quoted-find-real-index-benchmark.json`. Full pair still pending.
