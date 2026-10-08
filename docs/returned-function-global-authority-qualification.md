# Returned function Global authority

A callback writer assigned locally to a declared Global.Read function type retained its actual Global.Write origin, but returning that writer from a factory declared to return the same type erased it. Actual default CLI accepted `f=factory(); f() can Global.Read` despite the writer's mutable-global update.

Returned callbacks now have a separate mandatory Global capability summary. Call-result function values retain canonical factory identities, resolved against the completed inference fixpoint. Function-value joins and type substitution preserve these identities and summaries. Factory execution effects remain separate: manufacturing a callback is pure, while invoking it requires its concrete members. Family-based function assignment compatibility is unchanged. A trusted block around construction does not erase a returned callback's future capability.

Qualification used the private returned-function-authority-core tree and rebuilt /root/work/elisacore-returned-function-authority. Full semantic suite passed60.728s (/root/work/returned-function-authority-full.log). Eight focused cases, each with a wrong-member negative and groupedBoth positive, passed0.034s (/root/work/returned-function-authority-focused3.log): direct, nested, branch, qualified, factory alias, type alias, trusted construction, and read-only wrong-axis returns.

Actual default CLI changed the original factory bypass from exit0 to exit1; direct assignment and callback handoff negatives still reject, and Both invocation controls pass with pure construction. Durable fixtures/logs: /root/work/fn-member-provenance-probes-fixed/. A further valid higher-order factory forwarding probe also rejects the missingWrite invocation. The fully migrated compiler source admitted successfully (exit0), /root/work/returned-function-authority-compiler-admission.log.

This is source/policy qualification. The private binary carries candidate provenance; integration owns the clean rebuild and installation.
