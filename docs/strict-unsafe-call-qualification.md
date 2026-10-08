# Strict Unsafe caller authority

The prior validator rejected direct unsafe operations under strict enforcement but only warned at ordinary calls, including calls whose sole inferred row was Unsafe.PointerCast. A helper with a local tracked cast grant therefore did not require its caller to grant that member under `-emit unsafe`.

Call validation now rejects any missing Unsafe member under EnforceUnsafePermissions, independently of other advisory families. Generic-context warning suppression runs after mandatory and strict checks. Direct operation validation likewise recognizes Unsafe among mixed missing families. Default mode retains advisory ordinary Unsafe call rows; existing mandatory Global and segment rules remain mandatory.

Regression coverage checks direct and transitive calls, inferred function values, mixed Console.Write rows, capability aliases, wrong-member grants, matching grants, exact trusted suppression, and unrelated trusted members under both default and strict modes.

Private combined ownership/policy qualification: full semantic suite passed (59.138 s), durable log `/root/work/strict-unsafe-review/full.log`. This is source qualification, not an installed product provenance claim.

The strengthened nonambient Console.Write mixed-row regression passed (0.033 s). Actual CLI checks against the private rebuilt candidate accepted all five fixtures in default semantic mode. Strict unsafe mode rejected direct, wrong-member, and inferred function-value calls (exit 1); matching PointerCast grants and exact trusted suppression passed (exit 0). Fixtures and durable outputs are under `/root/work/strict-unsafe-review/*-qualified.elisa` and `*-{semantic,unsafe}.log`.
