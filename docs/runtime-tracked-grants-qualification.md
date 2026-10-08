# Runtime tracked-grant qualification

This checkpoint follows `ec7a2b199fbc82be64d22574f6d5db64f6357812`
(after `8c8657a9` and `944853e7`, based on `61b449f5`). Qualification
also includes the independently committed ownership correction `e6a380d8`.
No shared main checkout or installed compiler was changed.

The 18 runtime files replace the prior convenience Global suppression with
tracked local grants at the actual reads, writes, borrows and effectful calls.
Repeated members use `Global{Read,Write}`. Concrete function effect headers remain
optional; inferred rows reach callers. Existing intentional named Unsafe
suppression is retained separately from Global authority.

The complete runtime closure initially reported 172 Global grant gaps. Narrow
grants closed 38 arena lock operations, 37 transitive arena calls, then the
remaining 97 calls across arena, collections, deque, heap, packed stores and the
runtime prelude. The final closure has no missing Global grants.

Removing the runtime directory exemption also exposed 41 default-hard pointer
conversions on 38 expression lines. Raw packed-store state/handles, public Barrier
address carriers, join contexts and work-state conversions use tracked local
`can Unsafe.PointerCast`. A raw address or opaque state pointer does not establish
allocation provenance, so those APIs do not hide that effect. Writable packed
column casts explicitly produce writable references; read-only consumers retain
read-only targets. Two join/await function values now infer their effect rows
instead of narrowing them with stale explicit Fn annotations, and their actual
invocation grants PointerCast locally.

Only two new allocation-owned conversions deliberately suppress PointerCast.
`arena_mmap_owned_region` computes the checked Region allocation size, obtains a
fresh aligned mapping and rejects null/MAP_FAILED before a one-line trusted
return. `ctx_pool_state_allocate` obtains a nonnull allocation of exactly
`size_of(ConcurrencyPoolState)` before its one-line trusted return. Neither accepts
an arbitrary raw carrier. The generic integer/reference promotion helpers remain
tracked. These helpers are necessary because trusted is a statement-block grant;
unsupported inline trusted syntax was rejected during qualification and removed.

The bodyless Str protocol explicitly bounds Global Read/Write, while Store's
total handle lookup explicitly bounds Abort.Panic to admit the bounds-checked
deque implementation. Concrete implementations infer their actual rows, and
protocol subset validation runs after inference.

All qualification ran privately on Linux/Vast:

- Full combined semantic suite: PASS, 59.410 seconds;
  `/root/work/runtime-migration-combined-semantic-full.log`.
- Whole runtime default CLI semantic admission: exit 0;
  `/root/work/runtime-default-deliberate-boundaries5.log`.
- A generic print caller granted Console/Memory/Abort but missing Global rejects
  with exactly one mandatory Global diagnostic. The same caller with grouped
  Global Read/Write passes. Fixtures/logs:
  `/root/work/runtime-migration-qualification/{missing,granted}.{elisa,log}`.
- Windows arena backend admission passed during the narrow arena-call migration.

The experimental qualification binary combines policy and ownership sources:
`/root/work/elisacore-policy-ownership`. It is not a clean provenance-stamped
installation product. A final integration checkout must build and qualify its
own compiler/runtime pair.

The broader strict Unsafe inventory remains a separate deliberate boundary
review. Default admission success does not claim that `-emit unsafe`/strict
permission admission is complete. No runtime directory exemption or blanket
trusted module grant has been reintroduced. No performance claim is made.
