package semantic

import "testing"

// docs/128: every part of a program-lived parameter (an AST node of a store-backed `@source`
// enum) is program-lived, so a callee cannot store anything shorter-lived through it. A
// recursive emitter handing scope-held AST nodes to a decline recorder that stores the node's
// kind into a longer-lived table therefore stores nothing of the scope (pos); a plain `sview`
// row of the same scope still does (neg).
const storeScanProgramLivedParamPos = `global region source

enum Node @source:
    Ident(name: sview @source)
    Wrap(inner: Node)
    Other

struct Diag:
    kind: mutable sview = ""

struct Structs:
    diagnostics: mutable Diag = Diag{}

struct Sc:
    nodes: mutable darray[Node] = []

def node_kind(e: Node) -> sview:
    match e:
        Node.Ident(name):
            text: mutable sview = ""
            text <- name
            return text
        _:
            return "other"

def remember(e: Node, structs: mutable Structs&) -> void:
    structs.diagnostics.kind <- node_kind(e)

def emit(sc: mutable Sc&, structs: mutable Structs&) -> void:
    can Memory.Allocate, Abort.Panic:
        if sc.nodes.count > 0:
            remember(sc.nodes[0], structs)

def declare(n: Node, structs: mutable Structs&) -> void:
    can Memory.Allocate, Abort.Panic:
        local_sc: mutable Sc = Sc{nodes: []}
        local_sc.nodes.push(n)
        emit(local_sc, structs)
`

const storeScanProgramLivedParamNeg = `global region source

enum Node @source:
    Ident(name: sview @source)
    Wrap(inner: Node)
    Other

struct Diag:
    kind: mutable sview = ""

struct Structs:
    diagnostics: mutable Diag = Diag{}

struct Sc:
    nodes: mutable darray[Node] = []
    names: mutable darray[sview] = []

def node_kind(e: Node) -> sview:
    match e:
        Node.Ident(name):
            text: mutable sview = ""
            text <- name
            return text
        _:
            return "other"

def remember(e: Node, structs: mutable Structs&) -> void:
    structs.diagnostics.kind <- node_kind(e)

def remember_name(name: sview, structs: mutable Structs&) -> void:
    structs.diagnostics.kind <- name

def emit(sc: mutable Sc&, structs: mutable Structs&) -> void:
    can Memory.Allocate, Abort.Panic:
        if sc.nodes.count > 0:
            remember(sc.nodes[0], structs)
        if sc.names.count > 0:
            remember_name(sc.names[0], structs)

def declare(n: Node, structs: mutable Structs&) -> void:
    can Memory.Allocate, Abort.Panic:
        local_sc: mutable Sc = Sc{nodes: []}
        local_sc.nodes.push(n)
        emit(local_sc, structs)
`

func TestStoreScanProgramLivedParamCarriesNothing(t *testing.T) {
	pos := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_pl_param_pos.elisa", storeScanProgramLivedParamPos, AnalyzeOptions{})
	if got := pos.analyzer.calleeReturnModeOf(storeScanFindFunc(t, pos, "node_kind"), 0, map[storeScanKey]bool{}); got != storeNone {
		t.Fatalf("pos: node_kind returns %v of its program-lived node, want none", got)
	}
	if got := pos.analyzer.calleeStoreModeInto(storeScanFindFunc(t, pos, "remember"), 0, 1); got != storeNone {
		t.Fatalf("pos: remember e->structs = %v, want none", got)
	}
	if got := pos.analyzer.calleeStoreModeInto(storeScanFindFunc(t, pos, "emit"), 0, 1); got != storeNone {
		t.Fatalf("pos: emit sc->structs = %v, want none", got)
	}
	neg := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_pl_param_neg.elisa", storeScanProgramLivedParamNeg, AnalyzeOptions{})
	if got := neg.analyzer.calleeStoreModeInto(storeScanFindFunc(t, neg, "emit"), 0, 1); got == storeNone {
		t.Fatalf("neg: emit sc->structs = none, want a store")
	}
}
