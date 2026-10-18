# Reuse the parse request's exact source digest

A cached parse request computes `(filename, SHA-256(source))` once and passes the
key through private cache-probe, lexer and parsed-snapshot admission helpers.
The source bytes must stay immutable for the synchronous request. Standalone
helpers keep their existing signatures and hashing behavior; uncached parsing
does not gain a digest calculation. Filename ownership, token-copy ownership,
cache limits, lazy syntax admission and diagnostic registration remain unchanged.

Previously a parse-cache miss hashed again in lexing; when a syntax trace was
available, parsed-snapshot admission hashed a third time. Exact syntax-cache hits
already hashed once and still do.

Qualification uses clean Core 6202047e in an isolated checkout. Existing source
cache/suffix replay tests pass. An overlay restores the two original runtime
files for the baseline, so the benchmark workload is identical. The benchmark
models a roughly 1.8 MB loaded source with a large comment and novel middle
literal edits; source construction is untimed. Three 300 ms, one-CPU Apple M5
samples measured baseline 13.19–14.25 ms and candidate 9.97–12.10 ms. Both used
about 49.58 MB and 70 allocations per request. This is a focused parse-cache
workload, not a whole-project compile-speed claim; the earlier lineage-branch
comparison was noisy, so the range should not be generalized.

The standalone port has no AST lineage, semantic manifest, or analysis-session
changes. It can be integrated independently of those opt-in experiments and of
the separate in-progress semantic admission changes.

Final exact-source race qualification passed in 1.891 s. The single hash stays
inside the existing replay timer so parse-hit timing still includes its cost.
