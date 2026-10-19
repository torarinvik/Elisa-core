# Cache-to-parser token borrowing

The compiler's source-token cache previously copied every 104-byte Token into a parser-owned array, including ordinary reads that never rewrite a token. The cache now lends its immutable slice to the parser. A `borrowedTokens` bit makes `rewriteTokenKind` clone once before reserved-word recovery changes a kind. Existing `New([]Token)` callers retain their original ownership behavior. Both subparser token-span consumers still make independent slices before appending EOF or recovering. Cached bytes and pristine syntax snapshots remain unchanged.

`BorrowReadOnlyTokens` is an explicit trusted-frontend API contract: callers must not write into the slice or backing array. Go does not enforce read-only slices. Production use is restricted to the cache's borrowed parser path; legacy `lex` callers still get copies. The slice and its Token string pointers keep source/token backing alive even after cache eviction. Tests cover input mutation, eviction plus GC, repeated cached recovery, concurrent independent recovery, AST/diagnostic parity and syntax replay.

A typed TokenView alternative was rejected. It saved allocation but slowed parsing; compiler diagnostics found `advance` exceeded Go's inline budget (93 > 80) and gained a 432-byte out-of-line frame. Simplifying it restored inlining, but token-read overhead remained in profiles and timings were still mixed/slower. The accepted direct-slice version restores the original hot accessors rather than exchanging throughput for memory.

## Measurement

Baseline commit: 3b0cc522. Frozen files, SHA-256 manifest, paired test binary, raw logs, samples and CPU profiles: `/private/tmp/elisa-parser-token-view-20261019/`. Acceptance artifacts are `source-manifest-direct.json`, `direct-qualified-manifest.json`, `direct-pair.log`, `direct-medians.json`, `direct-runs.json`, and `direct-{baseline,candidate}-cpu.pprof`. The earlier typed-view runs are not acceptance evidence.

The same process contains the complete original parser package plus original cache/parse functions under test-only alternate names. Both use the same lexer and AST dependencies. Every leaf preflights AST/status parity before timing. Order is baseline/candidate/candidate/baseline, three repetitions per leaf (six samples per implementation), 100ms each. Go 1.27.2 darwin/arm64, GOMAXPROCS=4, GOMEMLIMIT=2GiB, shared benchmark locks held and no competing compiler run. This controls compiler workload contention, not all desktop/background activity.

| Frontend case | Baseline median | Borrowed median | B/op baseline → borrowed |
| --- | ---: | ---: | ---: |
| 256 functions, token-cache hit + parse | 1.025 ms | 0.846 ms (−17.5%) | 1.019 → 0.478 MB |
| 256 functions, edited parse | 1.339 ms | 1.089 ms (−18.7%) | 1.654 → 1.113 MB |
| 2,048 functions, token-cache hit + parse | 7.589 ms | 7.106 ms (−6.4%) | 8.121 → 3.853 MB |
| 2,048 functions, edited parse | 10.543 ms | 8.502 ms (−19.4%) | 12.770 → 8.488 MB |

A separate 2-second profiled token-cache sample at 2,048 functions corroborates the direction: 7.466 → 6.953 ms (−6.9%), allocation 8.121 → 3.853 MB. Recovery medians were −1.3% / −12.3% with effectively equal allocation (one additional small allocation), so COW did not produce a measured recovery slowdown. Cold controls were +4.5% / +1.5%, exact AST-snapshot controls −8.6% / +8.0%; these mixed controls show timing variance and are not improvements attributed to borrowing. Exact AST-snapshot hits bypass token handoff and parsing entirely.

This is frontend component evidence, not full Stage1 compile time or a semantic-check speedup. Token storage remains contiguous and rich, edited lexing still copies/rebases a new revision, and syntax replay still copies owned AST nodes. CPU profiles now identify parser `peek` and reflection-based auto-region scans as useful next component targets.

## Validation

The source-matched direct binary passed all selected SourceToken, SourceSyntax and compile-server token/syntax/edited replay tests (0.737s). Full parser/lexer package tests passed before timing. Final race validation is recorded in `/private/tmp/elisa-parser-token-view-20261019/direct-final-race.log`. After timing, the only additional tracked lexer edit clarified its API ownership comment; executable logic and the measured direct-source files are unchanged.
