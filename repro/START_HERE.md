# Resume point after the Claude Code restart

Everything below is ON DISK and UNCOMMITTED — git was unavailable for the second
half of the session (`Operation not permitted` reading
`Go projects/Elisa-core/.git/config`), so nothing could be staged.

## First thing: verify access came back

    ls nw-core/scripts/            # was DENIED all session
    git -C worktrees/elisa-core-nw status --short

## Then: commit the work

15 compiler/runtime/test defects fixed, all verified. Full record with
measurements, negative controls, and the three wrong turns worth not repeating:

    repro/SESSION_COMPILER_FIXES.md

Changed files:

  worktrees/elisa-core-nw/
    compiler/src/semantic/target_consts.go              (+ target_triple_test.go, NEW)
    compiler/src/backend/llvm_exprs_emitspecializedarenafromviewcall_to_emitunaryexpr.go
    compiler/runtime/elisacore_std/arena.elisa
    compiler/runtime/elisacore_std/collections.elisai   (regenerated)
    repro/*                                             (repros + these docs)

  worktrees/elisa-compiler-nw/
    src/semantic/resolve_types_infer.elisa              (the exponential fix)
    src/semantic/check_firm_arg_type_mismatch.elisa
    src/backend/codegen_env.elisa                       (\xHH and \uXXXX escapes)
    src/backend/codegen_struct_reg.elisa
    src/backend/codegen_expr_idents_binary.elisa
    src/backend/codegen_declare.elisa
    src/backend/codegen_type_tables.elisa
    src/backend/llvm_c.elisa
    src/driver/project.elisa                            (-O1 accept + message)
    test/parity/differential_corpus.sh                  (exclude repro/)
    test/parity/project_report_smoke.sh                 (badopt fixture O1 -> O9)
    test/parity/parser_ast_smoke.sh                     (quote $ELISACORE_BIN)
    elisacore_std/{arena.elisa,collections.elisai}      (re-synced with canonical)

NOTE: `worktrees/elisa-compiler-nw` is a worktree of `Elisa Projects/Elisa-compiler`
and another agent works in that repo's main worktree. Commit on the nw-port branch
only. Never `make build` from a worktree — it installs over ~/.elisac/elisac,
which is theirs.

## Then: the port work that was BLOCKED all session

    repro/pending_nw_core_patches/README.md

Seven items, in apply order. The first two are the browser-text fix and are
verified against a real display-list object file; item 7 is the board.elisa
region cleanup (drop the `Arena&` params, let region inference do it).
`sdl3_shell/` next to it is roadmap item 16, written, compiling, linking and
running headless.

Still to write after that: roadmap item 18, the ~40 task modules (maze done,
sokoban next). These need `original-neural-workshop-enhanced/` readable — do not
write them without the reference source.

## Gate state when the session ended

  * stage0 `go test ./src/...`            ALL GREEN
  * stage1 self-host fixpoint             gen3.o == gen4.o byte-identical
  * stage1 semantic_internal_diff         0/3178 mismatches (baseline 0)
  * stage1 project_report_smoke           91 passed / 0 failed
  * stage1 full gate                      187 ok / 1 FAIL before the last two
                                          fixes; the 1 was project_report_smoke.
                                          A clean full re-run was in flight at
                                          restart and never finished — RUN IT:

    cd worktrees/elisa-compiler-nw && GOFLAGS=-buildvcs=false \
      ELISAC=$PWD/../elisa-core-nw/compiler/bin/elisac-nw \
      ELISA_CORE=$PWD/../elisa-core-nw \
      ELISACORE_BIN=$PWD/../elisa-core-nw/compiler/bin/elisac-nw \
      bash test/parity/run_all.sh

  Expect 189/189. That number is the one thing not yet observed.

## Still open (filed, not fixed)

  * repro/nw_stage1_char_i64_impl_collision.elisa — `print(42)` declines. Root
    caused: char and i64 share ValueType{Signed,64} so two protocol impls collide.
    stage0 keeps char a distinct named type and only LOWERS it to i64; fix plan
    and the surgical alternative are both in the file.
  * repro/nw_stage1_accepts_unsatisfied_protocol_bound.elisa — stage1 is more
    permissive than stage0 on an unsatisfied generic bound (missing check, not a
    miscompile).
  * repro/nw_stage1_redeclared_extern_signature.elisa notes a SEPARATE stage0
    defect: it compiles a redeclared extern but the object does not link
    (`___ovl__getenv__cstr__getenv`).
