package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/regionframesyntax"
	"fmt"
	"os"
	"reflect"
	"sort"
)

// objectMayEscape reports whether any reference to `name` could outlive an early free: an address-of
// (`&name` / `&name[i]`), or any occurrence of the identifier that is NOT a method/field receiver
// (`name.x`) or an index base (`name[i]`) — e.g. a bare value use, an assignment source, or a call
// argument, all of which can copy the buffer pointer out.
func objectMayEscape(body []ast.Stmt, name string) bool {
	return regionframesyntax.ObjectMayEscape(body, name)
}

// subtreeReferencesIdent reports whether any *ast.Ident named `name` appears anywhere in node.
func subtreeReferencesIdent(node ast.Node, name string) bool {
	return reflectSubtreeHasIdent(reflect.ValueOf(node), func(candidate string) bool { return candidate == name })
}

func reflectSubtreeHasIdent(value reflect.Value, matches func(string) bool) bool {
	return regionframesyntax.SubtreeHasIdent(value, matches, syntaxWalkFields)
}

// collectInteriorRefNames returns the tracked container names that have an interior reference
// taken into them anywhere in body — i.e. `&name[...]` (an address-of an index expression). These
// are the darrays whose backing must not relocate for the reference to stay valid (Phase C).
func collectInteriorRefNames(body []ast.Stmt, tracked map[string]bool) map[string]bool {
	return regionframesyntax.InteriorRefNames(body, tracked)
}

// hardBoundForName returns the provable maximum element count N for a fresh darray `name` when its
// ONLY element growth is `name.push(...)` inside pure counting loops `for _ in 0..<N` (N a pure
// ident/int), with no unbounded growth operations. Multiple push sites are summed, nested counting
// loops multiply their bounds, and `push([a, b])` scales by the literal element count. Such a darray
// can never hold more than the bound's elements, so a reserve_commit reservation sized to it can
// never overflow. Returns the bound expression and ok; otherwise ok=false.
func hardBoundForName(body []ast.Stmt, name string) (ast.Expr, bool) {
	s := &hardBoundScan{name: name}
	s.walk(reflect.ValueOf(body))
	if s.disqualified || s.pushSites == 0 || s.bound == nil {
		return nil, false
	}
	return s.bound, true
}

type hardBoundScan struct {
	name         string
	loopStack    []ast.Node // enclosing loops at the current position
	pushSites    int
	bound        ast.Expr
	disqualified bool
}

func (s *hardBoundScan) checkCall(call *ast.CallExpr) {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		return
	}
	recv, ok := field.Object.(*ast.Ident)
	if !ok || recv.Name != s.name {
		return
	}
	switch field.Field {
	case "push":
		s.pushSites++
		if len(s.loopStack) == 0 {
			s.disqualified = true
			return
		}
		var siteBound ast.Expr = semanticIntReserveExpr(hardBoundPushElementCount(call), call.Pos())
		for i := len(s.loopStack) - 1; i >= 0; i-- {
			b, ok := countingLoopBound(s.loopStack[i])
			if !ok {
				s.disqualified = true
				return
			}
			siteBound = semanticMultiplyReserveExpr(b, siteBound, call.Pos())
		}
		s.bound = semanticAddReserveExpr(s.bound, siteBound, call.Pos())
	case "push_back", "push_front", "extend", "append", "append_many", "insert", "resize", "reserve", "as_cstr", "cstr":
		s.disqualified = true // adds or reserves an unbounded / non-counting amount
	}
}

func hardBoundPushElementCount(call *ast.CallExpr) int {
	return regionframesyntax.HardBoundPushElementCount(call)
}

func (s *hardBoundScan) walk(v reflect.Value) {
	if s.disqualified || !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		// Unwrap to the concrete pointer only — the checks below run at the Pointer case so each
		// AST node is processed exactly once (a node reached as both interface and pointer would
		// otherwise push its loop onto loopStack twice).
		if !v.IsNil() {
			s.walk(v.Elem())
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		iface := v.Interface()
		if loop, ok := iface.(*ast.ForStmt); ok {
			s.loopStack = append(s.loopStack, loop)
			s.walk(reflect.ValueOf(loop.Body))
			s.loopStack = s.loopStack[:len(s.loopStack)-1]
			return
		}
		if loop, ok := iface.(*ast.IterForStmt); ok {
			s.loopStack = append(s.loopStack, loop)
			s.walk(reflect.ValueOf(loop.Body))
			s.loopStack = s.loopStack[:len(s.loopStack)-1]
			return
		}
		if call, ok := iface.(*ast.CallExpr); ok {
			s.checkCall(call)
		}
		if node, ok := iface.(ast.Node); ok && isLoopStmtNode(node) {
			s.loopStack = append(s.loopStack, node)
			s.walk(v.Elem())
			s.loopStack = s.loopStack[:len(s.loopStack)-1]
			return
		}
		s.walk(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			s.walk(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			s.walk(v.Index(i))
		}
	}
}

// boundInScopeAtRegionEntry reports whether a reservation bound can be evaluated at the region's
// entry (where the reserve_commit arena is created): an integer literal always, an identifier
// that is a function parameter, or arithmetic over entry-available terms. A local declared inside
// the region body is not yet in scope there, so it does not qualify for reserve_commit (the darray
// stays chained — a sound, graceful fallback; deferring the reservation to the darray's first use
// to support local bounds is a follow-up).
func boundInScopeAtRegionEntry(bound ast.Expr, params map[string]bool) bool {
	return regionframesyntax.BoundInScopeAtRegionEntry(bound, params)
}

func countingLoopBound(node ast.Node) (ast.Expr, bool) {
	return regionframesyntax.CountingLoopBound(node)
}

func stripParenExpr(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.Inner
	}
}

// regionLifetimeClasses counts the DISTINCT lifetime equivalence classes a single region resolves
// its allocations to. A class is a distinct death-point at which live objects are reclaimed:
//   - one per distinct B2 early-free offset (objects that die before region exit), plus
//   - one for the region exit itself, iff some allocated stack survives to it.
//
// Stacks within a region are layout (all freed together at region exit, docs/71) and do NOT count
// as separate lifetimes — only distinct *death-points* do. A region with no fresh allocations
// contributes 0.
func (asn RegionStackAssignment) regionLifetimeClasses() int {
	earlyOffsets := map[int]bool{}
	for _, off := range asn.StackEarlyFreeAfter {
		earlyOffsets[off] = true
	}
	survivesToExit := false
	for _, stack := range asn.StackOf {
		if _, early := asn.StackEarlyFreeAfter[stack]; !early {
			survivesToExit = true
			break
		}
	}
	classes := len(earlyOffsets)
	if survivesToExit {
		classes++
	}
	return classes
}

// dumpRegionLifetimeSummary prints the program-wide unified-lifetime count: the total number of
// distinct lifetime equivalence classes that every inferred allocation resolves to (regions
// refined by B2 early-free), alongside the raw region/stack/allocation totals so the layout axis
// (stacks) is not mistaken for the lifetime axis (classes).
func (a *Analyzer) dumpRegionLifetimeSummary() {
	regions, stacks, allocations, classes := 0, 0, 0, 0
	for _, asn := range a.regionStacks {
		regions++
		stacks += asn.StackCount
		allocations += len(asn.StackOf)
		classes += asn.regionLifetimeClasses()
	}
	fmt.Fprintf(os.Stderr, "region lifetime summary: %d allocation(s) across %d region(s) (%d stack(s) for layout) -> %d unified lifetime class(es)\n",
		allocations, regions, stacks, classes)
}

func (a *Analyzer) dumpRegionStackAssignment(region *ast.RegionStmt, asn RegionStackAssignment) {
	note := ""
	if asn.Merged {
		note = " [over-split cap hit -> merge stack]"
	}
	fmt.Fprintf(os.Stderr, "region %s: %d stack(s)%s\n", region.Name, asn.StackCount, note)
	byStack := map[int][]string{}
	for name, s := range asn.StackOf {
		byStack[s] = append(byStack[s], name)
	}
	stacks := make([]int, 0, len(byStack))
	for s := range byStack {
		stacks = append(stacks, s)
	}
	sort.Ints(stacks)
	for _, s := range stacks {
		sort.Strings(byStack[s])
		ef := ""
		if off, ok := asn.StackEarlyFreeAfter[s]; ok {
			ef = fmt.Sprintf(" early-free@%d", off)
		}
		fmt.Fprintf(os.Stderr, "  stack %d (%s, %s)%s: %v\n", s, asn.StackKind[s], asn.stackStrategy(s), ef, byStack[s])
	}
}

// earlyFreePlaceRoot: the local a place expression (`a`, `a.b[i]`, `(a)`) is rooted in.
func earlyFreePlaceRoot(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.FieldExpr:
		return earlyFreePlaceRoot(x.Object)
	case *ast.IndexExpr:
		return earlyFreePlaceRoot(x.Object)
	case *ast.ParenExpr:
		return earlyFreePlaceRoot(x.Inner)
	}
	return ""
}
