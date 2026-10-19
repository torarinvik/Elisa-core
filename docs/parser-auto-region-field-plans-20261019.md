# Auto-region reflection field plans

CPU profiles from the cache-to-parser work identified repeated AST reflection in allocation discovery, ExprBlock discovery, and constructed-local forwarding. Those walks previously visited every struct field, including scalar source positions and names.

The four struct-walk loops now use an immutable field-index plan keyed by Go reflect.Type. Pointers and interfaces remain candidates; structs, arrays and slices remain candidates when they can contain them. Scalar-only subtrees are skipped. Unexported fields and maps retain the old traversal behavior. The shared sync.Map contains only type metadata, never an AST, source revision, or query result. Existing per-scan ExprBlock/context memoization and forwarding/shadowing rules are unchanged. Metadata is built once per encountered Go struct type, not once per source revision.

A differential test compares every reachable pointer with an unfiltered traversal through nested structs, arrays, slices, interfaces and nil values, including excluded maps/unexported fields. Another test exercises concurrent plan publication. Existing parser tests cover inferred-region semantics, nested expression blocks, AST mutation between scans, explicit owners and shadowed constructors.

## Evidence

Frozen source/reference hashes, raw samples and command records are in `/private/tmp/elisa-parser-field-plans-20261019/`: `qualified-manifest.json`, `paired.log`, `medians.json`, `race.log`, and `runs.json`. Baseline is bdb39acc. The same test process includes the complete baseline parser under an alternate package name; both implementations share identical lexer/AST dependencies. Each parser fixture preflights exact AST and notice parity and rejects parse errors. Order is baseline/candidate/candidate/baseline, three repetitions per leaf (six observations each), 150ms samples. Go 1.27.2 darwin/arm64, GOMAXPROCS=4, GOMEMLIMIT=2GiB. Shared locks and a process scan excluded competing compiler work; unrelated desktop activity remains uncontrolled.

| Repeated parse | Baseline median | Candidate median | Change |
| --- | ---: | ---: | ---: |
| 256 scalar functions | 0.743 ms | 0.602 ms | −19.0% |
| 256 container functions | 1.422 ms | 1.032 ms | −27.4% |
| 256 expression-block functions | 1.031 ms | 0.764 ms | −25.9% |
| 256 struct-forwarding functions | 0.912 ms | 0.890 ms | −2.4% |
| 2,048 scalar functions | 5.382 ms | 4.092 ms | −24.0% |
| 2,048 container functions | 11.086 ms | 8.931 ms | −19.4% |
| 2,048 expression-block functions | 7.934 ms | 6.609 ms | −16.7% |
| 2,048 struct-forwarding functions | 7.002 ms | 6.562 ms | −6.3% |

Deep nonallocating scan controls at depths 4/16/64 improved 42.1%/34.5%/36.9%. Per-operation allocation is unchanged (small fixture-dependent variation in variable region names), with a new process-lifetime metadata cache. Type-plan construction is outside these steady-state samples. These are parser-component results, not end-to-end Stage1 or semantic-check timing.

The paired run also executed the complete parser test package (exit 0). The final race run passed (package 7.289s). The permanent fixture matrix is `BenchmarkAutoRegionParseBodies`; run it from `compiler/` with `GOMAXPROCS=4 GOMEMLIMIT=2GiB go test -p 1 ./src/parser -run '^$' -bench '^BenchmarkAutoRegionParseBodies$' -benchmem`. The permanent benchmark was added after the paired measurement and receives a separate smoke check; runtime implementation hashes remain unchanged.
