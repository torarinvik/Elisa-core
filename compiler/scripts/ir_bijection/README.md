# Strict generated-identifier IR bijection candidate

Offline qualification utility. The 46 fixture tests pass; the consistent-renaming fixture pair also passes LLVM assembly validation. This is not a substitute for validating real compiler outputs.

Run with Python 3.10+:

    python3 ir_bijection.py left.ll right.ll --manifest renames.json
    python3 -m unittest -v test_ir_bijection.py

CLI exits: 0 = witnessed permitted equivalence; 1 = differing text/binding; 2 = unsupported input or I/O error. It emits no normalized IR. `verify(bytes, bytes, list[Rename])` returns the witnessed mapping, or raises `NotEquivalent`/`UnsupportedIR`.

## Contract

Inputs must be independently valid LLVM textual IR. This is an alpha-renaming/textual equivalence checker, not an LLVM parser, verifier, optimizer, or executable-equivalence proof. The caller must establish that permitted names really are compiler-generated; source programs can imitate any naming convention. Generated externally visible names can be ABI-relevant, so this must never authorize changing a user-visible ABI name merely because its spelling matches a convention.

The default CLI/API policy permits NO changed identifiers. Every changed decoded name must have an exact `(namespace, scope, left, right)` manifest entry with nonempty producer evidence. Extra/unused entries are rejected. The checker never generates an allowlist from the differences. Evidence is caller-supplied review material, not an automatically verified claim: audit the producer, actual name origin, source revision and ABI boundary before creating it. The synthetic test helper authorizes synthetic fixture pairs only and must not be copied into a real oracle.

In addition to explicit authorization, changed globals MUST have an actual `private` or `internal` definition in BOTH modules. External definitions/declarations, linkonce/weak/available_externally symbols, exports, and unresolved globals cannot be renamed even with a manifest. This restriction deliberately sacrifices some true positives to avoid authorizing a linker ABI change by spelling.

Only complete names matching `__[a-z_]+_[0-9]+`, `__L[0-9]+_C[0-9]+`, or the separately authorized local `__auto_<offset>#<positive-stack-ordinal>` form may differ. The stack ordinal must remain exact, and this form requires the `region-extra-stack` producer in a version-2 manifest. Both sides must have the same stable stem/family. Embedded matches such as `user__L12_C3` or a mangled symbol containing `__temp_3` are NOT rewritten or accepted. LLVM numeric slots, metadata IDs, COMDAT names, attribute groups, and all ordinary names remain exact. Extending this policy needs a producer-backed grammar, not a broad regex substitution.

The lexer distinguishes `@`/`%` symbols, quoted identifiers, ordinary strings, comments, whitespace, and punctuation. LLVM two-hex-digit identifier escapes are decoded for identity checks. Quoted identifiers remain quoted when renamed. All non-identifier bytes, including whitespace, string constants, inline assembly strings, source filenames, comments, debug metadata, and instruction text, must match exactly. Comment predecessors that change due to renamed blocks therefore intentionally cause a conservative rejection.

Namespace keys are `(global,-1,name)`, `(type,-1,name)`, and `(local,function-ordinal,name)`. Basic block labels, arguments and SSA values share their function's local namespace. All encountered identifiers, INCLUDING UNCHANGED names, participate in forward and reverse maps, so a renamed name cannot collide with an unchanged one. Strict token order preserves definition/use positions; consistent bijections preserve the binding graph for valid inputs in supported syntax. Renaming cannot merge two source bindings, split one source binding, change a callee, or redirect a local reference. Global symbols stay module-wide even when referenced in different functions. Named type definitions are collected before references are classified, supporting forward references.

Scope recognition is intentionally restricted to LLVM printer style: each `define` header is on one line ending in `{`, each closing `}` is alone apart from whitespace/comments, and `declare` signatures occupy one line. Named types require module-level `%name = type ...` definitions. Cross-function `blockaddress` and `uselistorder_bb` are rejected. Local/type name overlap and ambiguous named-type/parameter header syntax are rejected rather than guessed. NUL-containing object/bitcode input is rejected. No renaming across functions or reordered instructions/definitions is accepted. Unsupported syntax fails closed even if both inputs are byte-identical.

The CLI uses two seekable streaming passes, retaining module symbol indexes and one complete function at a time. All local bindings, including unchanged names, remain in forward/reverse maps until that function ends; global/type maps persist across functions. A line or function over 64 MiB is rejected. Token storage adds overhead to that bound. The legacy `verify(bytes, bytes)` API still materializes complete streams. It is an offline correctness oracle, not a compiler hot-path dependency.

## Source inspection evidence

Claude artifact inspected:
`/private/tmp/claude-501/-Users-torarinvikbjarko-Documents-Coding-Projects-Elisa-Projects-Elisa-compiler/59b79e01-de4f-45dc-8e17-cd2a9a855de5/scratchpad/h/norm.py`

It substitutes `__([a-z_]+)_[0-9]+` and `__L[0-9]+_C[0-9]+` globally without lexical scope or bijection. Thus `__temp_1` and `__temp_2` collapse, and literal/string/comment bytes are also changed. Its `disk_oracle.sh` removes IR outputs after comparison. No surviving textual IR pair from that script was verified here. A bounded first-8192-byte inspection of surviving `dk-q2.out` found Mach-O magic, so `.out` is not sufficient evidence that an artifact is LLVM IR.

Repository `compiler/src/backend/llvm_retained_disjoint_cache_test.go` has `normalizeRetainedDisjointIR`, also a regex suffix normalizer; it is NOT a binding-aware precedent. This candidate neither modifies that helper nor claims its existing results are invalid without checking actual differences.

## Fixtures

The unittest module supplies 18 focused cases: identity; consistent SSA/block renaming; merge collision; changed reference binding; unchanged-name collision; exact comment/string bytes; exact instruction/whitespace/ordinary-name bytes; global/type namespace separation; per-function locals; module-wide globals; quoted and escaped symbols; whole-name location grammar; changed call target; named type references; and fail-closed unsupported syntax. Cyclic control-flow and forward references are represented lexically without needing a graph solver. Run the tests from this directory with the command above. Unsupported syntax intentionally rejects equivalence. V1 was tested by root and had two fixture errors because BASE used a Python interpreted NUL escape; V2 fixes BASE to a raw bytes literal and changes no string bytes in the LLVM fixture.

## Manifest format and reviewed producer boundaries

`renames.example.json` shows the schema, not a policy for real builds. `scope` is the zero-based textual function/declaration ordinal (both definitions and declarations consume an ordinal), or -1 for globals/types. Names are decoded LLVM symbol contents without @/% or quotes; JSON strings encode as UTF-8. Non-UTF8 decoded names cannot be authorized through this JSON interface and fail closed. API byte names can represent them explicitly.

Observed producer grammar from source review:

- `backend/llvm_generatellvmir_to_flattenllvmtreememberdecls.go:382`: `nextSyntheticName(prefix)` appends a decimal global counter to an arbitrary prefix. Examples are `store.rows.tmp.`, `let.destructure.`, and `scope.guard.`. These do NOT match this tool's narrow legacy family and remain unsupported even with a manifest.
- `parser/parser_statements_parseforheaderexpr_to_parsebracematchstructpatternarg.go:374` and semantic region-polymorphism sites use `__auto_<byte-offset>`; quantifiers use `__quantifier_<byte-offset>`; effects use `__handler_capture_<counter>`; protocol arguments use `__protocol_arg_<ordinal>`. These are candidate families only after actual emitter-name provenance is checked, since LLVM naming can add suffixes and source users can imitate the spellings.
- `semantic/analyzer_hooks.go:78/87` creates EMBEDDED `<base>__<source>__to__<target>__L<line>_C<column>` / `<base>__for__<target>__L<line>_C<column>` names. These are intentionally unsupported; the manifest does not override stable-family grammar. Supporting them requires producer-specific decomposition, exact stable base/source/target comparison and ABI review, not substring deletion.
- Multiple-index names (`__with_<line>_<col>`, `__anon_checkpoint_<line>_<col>_<index>`, `__grouped_checkpoint_<line>_<col>_<index>`) and nursery names carrying an internal NUL sentinel are likewise outside the supported complete-name grammar.
- `backend/llvm_types_packedenumabimode_to_addglobal.go:474-488`: defined functions default to external linkage; preferPrivateLinkage can choose private, but main, direct exports and default native runtime support exports stay external. Therefore an internal-looking generated spelling cannot establish ABI safety. The checker enforces actual private/internal IR linkage for global renames.

These observations do not establish complete producer coverage. V2 is a conservative offline oracle for a supported subset, not yet a replacement proving the full edit matrix. Inputs/diagnostics rejected as unsupported must be reported as unqualified, never normalized again until they happen to match.

## Streaming qualification

The original18 fixtures and19 streaming/header cases pass, including unchanged-binding
collisions, cross-function globals, forward types, nameless function/declaration
ordinals, exact nonidentifier bytes, denied external ABI renames, producer and
stack-ordinal checks, and bounded stream reads. The positive fixture pair also
passes LLVM assembly. Tests do not establish provenance for a real compiler pair.

Version2 adds a required `producer` field to each exact rename row. Supported
local producer grammars are `auto-region`, `region-extra-stack`, and `quantifier`.
A producer tag constrains spelling; it does not independently establish source
origin. Real manifests still require reviewed binding/source-site evidence. The
tool has no automatic authorization-from-differences mode.

Version2 manifest loading also rejects empty, whitespace-only and nonstring
producer tags; they cannot silently select the legacy version1 policy.

LLVM escaped backslashes are accepted and decoded for identifier identity,
while literal bytes remain exact. This follows the installed LLVM23.1.3
[lexer](https://raw.githubusercontent.com/llvm/llvm-project/llvmorg-23.1.3/llvm/lib/AsmParser/LLLexer.cpp).
The escaped-backslash constant fixture independently passes LLVM assembly.

Function headers distinguish leading formal types, optional local parameter
names, and recognized typed attributes such as sret/byval. Aggregate return
types and unnamed named-type formals are supported. Unknown percent-containing
attribute contexts and local/type name collisions remain unsupported. Seven
header regressions pass; their valid module fixtures independently assemble
with LLVM23.1.3. No real Stage1 pair equivalence is implied by these fixtures.

Checkpointed qualification uses `--checkpoint proof.json --budget-seconds 4`
inside an external30second timeout. Exit3 means incomplete; equivalence is
reported only after checked EOF and consumption of every manifest entry.
The work budget excludes mandatory input hashing/index/cursor validation,
which adds startup time. Inputs, manifest and checker hashes bind progress;
module-wide binding maps survive batches. All46 tests pass, including resume,
corruption, changed identities, cross-batch collisions, startup budgeting and
input-symlink retargeting. Checkpoints are trusted local proof bookkeeping with
corruption checksums, not adversarial signatures or compiler cache authority.

The classifier reuses the complete read-only module type index rather than
copying it for every unit. Definitions missing from a supplied complete index
are rejected; legacy whole-module classification still builds its own index.
A controlled2,000-unit/10,000-type fixture improves median classification from
0.219646 to0.034788seconds across three samples. This is an offline checker
improvement, not a Stage1 compiler latency claim.
