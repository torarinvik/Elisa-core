# Inline grant expression region transparency

Based on core `2d7170a3673af3d8c66566d8286d2c1ff6a449df`.
An inline `can` supplies permissions without changing its operand's value,
contextual destination type, call shape, storage owner, or borrowed provenance.
The contextual analyzer now analyzes the operand with the expected type inside
exactly the existing permission scope. Region-polymorphic call queries, struct
local owner recording, reference provenance, and inferred return provenance see
through the same wrapper. Existing liveness, region-polymorphic builder admission,
and mutable reference checks remain in force.

Focused controls cover a context-typed empty darray, a packed Expr/File parse
helper result passed to a mutable File reference under a grant block versus
postfix/nested postfix grants, and rejection of local reference and scope-owned
container escapes. The exact base rejects the postfix positive controls; the
candidate accepts all positive controls and rejects both borrow negatives.

Qualification ran only on Vast in private archived trees:
- `/root/work/codex-inline-can-region-before`: exact base, private baseline CLI.
- `/root/work/codex-inline-can-region`: candidate, private CLI.
- `/root/work/codex-inline-can-source48`: frozen compiler source
  `48aa99382386fd8a787f84685b5733647067baaa`.

Commands:
```
GOMAXPROCS=2 /usr/local/go/bin/go test -p 1 ./src/semantic -run TestInlineCan -count=1
GOMAXPROCS=2 /usr/local/go/bin/go test -p 1 ./src/semantic -count=1
PATH=/usr/local/go/bin:$PATH GOMAXPROCS=2 make build
ELISA_HOST_LINUX=1 ELISA_HOST_X86_64=1 GOMAXPROCS=2 BINARY -emit facts src/driver/elisac.elisa
```

The final candidate semantic suite passed in 61.495 seconds; LLVM was 21.1.8.
The final private comparison reproduces 29 hard diagnostics and exit 1 on the
base, versus zero hard diagnostics and exit 0 on the candidate. Existing
non-fatal local-effect warnings are retained. Final source comparison logs are `/tmp/inline-can-frozen48-{before,after}.{facts,stderr,status}`.
Earlier shared-binary comparison logs are `/tmp/inline-can-source48-{before,after}.*`;
those demonstrate exit 1 versus 0, but are superseded by the private comparison.
A mistaken `-check` invocation and a mistaken `src/main.elisa` invocation failed
before analysis and are not qualification evidence.

Product/runtime manifest: `/tmp/inline-can-products.json`.
Both private core runtime snapshots contain the same 52 files, aggregate digest
`65b8264de59636c24e20ad435027010cddda64b9b54a1d6e2d0da90ea28d3794`
(SHA256 of JSON-encoded sorted relative path/file-SHA256 pairs).
Baseline binary SHA256:
`a17097fb706fc729de8e3cd8f12744e942b2fdcb912411b198e16740fb28543c`.
Candidate binary SHA256:
`9021a464434fae8f511a93b2d11f4915c09314d4bac8fa01744087147a6be1b6`.

This qualification concerns semantic admission and region transparency. It does
not claim a native compiler bootstrap, installation, or effect-policy hardening;
those remain separate integration gates.
