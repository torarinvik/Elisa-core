# Lexer component iteration

Work on lexer throughput and edit reuse independently before whole compiler validation. The component gate is `GOMAXPROCS=4 GOMEMLIMIT=2GiB go test -p 1 ./src/lexer` from `compiler/`. Add `-bench 'BenchmarkSource' -benchmem` for the matrix. Use longer repeated samples for performance decisions; short samples only validate the harness.

The benchmarks construct revisions and check cold/incremental token and diagnostic parity before timing. Timed work includes source ownership, scanning, checkpoint construction, token copying and rebasing. Metrics distinguish prefix/suffix reuse, scanned tokens, transient allocation, token capacity, and conservative retained payload (not RSS). `scanned-tokens/op` counts scanner output only: reused tokens still require copying/rebasing into the new revision. MB/s normalizes by full input length and is not scanner bandwidth for retained edits; use ns/op as the primary latency measure. Distinct deletions and incomplete comments cover editor fallback paths. The old early-shift benchmark also excludes edit formatting from timing now.

Direct unchanged tokenization is component-only: the production parsed-file cache can bypass tokenization and parsing, but still copies the AST. These figures are not whole compiler request latency.

A speculative capacity candidate reduced early/middle edit allocation by 36–43% on a dense 512 KiB corpus, with mostly flat or modestly lower medians. It was rejected before promotion: a middle-of-file unterminated comment took a median 4.804 ms versus 3.803 ms, allocating 35.27 MB versus 19.10 MB. Its eager full-size reserve triggered a second compaction copy. Runtime code remains unchanged by this benchmark commit. The rejected patch is saved locally at `/private/tmp/elisa-lexer-capacity-rejected-20261019.patch`; raw fallback evidence is `/private/tmp/elisa-lexer-mid-comment-20261019.log`.

Next candidate should defer prefix/output materialization until convergence or EOF determines the required size, preserving independent source/token ownership. Accept only after package parity tests and repeated normal-edit/deletion/incomplete-input measurements. No full semantic gate is needed for this isolated benchmark change.

## Accepted deferred prefix candidate

Large revisions with a possible reusable suffix borrow the prior prefix only inside a local construction builder. At confirmed convergence or EOF, the builder creates independent token backing and rebinds the prefix views once. Absolute token counts still govern line epochs and checkpoints. Failed suffix replay truncates appended tokens/epochs after detaching the prefix, and subsequent scanning continues with the correct count. Revisions of at most 2,048 prior tokens, and edits with no suffix candidate, keep direct output buffering. Initial lexing has a separate scanner loop without builder bookkeeping. Final-prefix checkpoints skip suffix discovery when fewer than 256 old tokens remain, since replay already requires that minimum.

The final source is pinned by SHA-256: `incremental_source.go` c1a13bde1bfc0fb37b09dde1f3e7268efe384fc3ed54113c17fab0956d2bf78e; `incremental_suffix.go` 07fa0bbd527fb1ac9bf61fd7791d2819f6b9e70816e381b8bd5fd2da6da14366. Baseline is b4aadbc9. Frozen same-binary comparison clones the original convergence/replay/copier implementation too, instead of sharing modified helpers. Raw log, recipe, hashes and samples are in `/private/tmp/elisa-lexer-deferred-pair-20261019-r4/`. Go 1.27.2 darwin/arm64, GOMAXPROCS=4, GOMEMLIMIT=2GiB; 100ms samples, baseline/candidate/candidate/baseline leaf order, three repetitions per leaf (six observations per implementation). No competing compiler process; both shared benchmark locks held.

| Case | Baseline median | Candidate median | Allocation baseline → candidate |
| --- | ---: | ---: | ---: |
| 512 KiB middle insertion | 5.969 ms | 5.112 ms (−14.4%) | 42.33 → 24.09 MB |
| 512 KiB early insertion | 5.654 ms | 5.472 ms (−3.2%) | 37.68 → 24.09 MB |
| 512 KiB unchanged component | 5.186 ms | 4.686 ms (−9.6%) | 24.02 → 24.02 MB |
| Mid-file unterminated comment | 3.862 ms | 3.819 ms (−1.1%) | 19.10 → 12.34 MB |
| Opening unterminated comment | 2.386 ms | 2.233 ms (−6.4%) | 14.16 → 0.59 MB |

This is a targeted component improvement, not a universal latency win: cold lexing, complete rewrite and dirty-tail rewrite medians were within 1%; small replacement +2.2%, tail-quarter deletion +1.9%, and a 24µs large-shrink case +4.4%, with unchanged allocation on those direct paths and overlapping timing samples. Large middle-insertion samples were separated across implementations. Retained insertion output remains about 26.74 MB: this removes transient allocations rather than compressing token storage. Tail-deletion capacity retention remains a separate policy choice. The prior revisions r1–r3 were rejected or revised and are not acceptance evidence.

Final validation: all lexer tests ran as part of the final paired benchmark, including randomized edit/diagnostic parity, independent storage, attribution histories, and adversarial partial suffix-copy rollback. Race validation is recorded separately in `/private/tmp/elisa-lexer-deferred-final-race-20261019.log`.
