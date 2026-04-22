# Typestate Practical Cheat Sheet

This is the short companion to `14-typestate-system.md`.

Read `14-typestate-system.md` if you want the full model.
Read this file if you want the operational version you can keep in your head while writing code.

The current headline is simple:

> Typestate is already useful today, but the main precision cliff is still **mutation through ref calls**.

That makes this guide good for two things:

- using the current system effectively
- spotting the exact places where future ref-parameter poststate `ensures` clauses would pay off

---

## 1. Which typestate mechanism should you use?

### Use **named states** when the meaning matters

Good fits:

- `Socket[Open]` / `Socket[Closed]`
- `ParseJob[Pending]` / `ParseJob[Ready]` / `ParseJob[Failed]`
- `ScratchBuffer[Uninitialized]` / `ScratchBuffer[Initialized]`

Use named states when:

- the state is semantic
- the state is part of an API contract
- you want to narrow with `is`
- the state is derived from fields and should track mutation

### Use **aggregate state placeholders** when the state is mechanical

Good fits from the current runtime surface:

- `Task[R, Pending]`
- `Thread[R, Joinable]`
- `MutexGuard[Held]`

Use aggregate state placeholders when:

- you want compact positional proof bits
- the state is mostly being threaded through wrappers
- you do **not** need a derived-state story attached to fields

Short version:

- **semantic protocol or invariant** → named state
- **mechanical carrier state** → aggregate state placeholder

---

## 2. Expect one of three outcomes after mutation

After a write, the compiler will try to do one of these:

1. **Preserve** the current state if the write cannot affect any `derive state:` condition.
2. **Recompute exactly** if the new state can be proven from the write.
3. **Widen** if the exact poststate is not trustworthy.

That gives you the practical rule of thumb:

- direct local writes can stay precise
- helper calls through refs are where precision usually drops today

---

## 3. The main widening trigger to remember

The biggest current precision loss is this pattern:

```context
def finish_ok(job: ParseJob[Pending]&) -> void:
    job.checksum <- 7
    job.stage <- 1

def bad(job: mutable ParseJob[Pending]) -> int:
    finish_ok((&job).cast[ParseJob[Pending]&])
    return take_ready(job)
```

Even if `finish_ok` obviously makes the job ready, the caller currently widens afterward because the mutation crossed a ref-call boundary.

So the caller sees something like:

```context
ParseJob[Pending | Ready | Failed]
```

That is conservative, but sound.

---

## 4. Pattern: Open / Closed resource

```context
struct Socket[state Open | Closed]:
    fd: mutable int
    bytes_sent: mutable int

    derive state:
        Open when self.fd >= 0
        Closed when self.fd < 0
```

### Direct mutation stays precise

```context
sock.fd <- -1
# sock is now Socket[Closed]
```

### Ref helper call widens today

```context
def close_socket(sock: Socket[Open]&) -> void:
    sock.fd <- -1

close_socket((&sock).cast[Socket[Open]&])
# caller now treats sock conservatively as Socket[Open | Closed]
```

Use this pattern when the state is a real resource protocol, not just a tag you want to carry around.

---

## 5. Pattern: Uninitialized / Initialized buffer wrapper

```context
struct ScratchBuffer[state Uninitialized | Initialized]:
    capacity: mutable int
    used: mutable int

    derive state:
        Uninitialized when self.capacity == 0
        Initialized when self.capacity > 0
```

### Good use

- wrappers that start empty and become usable after setup
- arena-backed scratch carriers
- state that is genuinely derived from fields already present in the struct

### Current caveat

```context
def init_buffer(buf: ScratchBuffer[Uninitialized]&) -> void:
    buf.capacity <- 64
    buf.used <- 0

init_buffer((&buf).cast[ScratchBuffer[Uninitialized]&])
# caller currently gets ScratchBuffer[Uninitialized | Initialized]
```

So today you should expect a re-check if you cross a helper call.

---

## 6. Pattern: Pending / Ready / Failed parser or async state

```context
struct ParseJob[state Pending | Ready | Failed]:
    stage: mutable int
    checksum: mutable int

    derive state:
        Pending when self.stage == 0
        Ready when self.stage == 1
        Failed when self.stage == 2
```

This is a strong dogfooding pattern because it is exactly the sort of API where callers will feel ref-call precision loss.

### Direct local transition can be exact

```context
job.checksum <- 7
job.stage <- 1
return take_ready(job)
```

### Helper call currently forces the “re-prove” style

```context
finish_ok((&job).cast[ParseJob[Pending]&])

if job is ParseJob[Ready]:
    return take_ready(job)
return 0
```

That `if job is ParseJob[Ready]` can feel redundant when you know what the helper does.
That is precisely the pain point a future ref-parameter poststate `ensures` system should target.

---

## 7. When should you expect widening?

Assume widening is likely when:

- a branch join combines different surviving states
- you mutate through an alias
- you pass a typestated value by reference into a call
- the mutation path is nested or complicated enough that exact proof is not easy

If you remember only one of these, remember the third one.

---

## 8. When should you reach for `is`?

Use `is` when:

- a value came in with a state set like `Player[Alive | Dead]`
- a prior mutation widened the state
- a ref helper call crossed a mutation boundary
- you need to re-establish an exact protocol state before calling a stricter function

Typical pattern:

```context
helper((&value).cast[SomeStatefulType[OldState]&])

if value is SomeStatefulType[NewState]:
    return use_new_state(value)
```

---

## 9. Common anti-patterns

### Anti-pattern: expecting helper calls to preserve exact derived state

If a helper mutates through a ref, the caller currently should not assume an exact poststate.

### Anti-pattern: using named states for purely mechanical carrier bits

If the state is just a compact wrapper tag like `Pending` on a runtime handle, an aggregate state placeholder is usually the simpler fit.

### Anti-pattern: making derive conditions too fancy

Prefer conditions that are:

- field-local
- obvious
- cheap to re-evaluate mentally
- mutually exclusive when exact states matter

### Anti-pattern: treating a successful earlier proof as permanent

Mutation can revoke typestate precision.
Proofs are not lifetime membership cards.

---

## 10. What is the next precision feature worth building?

If you want the single best next extension, it is this:

> add explicit **ref-parameter poststate `ensures` clauses** so helper calls can say what they do to caller-visible typestate.

Examples of the kind of effect worth expressing:

- “this ref argument becomes `Ready`”
- “this ref argument stays `Open`”
- “this ref argument widens to `Ready | Failed`”

That would improve the exact place where the current system is deliberately conservative, without giving up the soundness model already in place.

Crucially, this should be **statically checked effect typing**, not a runtime design-by-contract feature that merely crashes if the summary is wrong.

If you want the concrete first-cut surface and implementation sketch for that idea, see `16-ref-parameter-poststate-ensures.md`.

---

## 11. Pocket summary

- Use **named states** for real semantic protocols.
- Use **aggregate placeholders** for mechanical state threading.
- Expect exactness for simple direct writes.
- Expect widening after ref-call mutation.
- Re-prove with `is` when you cross that boundary.
- The best future improvement is explicit ref-call poststate `ensures` clauses.