# Persistent rebuild cache

The standalone CLI checks the content-addressed object cache before retained-server
lookup. An identical `-emit obj` invocation can restore its output and diagnostic
transcript from disk without contacting or starting a daemon. Server socket and
idle/automatic-start controls do not affect object identity. Compiler options,
source and includes, diagnostics policy, target and toolchain identity still do.

Native object partitions now also persist across compiler processes. Both the
standalone object emitter and retained server use the same partition layout and
compiler-artifact namespace. Lookup compares complete current partition bitcode
and target settings, validates the object checksum, and treats unavailable,
incomplete or corrupt entries as misses. Publication uses a temporary file and
atomic rename. Each compiler namespace has a 2 GiB disk budget with oldest-use
entry eviction. Old compiler namespaces are isolated and can be deleted manually.

The default location is the existing platform build cache directory, under
`elisacore/build_objects/partitions/<compiler-identity>/`. `ELISAC_BUILD_CACHE_DIR`
overrides the build cache root. `ELISACORE_BUILD_CACHE=0` disables both object
cache layers; `ELISACORE_PARTITION_CACHE=0` disables only partition persistence.
`ELISACORE_RETAINED_SERVER=` (set to empty) forces standalone compilation.

## Validation and current limits

Tests cover separate object sessions, exact input/target/compiler invalidation,
corrupt headers/input/object bytes, trailing data, concurrent publication, eviction,
and executing linked output after an edit and revert. CLI cache tests check that
a disk hit succeeds with a nonexistent server socket and that changed codegen
flags cannot reuse an old object.

A subprocess smoke using an 80-function program and separate compiler processes
measured 0.576 s initially, 0.037 s for an identical disk hit, 0.043 s for a body
edit, and 0.037 s for a revert. Every invocation had a 30 s timeout. These are
small-fixture results, not timings for the self-host compiler.

This does **not** establish the requested sub-10-second stage1 rebuild after an
arbitrary edit without a server. Partition lookup currently follows complete
frontend analysis and LLVM lowering. The whole-object cache bypasses that work
only for a source revision built previously. Executable linking is not covered by
this whole-object cache.

The missing frontend layer needs stable, pointer-free function products and a
dependency graph on disk. `semantic/incremental_function_product_schema.go`
already inventories the Result fields that must be rebound, restored or
recomputed, but is an inventory rather than a complete replay implementation.
A correct disk restore must hydrate analyzer-backed backend queries as well as
expression types, proof facts, effects, borrows, diagnostics and lowered nodes;
serializing Result's exported fields alone would silently lose required state.

Next qualification must compare a fresh-process edited build and a cache-disabled
cold build (diagnostics, LLVM IR and linked execution), including contract,
permission, overload, struct-layout and transitive dependency edits. Measure the
actual stage1 source with the retained server explicitly disabled throughout.

Development compiler builds now disable embedded Go VCS metadata by default.
Previously, a test/documentation-only commit changed the executable through its
Git revision/time/dirty stamp and invalidated all compiler-content-keyed caches.
`make build` / `make install` use `-buildvcs=false`, with
`ELISAC_BUILD_VCS=true` available for builds that explicitly want that metadata;
the auto-install hook also uses `-buildvcs=false`. The complete executable hash
remains cache authority. No compiler-code or toolchain identity check is relaxed.
Two dirty-checkout builds of the hook's recipe produced the same SHA-256 at
different output paths; commit/reinstall identity is checked separately.


The per-stream diagnostic replay bound is now 16 MiB. The real compiler's
9,381,984-byte warning transcript exceeded the former 1 MiB bound, so otherwise
successful self-builds never populated the whole-object cache. The reader remains
bounded (48 MiB encoded JSON), and both streams retain independent size limits
and exact-byte checksums. A regression replays two nearly-16-MiB binary streams
through one build and a cache hit; over-limit output still reaches the user
unchanged and never publishes a cache entry. All build-object/transcript integrity,
corruption, request-observer and overflow tests pass.

## Stable Stage0 build receipts

Default `make build`, `make install`, and the primary-checkout install hook use
the bundled `compiler/scripts/stage0_provenance.py` producer. It builds with
`-buildvcs=false`, stages a fresh output, and publishes a hash-bound binary and
receipt after the Go-selected source inputs remain unchanged. Test/documentation
commits do not become binary identity through Git timestamps. Explicit custom
solver/linker/VCS builds keep their separate direct Go path and do not claim the
default producer's receipt. Python3 is required by the default receipt path.

Protocol v4 fingerprints the actual `go list -deps -json ./src` production and
embed inputs, module metadata, binary contents, full embedded Go build/dependency
metadata and supported build context. Unsafe output/overlay/dry-run flag overrides,
workspaces, local replacements and unsupported cross-target builds are rejected.
Failed or changing-source builds preserve the previous binary/receipt pair.
The self-host checkout carries the matching versioned consumer; freshness checks
outside Make replay the recorded supported build context rather than requiring
Make-only cgo flags to remain in the caller environment. This is trusted local
build provenance, not a signed attestation for untrusted supplied receipts.

The standalone Go copy passes20 helper controls; its consumer-shell-only case
is skipped here and passes in the self-host checkout's21-case suite. The default
Make build with explicit llvm-config succeeded under an external30second cap;
the self-host freshness checker then accepted that product outside Make. The
resulting compiler emitted a return42 smoke module that independently passed
LLVM assembly. Default/custom build/install dry runs, shell syntax and diff
checks also pass. Raw records `/tmp/elisa-core-stage0-receipt-tests-v2.log`,
`/tmp/elisa-core-stage0-receipt-make-build.log`,
`/tmp/elisa-stage0-receipt-outside-make-check.log`, and
`/tmp/elisa-stage0-receipt-smoke.*`.

The producer helper is vendored byte-for-byte from the self-host helper at
SHA256 f494b6c4a1cfb0e4afb41faf1827b2f3cb67f24a18f941457c69ca25fa2e2181.
Protocol changes must update both copies/version; the Go test copy adapts only
the consumer-shell integration case absent from a standalone Go checkout.
