# Native desktop shell (SDL3) — roadmap item 16

Written while `nw-core/` was unreachable, so it lives here until it can be moved.

## Where these files go

    main.c           -> nw-core/shells/sdl3/main.c
    display_list.h   -> nw-core/shells/sdl3/display_list.h
    build_sdl3.sh    -> nw-core/scripts/build_sdl3.sh   (chmod +x)

Then:

    brew install sdl3 pkg-config      # or the platform equivalent
    scripts/build_sdl3.sh
    build/nw-sdl3

## What it is

The third renderer for one display list. shells/web draws it on a canvas,
shells/python draws it with pyglet, this draws it with SDL3 — and none of them
contain game logic. The core decides what a frame contains; a shell only turns
commands into pixels. A verdict is a COMMAND here too, not a colour the shell
chose.

It reuses `src/wasm/wasm_exports.elisa` unchanged rather than adding a native
export file: every entry point there is already scalar C ABI, which is what a C
host wants. `nw_frame_alloc` returns a real pointer natively (it is a `malloc`
result cast to i64), so the shell casts it straight back.

## Coverage

Full: RECT, BORDERED_RECT, BOX, CIRCLE, ARC, LINE, TRIANGLE, POLYGON/POLY_POINT,
TEXT, VERDICT, MARKER. Circles/triangles/polygons go through SDL_RenderGeometry;
arcs are polylines, since SDL3 has no arc primitive.

Deliberately not yet: SPRITE (needs an atlas) and SOUND (counted, not played) —
both are later phases, and the core emits neither in an n-back frame.

TEXT uses `SDL_RenderDebugText`, whose 8x8 cell is scaled to the command's font
size via `SDL_SetRenderScale`. That keeps the shell dependency-free (no SDL_ttf).
It honours all nine anchors. If the desktop build later wants real typography,
this is the one function to replace.

## Verified

  * COMPILES clean against SDL3 3.4.14 (`cc -O2 -std=c11`).
  * LINKS and RUNS against a real Elisa core object. nw-core was unreachable, so
    a stand-in was built exporting the SAME C ABI (`nw_frame_alloc`,
    `nw_frame_write`) over the same encoder and 40-byte record. Run headless
    (`SDL_VIDEODRIVER=dummy`) for 3 seconds: SDL init, window, renderer, ~180
    frames written, decoded and rendered, clean exit, NO output.
  * That silence is meaningful, because the failure path was proven loud first:
    rebuilt with `FRAME_CAPACITY 8` so every frame is too big to write, the same
    run logs `frame 1: decode failed (0 bytes)` 70 times in 2 seconds. So an
    empty log means every frame really did decode and render.
  * `display_list.h` DECODES A REAL ELISA-ENCODED FRAME (324 bytes, 7 commands,
    28 pool bytes) with floats and pooled strings round-tripping:

        [ 0] RECT   a=0.0  b=0.0  c=100.0
        [ 2] TEXT   a=1.0  b=2.0  c=22.0   text="Dual N-Back" anchor=0
        [ 3] TEXT   ...                    text="N ="
        [ 4] TEXT   ...                    text="Trial"
        [ 6] TEXT   ...                    text="frame-end"

## Still to check

  1. `scripts/build_sdl3.sh` against the REAL core (`src/wasm/wasm_exports.elisa`)
     rather than the stand-in. The ABI is identical, so this should be a
     formality, but it has not been done.
  2. Compare a frame against the web shell at the same trial/position/verdict.
     Geometry should match modulo the text font.
  3. `test/display_decode.py` stays the reference decoder; `display_list.h` is a
     transliteration of it and the two must stay line-comparable.

Nothing has been seen on a real display -- it was only ever run headless, so
"renders" here means "every command was executed without error", not "looks
right".
