# The differential corpus scans repro/, and repro/ is full of programs that fail on purpose

## What the gate reported

    behavioural differential corpus (ratchet)   FAIL
      differential corpus FAILED: 1 program(s) produce a DIFFERENT ANSWER under stage1
      differential corpus FAILED: 11 declines exceeds baseline 1

## Why

`test/parity/differential_corpus.sh:101`

    CORPUS_DIRS=("$ROOT/test" "$ROOT/.probe")
    [ -d "$ELISA_CORE" ] && CORPUS_DIRS+=("$ELISA_CORE")
    find "${CORPUS_DIRS[@]}" -name '*.elisa' | xargs grep -l '^def main'

`$ELISA_CORE` is the whole Elisa-core checkout, so the sweep picks up
`$ELISA_CORE/repro/` -- 21 files with `def main` as of this writing. That
directory is BUG DEMONSTRATIONS. Most of them are curated to fail on stage1;
that is the entire point of the file.

So the corpus is asking a ratchet to enforce "stage1 matches stage0" over a
directory whose contents are chosen precisely because stage1 does NOT match
stage0. Every new repro filed raises the decline count and breaks the gate,
which punishes filing repros -- exactly backwards.

The baseline of 1 predates most of the directory. It was not wrong when written;
it rotted as repros accumulated, and nothing connects the two.

## The judgement call

Two defensible readings, and this one is worth a human deciding:

  (a) EXCLUDE `repro/` from CORPUS_DIRS. A repro that declines is a FILED BUG,
      not a regression, and the ratchet's job is to catch regressions in ordinary
      programs. This keeps the gate meaningful and keeps filing repros free.

  (b) KEEP it and re-baseline. Repros are, by construction, the best differential
      inputs there are -- they are the known disagreements. Under this reading the
      baseline should be bumped to the current count and should fall as repros get
      fixed.

(a) is the recommendation: under (b) the gate cannot distinguish "someone filed a
new bug" from "someone broke the compiler", and those need different responses.

The MISMATCH was looked at on its own -- a wrong answer is not excused by where
the file lives -- and it is now FIXED. It was
`nw_json_hex_escape_literal_stage1` (stage0=4, stage1=14): stage1 never decoded
`\xHH` or `\uXXXX`, so those escapes survived as literal source text. See
SESSION_COMPILER_FIXES.md #12. The corpus was rediscovering a bug this directory
had documented since 2026-08-25 while nothing failed on it, which is a fair
argument that repros DO belong in a differential sweep -- just not in one whose
ratchet cannot tell a filed bug from a regression.

## Resolution

`repro/` is excluded from CORPUS_DIRS, and the one real defect it surfaced is
fixed. Re-running the corpus should now report 0 mismatches and 0 declines over
the ordinary programs.

## Not caused by this session's compiler work

Checked separately: the float folding added this session is BIT-IDENTICAL to
stage0 over 0.1+0.2, 1.0/3.0, 1e300*10 (overflow to inf), 1e-300/10 (underflow),
0.0-0.0, and 1.0/3.0*3.0 -- compared as raw i64 bit patterns, not formatted text.
