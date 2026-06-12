package main

import "testing"

// `common(...)` on an otherwise value-backed hierarchy promotes it to the region-backed store
// machinery (the value layout has no slot for commons). The classic shape: a Form-like enum whose
// only recursion is THROUGH a struct (Seq) holding darray[Form] — previously the constructor
// rejected the common arg ("expects 1 arguments, got 2") and `.pos` reads failed.
func TestCommonFieldPromotesStructIndirectHierarchy(t *testing.T) {
	runEnumHierarchyProgram(t, "common_promote_struct.elisa", `
struct P:
    offset: mutable u32

enum FormRoot:
    common(pos: P)

enum Form is FormRoot:
    Atom(k: i64)
    Group(body: Seq)

struct Seq:
    pos: mutable P
    items: mutable darray[Form]

def make_atom(p: P, k: i64) -> Form:
    return Form.Atom(pos: p, k: k)

@test
def bt() -> void:
    can Abort.Panic, Memory.Allocate:
        a: Form = make_atom(P(7), 3)
        assert_eq(a.pos.offset, 7)
`)
}

// Same promotion for recursion through a direct darray[Form] payload (also value-backed before).
func TestCommonFieldPromotesDArrayIndirectHierarchy(t *testing.T) {
	runEnumHierarchyProgram(t, "common_promote_darray.elisa", `
struct P:
    offset: mutable u32

enum FormRoot:
    common(pos: P)

enum Form is FormRoot:
    Atom(k: i64)
    Group(items: darray[Form])

def make_atom(p: P, k: i64) -> Form:
    return Form.Atom(pos: p, k: k)

@test
def bt() -> void:
    can Abort.Panic, Memory.Allocate:
        a: Form = make_atom(P(7), 3)
        assert_eq(a.pos.offset, 7)
        xs: mutable darray[Form] = []
        xs.push(a)
        assert_eq(xs[0].pos.offset, 7)
`)
}

// Cross-hierarchy extension-method calls: a method on store-backed E whose body reaches a SECOND
// store-backed hierarchy F (via a struct payload + method call) must receive F's store threaded.
// Impl methods live in the per-receiver registry, not the global namespace, so the call-graph
// fixpoint needs the extension-method edge union (previously: "new[auto] packed allocation has no
// active inferred region arena" at LLVM lowering).
func TestCrossHierarchyMethodStoreThreading(t *testing.T) {
	runEnumHierarchyProgram(t, "cross_store_methods.elisa", `
struct P:
    offset: mutable u32

enum FRoot:
    common(pos: P)

enum F is FRoot:
    Atom(k: i64)
    Wrap(body: Seq)

struct Seq:
    pos: mutable P
    items: mutable darray[F]

enum ERoot:
    common(pos: P)

enum E is ERoot:
    Num(x: i64)
    Syn(raw: Seq)
    Add(a: E, b: E)

impl F:
    def total(self: F) -> i64:
        match self:
            F.Atom(k):
                return k
            F.Wrap(body):
                t: mutable i64 = 0
                for it in body.items:
                    t <- t + it.total()
                return t

impl E:
    def total(self: E) -> i64:
        match self:
            E.Num(x):
                return x
            E.Syn(raw):
                t: mutable i64 = 0
                for it in raw.items:
                    t <- t + it.total()
                return t
            E.Add(a, b):
                return a.total() + b.total()

def build_seq(owner: mutable Arena&) -> Seq:
    can Abort.Panic, Memory.Allocate:
        items: mutable darray[F] = []
        items_ref: mutable darray[F]& = &items
        _ = arena_da_append(owner, items_ref, F.Atom(pos: P(1), k: 4))
        return Seq(P(0), items)

def build_e(owner: mutable Arena&) -> E:
    can Abort.Panic, Memory.Allocate:
        return E.Add(pos: P(9), a: E.Num(pos: P(2), x: 1), b: E.Syn(pos: P(3), raw: build_seq(owner)))

@test
def bt() -> void:
    can Abort.Panic, Memory.Allocate:
        with arena scratch(8192) as owner:
            e: E = build_e(owner)
            assert_eq(e.total(), 5)
`)
}

// A `try f() else g()` whose FALLBACK creates an implicit region-backed store must not leak that
// store's SSA value past the merge (emitRecoveryClause snapshots packed stores; previously the
// LLVM verifier failed with "Instruction does not dominate all uses").
func TestRecoveryClauseStoreSnapshot(t *testing.T) {
	runEnumHierarchyProgram(t, "recovery_store_snapshot.elisa", `
struct P:
    offset: mutable u32

enum NRoot:
    common(pos: P)

enum N is NRoot:
    Lit(x: i64)
    Pair(left: N, right: N)

error ProbeError:
    Boom

def build_n(owner: mutable Arena&, fail: bool) -> N error[ProbeError]:
    can Abort.Panic, Memory.Allocate:
        if fail:
            raise ProbeError.Boom
        return N.Lit(pos: P(2), x: 21)

def fallback_n(owner: mutable Arena&) -> N:
    can Abort.Panic, Memory.Allocate:
        return N.Lit(pos: P(5), x: 1)

def read_n(n: N) -> i64:
    match n:
        N.Lit(x):
            return x
        N.Pair(left, right):
            return read_n(left) + read_n(right)

@test
def bt() -> void:
    can Abort.Panic, Memory.Allocate:
        with arena scratch(8192) as owner:
            a: N = try build_n(owner, false) else fallback_n(owner)
            b: N = try build_n(owner, true) else fallback_n(owner)
            assert_eq(read_n(a) + read_n(b), 22)
            assert_eq(read_n(a) + read_n(b), 22)
`)
}

// clone of a store-backed enum value is a handle copy (nodes are immutable; sharing is the handle
// model). Value enums with owning payloads stay rejected — this pins the accepted case.
func TestCloneStoreBackedEnumHandle(t *testing.T) {
	runEnumHierarchyProgram(t, "clone_packed_handle.elisa", `
enum N:
    Lit(x: i64)
    Pair(left: N, right: N)

def build(owner: mutable Arena&) -> N:
    can Abort.Panic, Memory.Allocate:
        return N.Pair(left: N.Lit(x: 2), right: N.Lit(x: 3))

def total(n: N) -> i64:
    match n:
        N.Lit(x):
            return x
        N.Pair(left, right):
            return total(left) + total(right)

@test
def bt() -> void:
    can Abort.Panic, Memory.Allocate:
        with arena scratch(8192) as owner:
            n: N = build(owner)
            c: N = clone[N](n)
            assert_eq(total(c), 5)
`)
}

// Stage 2: unannotated common(...) fields default to the COLD side table (a parallel dense array
// indexed by the node handle), not inline in the record. This pins the AoS round-trip through nested
// construction — where a parent record is allocated before its children, so the side write must be
// addressed by each node's OWN handle, not "last allocated". span and weight live in the side array;
// the read assembles them back per node.
func TestColdCommonsSideTableAoSRoundTrip(t *testing.T) {
	runEnumHierarchyProgram(t, "cold_commons_aos.elisa", `
enum N:
    common(span: i64, weight: i64)
    Lit(x: i64)
    Pair(left: N, right: N)

def build(owner: mutable Arena&) -> N:
    can Abort.Panic, Memory.Allocate:
        return N.Pair(span: 100, weight: 7, left: N.Lit(span: 10, weight: 1, x: 2), right: N.Lit(span: 20, weight: 2, x: 3))

def span_sum(n: N) -> i64:
    match n:
        N.Lit(x):
            return n.span + n.weight
        N.Pair(left, right):
            return n.span + n.weight + span_sum(left) + span_sum(right)

@test
def bt() -> void:
    can Abort.Panic, Memory.Allocate:
        with arena scratch(8192) as owner:
            n: N = build(owner)
            # spans 100+10+20 = 130, weights 7+1+2 = 10 -> 140
            assert_eq(span_sum(n), 140)
`)
}
