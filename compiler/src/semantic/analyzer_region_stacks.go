package semantic

import (
	"fmt"
	"os"
	"reflect"
	"sort"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// regionStackCap bounds the number of own (growable-tail) stacks per region before the overflow
// growables are merged into one shared CHAINED stack — so a function with many small growables
// does not allocate many separate blocks (docs/71, over-split guard).
const regionStackCap = 4

// RegionStackAssignment partitions an inferred region's fresh container allocations into parallel
// bump-stacks (lifetime inference, Phase B; docs/71). Stack 0 is the shared stack for
// fixed-footprint allocations (reserved growables); stacks 1..N each hold one unreserved growable
// as its tail so it can grow freely. When the over-split cap is hit, the overflow growables share
// one merge stack and Merged is set. B1a computes and records this; B1b consumes it in codegen.
type RegionStackAssignment struct {
	StackOf       map[string]int   // allocation name -> stack id (0 == shared fixed/reserved stack)
	StackKind     map[int]string   // stack id -> "shared" | "growable" | "merge"
	StackStrategy map[int]string   // stack id -> "chained" (default) | "reserve_commit" (Phase C)
	StackCapacity map[int]ast.Expr // reserve_commit stack id -> proven element-count bound N
	StackElemType map[int]Type     // reserve_commit stack id -> the darray's element type (for sizing)
	// StackEarlyFreeAfter (Phase B2): own-stack id -> byte offset of the top-level statement after
	// which the stack's arena can be freed early (the object dies before region exit and nothing
	// references it past then). Absent when the stack must live to region exit.
	StackEarlyFreeAfter map[int]int
	StackCount          int
	Merged              bool
}

// stackStrategy returns the backing strategy for a stack (chained unless Phase C assigned one).
func (asn RegionStackAssignment) stackStrategy(stack int) string {
	if s, ok := asn.StackStrategy[stack]; ok && s != "" {
		return s
	}
	return "chained"
}

var dumpRegionStacks = os.Getenv("ELISA_DUMP_REGION_STACKS") != ""

// assignRegionStacks computes the B1 partition for one inferred region: every unreserved growable
// gets its own stack, all reserved (fixed-footprint) allocations share stack 0, and growables
// beyond regionStackCap share a merge stack. Analysis-only (B1a) — recorded for codegen and
// inspection; no behavior change yet.
func (a *Analyzer) assignRegionStacks(region *ast.RegionStmt, paramNames map[string]bool, inLoop bool) RegionStackAssignment {
	asn := RegionStackAssignment{StackOf: map[string]int{}, StackKind: map[int]string{0: "shared"}, StackStrategy: map[int]string{}, StackCapacity: map[int]ast.Expr{}, StackElemType: map[int]Type{}, StackEarlyFreeAfter: map[int]int{}, StackCount: 1}
	order := make([]string, 0, 4)
	seen := map[string]bool{}
	elemTypeByName := map[string]Type{}
	for _, stmt := range flattenRegionTopLevel(region.Body) {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok || !a.isFreshRegionAllocation(vd) || seen[vd.Name] {
			continue
		}
		seen[vd.Name] = true
		order = append(order, vd.Name)
		if dt, ok := StripAggregateStateType(a.exprTypes[vd.Value]).(*DArrayType); ok {
			elemTypeByName[vd.Name] = dt.Elem
		}
	}
	if len(order) == 0 {
		return asn
	}

	// Reserved (fixed-footprint) growables: reuse the growth scan's reserve detection.
	tracked := map[string]int{}
	for _, n := range order {
		tracked[n] = 0
	}
	scan := &regionGrowthScan{tracked: tracked, reserved: map[string]bool{}}
	scan.walkGrowth(reflect.ValueOf(region.Body))
	// Phase C: darrays that have an interior reference taken into them (`&x[i]`). A reserved
	// (bounded) such darray gets its own reserve_commit stack so the base never moves and the
	// reference stays valid across growth.
	interiorRef := collectInteriorRefNames(region.Body, seen)

	next := 1
	mergeStack := -1
	for _, n := range order {
		// Phase C (docs/72): a darray with an interior reference taken into it gets a
		// reserve_commit stack (stable base) ONLY when its maximum footprint is PROVABLE — its
		// sole element growth is a single counting-loop push, bound N. A bare `reserve(n)` is a
		// hint, not a hard limit, so it is NOT sufficient; reserve_commit on an unbounded darray
		// would panic on overflow (worse than the current compile error).
		if interiorRef[n] {
			if bound, ok := hardBoundForName(region.Body, n); ok && boundInScopeAtRegionEntry(bound, paramNames) {
				if elem, ok := elemTypeByName[n]; ok {
					asn.StackOf[n] = next
					asn.StackKind[next] = "growable"
					asn.StackStrategy[next] = "reserve_commit"
					asn.StackCapacity[next] = bound
					asn.StackElemType[next] = elem
					next++
					continue
				}
			}
		}
		if scan.reserved[n] {
			asn.StackOf[n] = 0 // shared fixed/reserved stack
			continue
		}
		if next > regionStackCap {
			if mergeStack < 0 {
				mergeStack = next
				asn.StackKind[mergeStack] = "merge"
			}
			asn.StackOf[n] = mergeStack
			asn.Merged = true
			continue
		}
		asn.StackOf[n] = next
		asn.StackKind[next] = "growable"
		// docs/73: the DEFAULT backing for an unreserved own-growable tail is reserve_commit — a
		// contiguous bump stack that grows in place (no relocate-and-leave-a-hole), with a default
		// reservation (StackCapacity left nil). This makes fragmentation structurally impossible
		// for the common case and the base stable (interior refs survive). Excluded: regions reset
		// per loop iteration (chained there, so flipping would mislead the storage-view checker)
		// and the merge stack (multiple growables can't share one in-place tail — stays chained).
		if !inLoop {
			asn.StackStrategy[next] = "reserve_commit"
		}
		next++
	}
	// Phase B2 (docs/71): an own growable stack whose object provably dies before region exit and
	// is never aliased can have its arena freed early — right after its last use — rather than at
	// region exit. The escape check is the conservative D-merge: if the object is aliased (address
	// taken, copied, or passed out), it stays to region exit.
	topLevel := flattenRegionTopLevel(region.Body)
	for n, stack := range asn.StackOf {
		if stack <= 0 || asn.StackKind[stack] == "merge" {
			continue // shared/merge stacks hold more than one object; cannot free individually
		}
		if off, ok := earlyFreeableObject(topLevel, n); ok {
			asn.StackEarlyFreeAfter[stack] = off
		}
	}

	maxStack := 0
	for _, s := range asn.StackOf {
		if s > maxStack {
			maxStack = s
		}
	}
	asn.StackCount = maxStack + 1
	return asn
}

// earlyFreeableObject returns the byte offset of the last top-level statement referencing `name`,
// when that object can be freed there: it is referenced ONLY as a method/field receiver or an index
// base (never aliased — no address taken, no bare-value use that could copy its buffer pointer), and
// it dies strictly before the region's final statement. ok=false means keep it to region exit.
func earlyFreeableObject(topLevel []ast.Stmt, name string) (int, bool) {
	if objectMayEscape(topLevel, name) {
		return 0, false
	}
	lastRef := -1
	for i, stmt := range topLevel {
		if subtreeReferencesIdent(stmt, name) {
			lastRef = i
		}
	}
	if lastRef < 0 || lastRef >= len(topLevel)-1 {
		return 0, false // unused, or still live in the final statement (dies at region exit)
	}
	return topLevel[lastRef].Pos().Offset, true
}

// objectMayEscape reports whether any reference to `name` could outlive an early free: an address-of
// (`&name` / `&name[i]`), or any occurrence of the identifier that is NOT a method/field receiver
// (`name.x`) or an index base (`name[i]`) — e.g. a bare value use, an assignment source, or a call
// argument, all of which can copy the buffer pointer out.
func objectMayEscape(body []ast.Stmt, name string) bool {
	okBase := map[*ast.Ident]bool{}
	allRefs := []*ast.Ident{}
	addrTaken := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if addrTaken || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch e := v.Interface().(type) {
			case *ast.Ident:
				if e.Name == name {
					allRefs = append(allRefs, e)
				}
			case *ast.FieldExpr:
				if id, ok := stripParenExpr(e.Object).(*ast.Ident); ok && id.Name == name {
					okBase[id] = true
				}
			case *ast.IndexExpr:
				if id, ok := stripParenExpr(e.Object).(*ast.Ident); ok && id.Name == name {
					okBase[id] = true
				}
			case *ast.AddrOfExpr:
				if identReducesTo(e.Operand, name) {
					addrTaken = true
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(body))
	if addrTaken {
		return true
	}
	for _, id := range allRefs {
		if !okBase[id] {
			return true // a bare / aliasing use
		}
	}
	return false
}

// identReducesTo reports whether expr is `name` or `name[...]` (the targets of an address-of that
// would alias into the object's buffer).
func identReducesTo(expr ast.Expr, name string) bool {
	switch e := stripParenExpr(expr).(type) {
	case *ast.Ident:
		return e.Name == name
	case *ast.IndexExpr:
		id, ok := stripParenExpr(e.Object).(*ast.Ident)
		return ok && id.Name == name
	}
	return false
}

// subtreeReferencesIdent reports whether any *ast.Ident named `name` appears anywhere in node.
func subtreeReferencesIdent(node ast.Node, name string) bool {
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok && id.Name == name {
				found = true
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(node))
	return found
}

// collectInteriorRefNames returns the tracked container names that have an interior reference
// taken into them anywhere in body — i.e. `&name[...]` (an address-of an index expression). These
// are the darrays whose backing must not relocate for the reference to stay valid (Phase C).
func collectInteriorRefNames(body []ast.Stmt, tracked map[string]bool) map[string]bool {
	names := map[string]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if addr, ok := v.Interface().(*ast.AddrOfExpr); ok {
				if idx, ok := stripParenExpr(addr.Operand).(*ast.IndexExpr); ok {
					if id, ok := stripParenExpr(idx.Object).(*ast.Ident); ok && tracked[id.Name] {
						names[id.Name] = true
					}
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(body))
	return names
}

// hardBoundForName returns the provable maximum element count N for a fresh darray `name` when its
// ONLY element growth is a single `name.push(...)` located inside exactly one counting loop
// `for _ in 0..<N` (N a pure ident/int) — with no other push/extend/append/insert anywhere, and not
// nested in a deeper loop. Such a darray can never hold more than N elements, so a reserve_commit
// reservation sized to N can never overflow. Returns the N expression and ok; otherwise ok=false.
func hardBoundForName(body []ast.Stmt, name string) (ast.Expr, bool) {
	s := &hardBoundScan{name: name}
	s.walk(reflect.ValueOf(body))
	if s.disqualified || s.pushSites != 1 || s.bound == nil {
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
		if len(s.loopStack) != 1 { // outside a loop, or nested in a deeper loop -> not provably <= N
			s.disqualified = true
			return
		}
		b, ok := countingLoopBound(s.loopStack[0])
		if !ok {
			s.disqualified = true
			return
		}
		s.bound = b
	case "push_back", "push_front", "extend", "append", "append_many", "insert":
		s.disqualified = true // adds an unbounded / non-counting amount
	}
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
// entry (where the reserve_commit arena is created): an integer literal always, or an identifier
// that is a function parameter. A local declared inside the region body is not yet in scope there,
// so it does not qualify for reserve_commit (the darray stays chained — a sound, graceful fallback;
// deferring the reservation to the darray's first use to support local bounds is a follow-up).
func boundInScopeAtRegionEntry(bound ast.Expr, paramNames map[string]bool) bool {
	switch b := bound.(type) {
	case *ast.IntLit:
		return true
	case *ast.Ident:
		return paramNames[b.Name]
	}
	return false
}

// countingLoopBound returns the exclusive end N of a `for _ in 0..<N` loop with a pure ident/int
// bound (the same shape auto-reservation uses), or ok=false for any other loop.
func countingLoopBound(node ast.Node) (ast.Expr, bool) {
	loop, ok := node.(*ast.ForStmt)
	if !ok || loop.Reverse || loop.Op != lexer.TOKEN_RANGE_LT || loop.Step != nil {
		return nil, false
	}
	if start, ok := loop.Start.(*ast.IntLit); !ok || start.Value != "0" {
		return nil, false
	}
	switch loop.End.(type) {
	case *ast.Ident, *ast.IntLit:
		return loop.End, true
	}
	return nil, false
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
