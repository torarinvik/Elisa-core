# Owned lexer suffix convergence

A source edit can now resume lexing when it reaches a compatible checkpoint
inside an identical byte suffix. This works with shifted offsets and line counts,
and with inserted/deleted tokens before convergence. It needs no admitted AST
snapshot, so it can benefit the first edit after initial tokenization. The parser
continues to receive tokens with the same fields as fresh lexing.

Convergence requires equal filenames, columns, indentation stacks, last token
kind and line-start state, with no pending tokens, open grouping/pattern/layout
contexts or lexer errors. The current physical line must also belong to the
unchanged byte suffix: later grouped-layout logic can inspect its indentation.
Only sufficiently large remaining suffixes are reused.

Tokens rebind source-backed text to the current owned buffer and shift physical
offsets. A compact token-indexed attribution epoch log distinguishes relative
logical lines from explicit `#line` resets, including resets to the same filename.
Owned logical filenames and rebased checkpoint stacks prevent old source owners
from being retained. Later checkpoints are preserved so a subsequent late edit
can still reuse its prefix. Checkpoint/input budgets include the new metadata.
The existing `ReusedTokens` prefix metric keeps its meaning; suffix reuse has a
separate accessor.

## Qualification

The frozen source consists of committed `a496a010` plus the six owned lexer/native
test files listed with SHA-256 hashes in the evidence manifest. Other ongoing
parser/AST/semantic edits were excluded. Selected lexer, parser and compile-server
integration checks pass under race detection (7.093 s, 2.781 s, 10.057 s).

Coverage includes width and line shifts, Unicode/string views, token insertion,
subsequent late edits, old-buffer destruction, copied pattern-header offsets,
grouping/layout, errors, repeated `#line` resets and 120 deterministic valid edit
histories. Six fresh/retained objects link and execute correctly after a width
change, an expression/token-count plus line change, and undo; each retained
revision explicitly verifies lexer convergence was exercised.

A negative control removes the physical-line guard. Equal columns/stored layout
then accept a boolean-continuation edit whose later grouped header observes
different leading indentation, producing different tokens without lexer errors.
The guarded implementation passes that regression. This validates a real backward
read dependency, not just the stored-state comparison.

## Measurements

The source has repeated expressions across three source-attribution sections.
Every edit widens an early integer literal and adds blank lines. Three single-CPU
300ms samples compare cold `TokenizeSource` (including owned input and checkpoint
capture) with the same tokenizer using its previous revision:

| Median per edit | Cold owned tokenization | Convergence |
| --- | ---: | ---: |
| Time | 484.0 us | 159.1 us |
| Allocated bytes | 1,136,558 | about 752,002 |
| Allocations | 35 | 52 |

That is about 67% lower time and 34% fewer bytes in this narrow workload, at the
cost of more small allocations. Samples range 472–571 us cold and 156–165 us
converged. Source construction is included in both benchmark paths. Cold owned
tokenization is not the ordinary CLI's bare `Lexer.Tokenize` path, and these
numbers are not whole-compiler latency or a stable project speedup claim.

Exact frozen inputs, source hashes, source archive, full logs and the failing
negative control are under
`build/verification/incremental-lexer-convergence-20261018/`. General semantic
body reuse, broad position-relocated syntax reuse and real-project qualification
remain unfinished parts of the fast incremental compiler objective.
