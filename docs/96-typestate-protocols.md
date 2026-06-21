# 96 — Typestate / Protocol Types (the third orthogonal axis)

## 1. Where this sits

Elisa's verification model has three orthogonal axes. A claim about a program is the
**conjunction** of facts drawn from these axes, and a fact in one axis never launders into
another (docs/85, the honesty layer — every fact carries its discharge class):

| Axis | Question it answers | Primitive | Discharge class |
|------|--------------------|-----------|-----------------|
| **Value laws** | Is this *value* in range / shaped right? | `law`, refinement `T is Law[..]` | flow / linear / SMT / contract |
| **Sequence typestate** | Is this *operation* legal *now*, given history? | `typestate` (this doc) | named-state |
| **Effect capabilities** | Is this *effect* permitted here? | `permission`, `forbids`, `alias` | effect |

This document specifies the **sequence-typestate** axis: making illegal operation
*sequences* compile errors. `socket.send()` is rejected unless the socket is `Connected`;
`close()` twice is impossible; a builder is consumed exactly once.

Crucially this is **not a new prover**. Typestate lowers entirely onto machinery Elisa
already has and trusts:

- **named-state structs** — `struct T[state A | B]` with a `derive state:` discriminant,
- **the `is` operator** — Elisa's single application operator, so a typestate check reads
  `if sock is Connected:`,
- **strict protocol balance** — a `mutable T[S]&` parameter must be handed back in its
  declared state unless the function declares the transition with `ensures p => NewState`
  (docs/87 §7 dual; implemented in `appendImplicitPreservePoststates`),
- **affine/borrow consumption** — for the "consumed exactly once" flavor.

`typestate` is therefore **pure front-end sugar** over `struct[state…]` + `derive state` +
transition functions. The whole point of an increment that lowers onto a proven substrate is
that soundness is inherited, not re-argued.

## 2. Grammar

```
TypestateDecl  := "typestate" Ident ":" NEWLINE INDENT TypestateBody DEDENT
TypestateBody  := ( FieldDecl | StatesDecl | TransitionDecl )+
StatesDecl     := "states" ":" Ident ( "," Ident )* NEWLINE
TransitionDecl := "transition" Ident ":" Ident "->" Ident NEWLINE
FieldDecl      := <ordinary struct field line, e.g. `fd: mutable i64`>
```

- The `states:` list is the finite state set. The **first** state listed is the protocol's
  initial state (by convention; a constructor lands the value there).
- Each `transition name: From -> To` names an operation legal **only** in state `From`,
  leaving the resource in state `To`.
- `FieldDecl`s are ordinary per-instance data carried alongside the protocol state.

A `typestate` declaration must name **≥ 2 states**; every transition's `From`/`To` must be a
declared state (both checked in the parser — unknown state = hard error).

(Spelling note: `protocol` is already taken by static interfaces, so the typestate keyword is
`typestate`. The two are complementary — an interface constrains *shape*, a typestate
constrains *history*.)

## 3. Lowering

`typestate Name` desugars — in the parser, by generating the equivalent Elisa source and
re-parsing it (the same technique the grammar DSL uses, so the expansion is guaranteed
well-formed) — to:

1. a **state-bearing struct** `struct Name[state S0 | S1 | … ]:` carrying the declared
   fields plus a synthetic discriminant `__typestate: mutable i64`;
2. a `derive state:` block mapping each state to its discriminant value
   (`Si when self.__typestate == i`);
3. one **transition function** per `transition`, a free function
   `def t(self: mutable Name[From]&) ensures self => To:` whose body assigns the discriminant
   to `To`'s index.

### Worked example — `Socket` (Closed → Connecting → Connected → Closed)

Source:

```elisa
typestate Socket:
    fd: mutable i64
    states: Closed, Connecting, Connected
    transition connect:     Closed     -> Connecting
    transition established:  Connecting -> Connected
    transition close:        Connected  -> Closed
```

Desugars to:

```elisa
struct Socket[state Closed | Connecting | Connected]:
    fd: mutable i64
    __typestate: mutable i64

    derive state:
        Closed     when self.__typestate == 0
        Connecting when self.__typestate == 1
        Connected  when self.__typestate == 2

def connect(self: mutable Socket[Closed]&)        ensures self => Connecting:
    self.__typestate <- 1
def established(self: mutable Socket[Connecting]&) ensures self => Connected:
    self.__typestate <- 2
def close(self: mutable Socket[Connected]&)        ensures self => Closed:
    self.__typestate <- 0
```

Now the named-state machinery does all the work:

```elisa
def use(s: mutable Socket[Closed]&) ensures s => Closed:
    connect(s)        # s : Socket[Closed]      -> Socket[Connecting]   OK
    established(s)    # s : Socket[Connecting]  -> Socket[Connected]    OK
    close(s)          # s : Socket[Connected]   -> Socket[Closed]       OK
```

…and illegal sequences are rejected exactly as a type mismatch on the `From` parameter:

```elisa
def misuse(s: mutable Socket[Closed]&):
    established(s)    # ERROR: established requires Socket[Connecting], s is Socket[Closed]

def double(s: mutable Socket[Connected]&):
    close(s)          # s -> Socket[Closed]
    close(s)          # ERROR: close requires Socket[Connected], s is Socket[Closed]
```

`File` (Open → Closed) is the same pattern with two states; both are covered by the tests in
§6.

### Reading the state with `is`

Because the lowering uses named states, the canonical refinement-binding form works
unchanged (docs/80):

```elisa
if s is Connected:
    established_only_op(s)   # s narrowed to Socket[Connected] inside the arm
```

## 4. Discharge-class composition

A typestate fact is the **named-state** discharge class. Composition with the other axes is
by **conjunction**, with **no cross-class laundering** (docs/85):

- A `connect(s)` call requires (effect axis) whatever permissions its body uses **and**
  (typestate axis) `s is Closed`. Neither implies the other; both must hold.
- A value law proven on `s.fd` (value axis) says nothing about `s`'s protocol state, and
  vice-versa — a `Socket[Connected]` with an out-of-range `fd` is still a value-law error.
- `ensures self => To` is a named-state poststate (`FuncPoststateKindNamedState`), tracked
  distinctly from refinement poststates (`RefinementEnsures`) and effect poststates
  (`RefState`). They ride separate channels and are reported under their own class in the
  `--explain` proof report.

Soundness inheritance: because transitions are ordinary functions over `T[State]&`
parameters, **strict protocol balance** already forbids an undeclared state mutation
(`appendImplicitPreservePoststates`), and **externs get no implicit preserve** (an
unverifiable native body must not be assumed to leave the protocol state intact), so a
borrowed protocol resource crossing an extern is conservatively widened.

## 5. Implementation status (this increment)

Landed:

- **Parser** (`compiler/src/parser/parser_typestate_protocol.go`): the `typestate`
  declaration, validation (≥2 states, known transition endpoints), and the
  generate-source-and-re-parse desugaring. `pendingDecls` on the `Parser` (drained by
  `ParseFile`) lets one declaration expand to a struct + N transition functions at file
  scope.
- **Dispatch**: `parseDecl` routes the `typestate` keyword (`parser_parser_to_parseparamdeclblock.go`).
- **Tests**: parser desugaring + unknown-state rejection
  (`parser_typestate_protocol_test.go`); semantic end-to-end — legal sequence compiles,
  illegal transition / double-close are hard errors, File protocol round-trips
  (`semantic/typestate_protocol_runtime_test.go`).

Deliberately reused (not rebuilt): named-state typing, `derive state`, strict protocol
balance, `ensures p => State` poststates, `is` narrowing.

## 6. Next increments

1. **Affine "consume exactly once"**: mark a `typestate` `linear` so a value that reaches a
   terminal state (or is dropped) is a must-consume obligation — gives builder-consumed-once
   and forbids leaking a half-open socket.
2. **Method sugar**: allow `transition` bodies and `s.connect()` UFCS call form, and let a
   transition carry extra params / a return value.
3. **Initial-state constructor**: synthesize `Socket.new()` landing in the first declared
   state, so user code never touches `__typestate`.
4. **Terminal / required-transition checks**: flag a state with no outgoing transition that
   is reached on a non-terminal path (resource-leak lint).
5. **Conditional transitions**: `transition recv: Connected -> Connected | Closed` (on a bool
   return), lowering onto the existing conditional-poststate machinery.
6. **`--explain` integration**: surface protocol facts in the proof report under a `typestate`
   class label.
