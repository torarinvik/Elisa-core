# Lazy SMT quantifier-binder scan

The helper now rejects terms without `q_` immediately and uses `strings.FieldsFuncSeq` to inspect tokens lazily. The delimiter predicate and existing binder-declaration scan remain unchanged. This preserves the original textual treatment of whitespace, quoted fragments, malformed terms and globally unique binder names. Go 1.25 is already the module minimum.

A historical, source-matched semantic profile attributed 2.15 GB of allocations to this helper's `FieldsFunc` token slices. This identifies the target; it is not a current whole-build saving.

Validation: 62 focused SMT tests passed with zero skips, including 3,000 deterministic differential inputs against the original implementation. Cases include bound/free binders, exact delimiters, tabs/newlines, Unicode, invalid UTF-8 bytes and malformed terms. The production function and tests are unchanged from that functional gate; the later benchmark-only addition exercises a `q_` token after a long ordinary prefix.

Matched ABBA timing used exact production-function extraction into a stdlib-only package, prebuilt binaries, six observations per arm/case, Go2 workers and a 1 GiB soft memory limit on darwin/arm64. Both extracted function bodies were checked byte-for-byte against their frozen production files. There was no competing compiler/VM or declared monitor breach in the completed run.

| Case | Baseline ns/op | Candidate ns/op | Baseline bytes/op | Candidate bytes/op |
|---|---:|---:|---:|---:|
| plain-short | 69.010 | 3.945 | 64 | 0 |
| plain-long | 8459.000 | 28.460 | 24832 | 0 |
| free-early | 9371.000 | 1659.000 | 38400 | 0 |
| bound-one | 118.750 | 87.030 | 80 | 0 |
| bound-many | 12612.000 | 7802.000 | 41608 | 3208 |
| free-last | 12650.000 | 7696.000 | 41608 | 3208 |
| free-late-plain | 9469.000 | 4800.500 | 38400 | 0 |

These are helper-level measurements, not whole-compiler or Stage1 timings. The binder-name map still allocates for many bound variables; the token slices are removed. Earlier attempts produced no timing rows because startup load or an active Stage1 compiler prevented qualification; their failure receipts are retained.

`compiler/scripts/prepare_smt_binder_atom.py OUTPUT` prepares this exact helper, its differential tests, benchmark and a SHA-256 manifest in a new directory. It performs no build itself. The manifest gives test and benchmark commands. This avoids rebuilding the entire semantic test package while iterating on the atom; production SMT integration remains a separate gate. In the captured artifact preparations, atom binaries each built in about 0.76 seconds versus 18–21 seconds for the full semantic package. Those build observations were not a controlled cache-state benchmark.

Evidence: `/private/tmp/elisa-smt-binder-scan-20261019`, functional `candidate-v1-receipt.json`, extraction `atom-sources.json`, build `atom-build-receipt.json`, completed timing `timing-atoms-02/timing-{receipt,summary}.json`. Source and binary hashes are recorded in those receipts.
