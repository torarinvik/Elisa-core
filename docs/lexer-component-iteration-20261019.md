# Lexer component iteration

Work on lexer throughput and edit reuse independently before whole compiler validation. The component gate is `GOMAXPROCS=4 GOMEMLIMIT=2GiB go test -p 1 ./src/lexer` from `compiler/`. Add `-bench 'BenchmarkSource' -benchmem` for the matrix. Use longer repeated samples for performance decisions; short samples only validate the harness.

The benchmarks construct revisions and check cold/incremental token and diagnostic parity before timing. Timed work includes source ownership, scanning, checkpoint construction, token copying and rebasing. Metrics distinguish prefix/suffix reuse, scanned tokens, transient allocation, token capacity, and conservative retained payload (not RSS). Distinct deletions and incomplete comments cover editor fallback paths. The old early-shift benchmark also excludes edit formatting from timing now.

Direct unchanged tokenization is component-only: the production parsed-file cache can bypass tokenization and parsing, but still copies the AST. These figures are not whole compiler request latency.

A speculative capacity candidate reduced early/middle edit allocation by 36–43% on a dense 512 KiB corpus, with mostly flat or modestly lower medians. It was rejected before promotion: a middle-of-file unterminated comment took a median 4.804 ms versus 3.803 ms, allocating 35.27 MB versus 19.10 MB. Its eager full-size reserve triggered a second compaction copy. Runtime code remains unchanged by this benchmark commit. The rejected patch is saved locally at `/private/tmp/elisa-lexer-capacity-rejected-20261019.patch`; raw fallback evidence is `/private/tmp/elisa-lexer-mid-comment-20261019.log`.

Next candidate should defer prefix/output materialization until convergence or EOF determines the required size, preserving independent source/token ownership. Accept only after package parity tests and repeated normal-edit/deletion/incomplete-input measurements. No full semantic gate is needed for this isolated benchmark change.
