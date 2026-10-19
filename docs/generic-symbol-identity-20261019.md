# Structural generic symbol identity

Generic specialization previously used punctuation-sanitized diagnostic type names as cache and LLVM symbol identities. `u8` and `u8&` collided. A native `width[T]` regression returned the wrong answer in both instantiation orders; the baseline emitted one body instead of distinct 1-byte and 8-byte implementations.

Specializations now use tagged, length-delimited structural identities including the qualified base, argument vector, and implementation owner/method where applicable. Bounded SHA-256 symbols retain an exact-key collision check. Nominal LLVM carrier identities erase lifetime provenance separately from semantic specialization identity, preserving compatible region-bearing layouts. Existing generic extern linker spellings remain compatible; conflicting lowered signatures, calling conventions, and ABI attributes are rejected.

Validation on frozen candidate v10: all 407 backend tests passed, including native scalar/reference and generic inline-layout collision cases in both orders, extern ABI controls, and region-bearing carrier regressions. The semantic TypeSymbolKey suite passed. All 2,661 recorded source entries matched before and after. Receipt: `/private/tmp/elisa-generic-symbol-identity-20261019/candidate-v10-receipt.json`; manifest SHA-256: `2caa6d54d22c11ef283d5f53ebdd7c3a5617f163056d300f63d842581c36b95f`.

This is a correctness prerequisite for trustworthy specialization and incremental reuse, with no throughput claim. These symbol keys are not persistent layout invalidation fingerprints. Other optional/error/packed-view naming sites remain separate follow-up work. Stage1 compilation is not yet validated by this change.
