# Fixes VERIFIED here that still need applying under nw-core/

nw-core/ was unreadable for the whole second half of this session (EPERM through
Bash, the Read tool, and with the sandbox disabled; only worktrees/ stayed
reachable). These two changes are verified against the real display-list object
file and just need transcribing.

## 1. nw-core/scripts/build_wasm.sh — export __heap_base

`wasm-ld --export-dynamic` does NOT export `__heap_base`; it is linker-synthesized
and needs naming explicitly. Add `--export=__heap_base`:

    wasm-ld --no-entry --export-dynamic --allow-undefined --export=__heap_base \
        -o "$ROOT/build/nwcore.wasm" "$ROOT/build/nwcore_wasm.o"

## 2. nw-core/shells/web/elisa-wasm-runtime.js — stop guessing the heap base

In `attach()`, replace:

    const base = instance.exports.__heap_base;
    heapNext = typeof base === "number" ? base : 65536;

with:

    // __heap_base is exported as a WebAssembly.Global, not a number. Guessing
    // when it is missing silently allocates on top of the module's static data:
    // string literals get overwritten mid-run and the failure looks like corrupt
    // program logic rather than a bad heap base.
    const raw = instance.exports.__heap_base;
    const base = raw instanceof WebAssembly.Global ? raw.value
               : typeof raw === "number" ? raw : null;
    if (base === null) {
        throw new Error(
            "elisa: module does not export __heap_base -- link with " +
            "`wasm-ld --export=__heap_base`. Refusing to guess a heap base: " +
            "a wrong one silently overwrites static data.");
    }
    heapNext = base;
    heapBase = base;

BOTH are required. The link flag alone does not fix it -- `typeof base === "number"`
is false for a Global, so the fallback still fires. Verified by testing that
intermediate state explicitly.

## 3. Already applied earlier in the session (before access was lost)

`elisa-wasm-runtime.js` also received, and these ARE on disk under nw-core:
  * `grow()` now throws when `memory.grow` returns -1 instead of advancing
    `heapEnd` and handing back out-of-bounds addresses;
  * `release()` tracks a stack of live blocks so NESTED regions reclaim; the old
    single-`lastBlock` test could never match once alignment padding intervened,
    so a frame leaked its whole outer region every tick.
  * a `heapBase` variable was added and is set in `attach()`; item 2 above sets it
    from the Global rather than from the guess.

## 4. scene_nback.elisa can drop its workaround

`def field_left()` exists only because stage1 mis-compiled `const FIELD_LEFT: f64 =
(REFERENCE_WIDTH - FIELD_SIZE) / 2.0`. That is FIXED (see the f64 const folding work
in this worktree; nine expression shapes now agree between stage0 and stage1), so
the const can go back:

    const FIELD_LEFT: f64 = (REFERENCE_WIDTH - FIELD_SIZE) / 2.0

and the comment block above field_left() can be deleted.

## Re-verify after applying

    scripts/build_wasm.sh        # 421 cases, and text should now be correct
    scripts/check.sh             # 13 differential suites
    node shells/web/repeat2.mjs  # pool must read "Dual N-BackN =Trialframe-end"
                                 # on EVERY frame, not just the first

## 5. `config` can come off STAGE0_ONLY

`scripts/check.sh` has `STAGE0_ONLY=(config)`, there because a `const cstr`
global declined the whole unit on stage1. That is FIXED (see
SESSION_COMPILER_FIXES.md #6 -- stage1 now prints "hello from a const global",
matching stage0). Remove `config` from STAGE0_ONLY and confirm the suite's 342
cases pass on both stages.

## 6. SDL3 desktop shell is written and waiting

See `sdl3_shell/` next to this file: `main.c`, `display_list.h`, `build_sdl3.sh`,
and a README with what is verified (compiles clean against SDL3 3.4.14; the C
decoder round-trips a real Elisa-encoded frame) and what is not (never linked or
run, because the core object comes from `src/wasm/wasm_exports.elisa`).

## Order to apply these

  1. shim + build flag (items 1 and 2) -- this is the browser text fix.
  2. `scripts/build_wasm.sh`, then `node shells/web/repeat2.mjs`: the pool must
     read "Dual N-BackN =Trialframe-end" on EVERY frame, not just the first.
  3. drop `field_left()` back to a const (item 4), rebuild, re-run check.sh.
  4. `config` off STAGE0_ONLY (item 5).
  5. move `sdl3_shell/` into place and build it (item 6).
  6. delete the two stale debug scripts at `shells/web/frames3.mjs` and
     `shells/web/repeat2.mjs` once the wasm differential covers repeated frames,
     or promote repeat2 into `test/` -- it is the only thing that was checking
     frame-over-frame pool stability, which is exactly the bug that got missed.

## 7. board.elisa threads `Arena&` through user-facing signatures — remove it

`nw-core/src/board.elisa` takes an explicit `Arena&` parameter at EIGHT sites
(lines 32, 54, 70, 77, 85, 99, 132, 162). stage0 warns on every one, on every
build:

    warning: internal runtime carrier type "Arena" is not supported in
    user-facing code; use "region scopes and inferred container regions" instead

`Arena` is the runtime's internal carrier, not the language surface. The surface
is region scopes (`in a:`), with mark/restore/destroy as the deliberate escape
hatch. Threading the arena struct through signatures makes every caller
region-polymorphic in a parameter, which is the thing the region system exists to
remove.

The port already has the RIGHT pattern, in display.elisa, with the reasoning
spelled out at display_list_new:

    # The list's buffers are allocated in the innermost active region, so a
    # caller wraps construction in `in arena:` to choose one. Taking an Arena&
    # here instead would make every draw_* call region-polymorphic in the LIST
    # parameter, and the region cannot be inferred at those call sites.

So this is not an open design question -- board.elisa simply predates that
decision and was never brought in line. Drop the parameters, let the callers own
the region with `in a:`, and the warnings go with them.

Worth doing for its own sake AND because eight recurring warnings per build are
what trained me to skim the build output; the Arena warnings sat in every wasm
build this session and I read past them.
