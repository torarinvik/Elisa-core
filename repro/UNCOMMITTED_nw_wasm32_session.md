# Uncommitted work from the wasm32 session (git was unavailable when it ended)

The session lost filesystem access to everything under the repo except this
worktree, and git could not read `Go projects/Elisa-core/.git/config`, so none of
this could be committed. All edits are ON DISK and verified; they only need
staging.

## Files changed in worktrees/elisa-core-nw

    compiler/src/semantic/target_consts.go        an explicit triple no longer
                                                  inherits the host's os/arch;
                                                  wasm32/wasm64/wasi recognized;
                                                  new ELISA_TARGET_ARCH_WASM* and
                                                  ELISA_TARGET_OS_WASI predicates
    compiler/src/semantic/target_triple_test.go   NEW - regression tests, incl. the
                                                  host-fallback case that must keep
                                                  working
    compiler/runtime/elisacore_std/arena.elisa    reserve_commit's default
                                                  reservation is sized to the
                                                  target's paging model (wasm has
                                                  none)
    compiler/runtime/elisacore_std/collections.elisai
                                                  regenerated: the arena const is
                                                  now target-dependent, and the
                                                  interface must say so
    repro/nw_wasm32_target_triple_falls_back_to_host.md   NEW - full diagnosis
    repro/nw_wasm32_mixed_elem_darray.elisa               NEW - the still-open bug
    repro/nw_wasm32_interleaved_darray_growth.elisa       withdrawn; the bug it
                                                          described does not exist

## Files changed OUTSIDE this worktree (also uncommitted, also on disk)

    nw-core/shells/web/elisa-wasm-runtime.js      two host-shim fixes:
                                                  memory.grow failure is no longer
                                                  ignored, and freed blocks are
                                                  tracked on a stack so nested
                                                  regions actually reclaim

## Verification actually run

    go test ./src/...   ALL GREEN (src 311s, semantic 51s, backend/easm/lexer/
                        parser/smt/interpreter/frontendir ok)
    negative control    the new tests fail against the old behaviour exactly as
                        claimed: wasm32-unknown-wasi reported os=macos arch=arm64,
                        and riscv64-unknown-linux-gnu reported arch=arm64
    wasm probe          before: one darray cost 128 MiB and the heap grew ~134 MB
                        per call until memory.grow failed
                        after:  every call maps at the same two addresses, no growth

## NOT verified (needs the rest of the repo back)

    scripts/check.sh          the 13 differential suites
    scripts/build_wasm.sh     421-case wasm differential -- it DID pass mid-session,
                              before the arena/shim changes were complete
    make -C compiler test     not re-run after the final edits
