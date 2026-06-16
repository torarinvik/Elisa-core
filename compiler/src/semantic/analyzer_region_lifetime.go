package semantic

import (
	"reflect"
	"sort"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Object-lifetime analysis for inferred regions (Phase 1 — diagnostics only).
//
// Elisa infers one region per lexical scope (`in auto:` / inferred function body). A region
// stack can only free in LIFO order, so it can tighten any set of object lifetimes that NEST
// or are DISJOINT. The patterns it cannot tighten are INTERLEAVED lifetimes: object A is born,
// then B is born, then A dies, then B dies — A and B overlap but neither contains the other.
// These are the "strange lifetimes" that forced MLKit toward a GC. Rather than fall back to a
// GC for them, Elisa REJECTS them (a hard error): they are rare (0 in a 1139-file sweep) and
// usually a code smell, and Elisa's explicit allocation keeps the banned surface small. The fix
// is local — put one object in an explicit region so the lifetimes nest, or reorder so one
// contains the other.
//
// Explicit regions (`region r(...)`, `def f[@r]`, an `@r` type annotation) are NOT
// inferred and are exempt: the user keeps full manual control. With interleaving banned, every
// inferred region's surviving objects nest or are disjoint — a clean forest the auto-tightening
// transform (Phase 2) relies on.

// flattenRegionTopLevel flattens a region body through transparent effect-grant blocks (`can ...:`)
// so allocations declared inside them count as the region's top level — most function bodies wrap
// their allocations in a `can` block. It does NOT descend into loops/ifs/sub-regions, which are
// genuine sub-scopes rather than the region's own top level.
func flattenRegionTopLevel(body []ast.Stmt) []ast.Stmt {
	out := make([]ast.Stmt, 0, len(body))
	for _, stmt := range body {
		if can, ok := stmt.(*ast.CanStmt); ok {
			out = append(out, flattenRegionTopLevel(can.Body)...)
			continue
		}
		out = append(out, stmt)
	}
	return out
}

// checkRegionLifetimes analyzes every inferred (synthesized `in auto:`) region in fn.
func (a *Analyzer) checkRegionLifetimes(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	// Names in scope at every region entry: function parameters. A reserve_commit reservation
	// (sized from the proven bound) is emitted at region entry, so its bound must resolve there.
	paramNames := map[string]bool{}
	for _, p := range fn.Params {
		paramNames[p.Name] = true
	}
	reserveCommitDeclOffsets := map[int]bool{}
	for _, region := range collectSynthesizedRegions(fn.Body) {
		asn := a.assignRegionStacks(region, paramNames)
		if a.regionStacks != nil {
			a.regionStacks[region] = asn
		}
		// Record the decl offsets of darrays given a reserve_commit stack: a deferred interior-ref
		// invalidation whose source is one of these is stable (the base never moves) and is dropped.
		for _, stmt := range flattenRegionTopLevel(region.Body) {
			vd, ok := stmt.(*ast.VarDeclStmt)
			if !ok {
				continue
			}
			if stack, ok := asn.StackOf[vd.Name]; ok && asn.stackStrategy(stack) == "reserve_commit" {
				reserveCommitDeclOffsets[vd.Position.Offset] = true
			}
		}
		// Both checks reflect the actual arena routing: crossing-lifetime / interleaved-growth
		// objects in separate stacks (each its own tail) are auto-resolved, so only an
		// in-the-same-stack collision (the over-split merge stack) is a real conflict.
		a.analyzeRegionLifetimes(region, asn)
		a.analyzeRegionGrowthDiscipline(region, asn)
		if dumpRegionStacks {
			a.dumpRegionStackAssignment(region, asn)
		}
	}
	a.resolvePendingStorageViewErrors(reserveCommitDeclOffsets)
}

// resolvePendingStorageViewErrors emits the deferred invalidated-view-use errors, dropping any whose
// source darray was given a reserve_commit stack (its base never moves, so the reference stays
// valid). Suppress iff the backing is provably stable — the soundness invariant (docs/72).
func (a *Analyzer) resolvePendingStorageViewErrors(reserveCommitDeclOffsets map[int]bool) {
	for _, p := range a.pendingStorageViewErrors {
		if p.hasSourceDecl && reserveCommitDeclOffsets[p.sourceDeclOffset] {
			continue // reserve_commit-backed source -> stable -> drop the error
		}
		if a.storageViewStaleUses != nil {
			a.storageViewStaleUses[p.expr] = p.dep
		}
		a.errorf(p.expr.Pos(), storageViewInvalidatedMessage(p.viewName, p.dep.InvalidatedBy))
	}
	a.pendingStorageViewErrors = nil
}

// regionGrowthMethods are the darray/dict operations that may REALLOCATE the backing buffer.
// In a shared bump arena a buffer can only grow in place while it is the tail (top) allocation;
// growing an interior buffer relocates it (leaving a dead hole under the CHAINED strategy) or
// panics (under the interior-pointer-stable RESERVE_COMMIT/FIXED strategies — arena.elisa:460).
var regionGrowthMethods = map[string]bool{
	"push": true, "push_back": true, "push_front": true,
	"insert": true, "append": true, "extend": true, "resize": true,
}

// analyzeRegionGrowthDiscipline (Phase 1 — diagnostics) enforces tail-growth discipline: once a
// later sibling is allocated on top of an inferred-region container, growing the earlier one
// forces a reallocation. Because an arena never frees mid-region, a container is sealed for the
// rest of the region as soon as ANY later sibling is born. `reserve()` pre-sizes a container so
// it never relocates, and suppresses the warning (the canonical size-then-freeze idiom).
func (a *Analyzer) analyzeRegionGrowthDiscipline(region *ast.RegionStmt, asn RegionStackAssignment) {
	// Top-level fresh allocations in source (== arena) order.
	birthByName := map[string]int{}
	order := make([]string, 0, 4)
	for _, stmt := range flattenRegionTopLevel(region.Body) {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok || !a.isFreshRegionAllocation(vd) {
			continue
		}
		if _, dup := birthByName[vd.Name]; dup {
			continue
		}
		birthByName[vd.Name] = vd.Position.Offset
		order = append(order, vd.Name)
	}
	if len(order) < 2 {
		return // need a later sibling to seal an earlier container
	}

	scan := &regionGrowthScan{tracked: birthByName, reserved: map[string]bool{}}
	scan.walkGrowth(reflect.ValueOf(region.Body))

	sort.SliceStable(scan.growths, func(x, y int) bool { return scan.growths[x].off < scan.growths[y].off })
	warned := map[string]bool{}
	for _, g := range scan.growths {
		if scan.reserved[g.name] || warned[g.name] {
			continue // user pre-sized it, or already reported once per container
		}
		xb := birthByName[g.name]
		blocker := ""
		blockerBirth := 0
		for _, other := range order {
			ob := birthByName[other]
			// Only a sibling SHARING g's stack can seal it: with multi-stack regions each
			// unreserved growable gets its own arena (its own tail), so a sibling in a
			// different stack never forces a reallocation. Same-stack collisions (e.g. the
			// over-split merge stack) are the real interior growths.
			if asn.StackOf[other] != asn.StackOf[g.name] {
				continue
			}
			if ob > xb && ob < g.off { // a sibling born after x, before this push -> x is sealed
				if blocker == "" || ob < blockerBirth {
					blocker, blockerBirth = other, ob
				}
			}
		}
		if blocker != "" {
			warned[g.name] = true
			a.warnf(g.pos,
				"growing %q after %q was allocated on top of it in the same inferred region forces a reallocation: %q is no longer the arena tail, so this relocates its buffer (a dead hole under the default region, or a panic under an interior-pointer-stable one). reserve() %q's capacity up front, finish growing it before %q is created, or give one its own region.",
				g.name, blocker, g.name, g.name, blocker)
		}
	}
}

type regionGrowth struct {
	name string
	off  int
	pos  lexer.Pos
}

// regionGrowthScan collects growth-method calls and reserve() suppressors on tracked containers.
type regionGrowthScan struct {
	tracked  map[string]int
	growths  []regionGrowth
	reserved map[string]bool
}

// growthCallTarget reports the receiver name of a `recv.method(...)` call when recv is a tracked
// container and method is a known growth/reserve op, plus whether it is a reserve (suppressor).
func (s *regionGrowthScan) growthCallTarget(call *ast.CallExpr) (name string, isReserve, isGrowth bool) {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		return "", false, false
	}
	recv, ok := field.Object.(*ast.Ident)
	if !ok {
		return "", false, false
	}
	if _, tracked := s.tracked[recv.Name]; !tracked {
		return "", false, false
	}
	switch {
	case field.Field == "reserve":
		return recv.Name, true, false
	case regionGrowthMethods[field.Field]:
		return recv.Name, false, true
	}
	return "", false, false
}

func (s *regionGrowthScan) recordSynthesizedReserve(stmt ast.Stmt) {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || call == nil {
		return
	}
	if name, isReserve, _ := s.growthCallTarget(call); name != "" && isReserve {
		s.reserved[name] = true
	}
}

func (s *regionGrowthScan) recordSynthesizedReserves(loop ast.Node) {
	for _, stmt := range ast.SynthesizedPreReserveStmts(loop) {
		s.recordSynthesizedReserve(stmt)
	}
}

func (s *regionGrowthScan) walkGrowth(v reflect.Value) {
	if !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		if loop, ok := v.Interface().(*ast.ForStmt); ok {
			s.recordSynthesizedReserves(loop)
			s.walkGrowth(reflect.ValueOf(loop.Body))
			return
		}
		if loop, ok := v.Interface().(*ast.IterForStmt); ok {
			s.recordSynthesizedReserves(loop)
			s.walkGrowth(reflect.ValueOf(loop.Body))
			return
		}
		if call, ok := v.Interface().(*ast.CallExpr); ok {
			if name, isReserve, isGrowth := s.growthCallTarget(call); name != "" {
				if isReserve {
					s.reserved[name] = true
				} else if isGrowth {
					s.growths = append(s.growths, regionGrowth{name: name, off: call.Pos().Offset, pos: call.Pos()})
				}
			}
		}
		s.walkGrowth(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			s.walkGrowth(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			s.walkGrowth(v.Index(i))
		}
	}
}

type regionLifetimeLocal struct {
	name  string
	birth int // declaration offset
	death int // last-use offset (loop-clamped); == birth if never used after decl
	// firstUse is the earliest use offset (raw, not loop-clamped); -1 if never used. With
	// firstUseInLoop it decides whether a concrete disjoint-reorder remedy is provably legal:
	// declaring this object just below an earlier object's last use is safe only when this
	// object's first use is straight-line (not inside a loop) and already past that point.
	firstUse       int
	firstUseInLoop bool
}

// analyzeRegionLifetimes flags interleaved object lifetimes within one inferred region.
func (a *Analyzer) analyzeRegionLifetimes(region *ast.RegionStmt, asn RegionStackAssignment) {
	// 1. Top-level fresh allocations in this region (not nested in loops/ifs/sub-regions —
	// those belong to sub-scopes or are churn, out of scope for interleaving).
	locals := make([]regionLifetimeLocal, 0, 4)
	byName := map[string]int{}
	for _, stmt := range flattenRegionTopLevel(region.Body) {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok || !a.isFreshRegionAllocation(vd) {
			continue
		}
		if _, dup := byName[vd.Name]; dup {
			continue // top-level shadowing — skip, can't disambiguate by name
		}
		byName[vd.Name] = len(locals)
		locals = append(locals, regionLifetimeLocal{name: vd.Name, birth: vd.Position.Offset, death: vd.Position.Offset, firstUse: -1})
	}
	if len(locals) < 2 {
		return // need at least two objects to interleave
	}

	// 2. Last-use of each local, scanning the whole region body (uses inside any loop extend
	// the lifetime to that loop's lexical end).
	scan := &regionLifetimeScan{localByName: byName}
	scan.walkField(reflect.ValueOf(region.Body))
	for _, u := range scan.uses {
		// Death is at STATEMENT granularity: two objects last used in the SAME statement
		// (e.g. `return f(a, b)`) die together and must not be read as crossing. A use inside
		// a loop extends to the loop's lexical end (live across all iterations).
		death := u.stmtOff
		if u.loop != nil && u.loop.maxOffset > death {
			death = u.loop.maxOffset
		}
		if death > locals[u.localIdx].death {
			locals[u.localIdx].death = death
		}
		if loc := &locals[u.localIdx]; loc.firstUse < 0 || u.stmtOff < loc.firstUse {
			loc.firstUse = u.stmtOff
			loc.firstUseInLoop = u.loop != nil
		}
	}

	// 3. Interleaving: order by birth; pair (i,j) crosses iff j is born while i is still
	// live and i dies strictly before j.
	order := make([]int, len(locals))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool { return locals[order[x]].birth < locals[order[y]].birth })
	flagged := map[int]bool{}
	for x := 0; x < len(order); x++ {
		i := order[x]
		for y := x + 1; y < len(order); y++ {
			j := order[y]
			li, lj := locals[i], locals[j]
			if lj.birth < li.death && li.death < lj.death {
				if flagged[j] {
					continue
				}
				// Multi-stack regions auto-resolve crossing lifetimes: if the two objects are
				// assigned to different parallel stacks (each its own arena), there is no LIFO
				// conflict to reject. The error survives only when they are forced into the SAME
				// stack — i.e. the over-split merge stack — where the region genuinely cannot
				// separate them.
				if asn.StackOf[li.name] != asn.StackOf[lj.name] {
					continue
				}
				flagged[j] = true
				// Name the specific remedy. The one legal reorder lever is moving lj's BIRTH:
				// declaring lj below li's last use makes the two ranges disjoint. That is provably
				// safe only when lj is not touched until after li dies AND its first use is
				// straight-line (we have no loop-start offset, so an in-loop first use can't be
				// shown to sit past li's death). Otherwise the ranges genuinely overlap and only
				// a separate region or a reservation can split them.
				if !lj.firstUseInLoop && lj.firstUse > li.death {
					a.errorf(declPosFor(region, lj.name),
						"interleaved object lifetimes in an inferred region: %q is created while %q is still live, but %q is last used before %q is first used — their lifetimes cross and the region's stack budget (%d) is exhausted, so they cannot share a bump-stack. Move %q's declaration below %q's last use (their live ranges are disjoint, so this makes the lifetimes nest), or put one in an explicit region (`in r:` / `region r(...)`), or reserve() one of them.",
						lj.name, li.name, li.name, lj.name, regionStackCap, lj.name, li.name)
				} else {
					a.errorf(declPosFor(region, lj.name),
						"interleaved object lifetimes in an inferred region: %q is created while %q is still live and their live ranges overlap, and the region's stack budget (%d) is exhausted, so they cannot share a bump-stack. Put one in an explicit region (`in r:` / `region r(...)`) so the lifetimes nest, or reserve() one of them (a reserved container is fixed-footprint and shares the base stack).",
						lj.name, li.name, regionStackCap)
				}
			}
		}
	}
}

// isFreshRegionAllocation reports whether vd declares a fresh region container (a darray/dict
// allocating literal) with NO explicit region annotation — i.e. one that inference placed in
// the enclosing synthesized region.
func (a *Analyzer) isFreshRegionAllocation(vd *ast.VarDeclStmt) bool {
	if vd == nil || vd.Value == nil {
		return false
	}
	switch vd.Value.(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr:
	default:
		return false
	}
	if typeExprHasExplicitRegion(vd.Type) {
		return false // user-specified region — exempt from inference analysis
	}
	t := a.exprTypes[vd.Value]
	if t == nil {
		return false
	}
	switch StripAggregateStateType(t).(type) {
	case *DArrayType, *DictType:
		return true
	}
	return false
}

// typeExprHasExplicitRegion reports whether a type annotation carries an `@r` region.
func typeExprHasExplicitRegion(te ast.TypeExpr) bool {
	switch t := te.(type) {
	case *ast.MutableType:
		return typeExprHasExplicitRegion(t.Elem)
	case *ast.RefType:
		return t.Region != "" || typeExprHasExplicitRegion(t.Elem)
	case *ast.BuiltinTypeExpr:
		return t.Region != ""
	case *ast.GenericType:
		return t.Region != ""
	}
	return false
}

// declPosFor returns the declaration position of a top-level local in region (falls back to
// the region position).
func declPosFor(region *ast.RegionStmt, name string) lexer.Pos {
	for _, stmt := range flattenRegionTopLevel(region.Body) {
		if vd, ok := stmt.(*ast.VarDeclStmt); ok && vd.Name == name {
			return vd.Position
		}
	}
	return region.Position
}

// --- the loop-aware reflection scan -------------------------------------------------------

type regionLoopFrame struct{ maxOffset int }

type regionUse struct {
	localIdx int
	stmtOff  int              // offset of the innermost enclosing statement
	loop     *regionLoopFrame // outermost enclosing loop, or nil
}

type regionLifetimeScan struct {
	localByName map[string]int
	loopStack   []*regionLoopFrame
	stmtStack   []int // offsets of enclosing statements (innermost = top)
	uses        []regionUse
}

func (s *regionLifetimeScan) visitNode(n ast.Node) {
	if n == nil {
		return
	}
	off := n.Pos().Offset
	for _, fr := range s.loopStack {
		if off > fr.maxOffset {
			fr.maxOffset = off
		}
	}
	if id, ok := n.(*ast.Ident); ok {
		if idx, tracked := s.localByName[id.Name]; tracked {
			var outer *regionLoopFrame
			if len(s.loopStack) > 0 {
				outer = s.loopStack[0]
			}
			stmtOff := off
			if len(s.stmtStack) > 0 {
				stmtOff = s.stmtStack[len(s.stmtStack)-1]
			}
			s.uses = append(s.uses, regionUse{localIdx: idx, stmtOff: stmtOff, loop: outer})
		}
	}
	pushedLoop := false
	if isLoopStmtNode(n) {
		fr := &regionLoopFrame{maxOffset: off}
		s.loopStack = append(s.loopStack, fr)
		pushedLoop = true
	}
	pushedStmt := false
	if _, ok := n.(ast.Stmt); ok {
		s.stmtStack = append(s.stmtStack, off)
		pushedStmt = true
	}
	rv := reflect.ValueOf(n)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
		if rv.Kind() == reflect.Struct {
			for i := 0; i < rv.NumField(); i++ {
				s.walkField(rv.Field(i))
			}
		}
	}
	if pushedStmt {
		s.stmtStack = s.stmtStack[:len(s.stmtStack)-1]
	}
	if pushedLoop {
		s.loopStack = s.loopStack[:len(s.loopStack)-1]
	}
}

func (s *regionLifetimeScan) walkField(v reflect.Value) {
	if !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if node, ok := v.Interface().(ast.Node); ok {
			s.visitNode(node)
			return
		}
		s.walkField(v.Elem())
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		if node, ok := v.Interface().(ast.Node); ok {
			s.visitNode(node)
			return
		}
		s.walkField(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			s.walkField(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			s.walkField(v.Index(i))
		}
	}
}

func isLoopStmtNode(n ast.Node) bool {
	switch n.(type) {
	case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt, *ast.ParallelForStmt:
		return true
	}
	return false
}

// collectSynthesizedRegions returns every compiler-synthesized (`in auto:`) region anywhere in
// the given statements, including nested ones.
func collectSynthesizedRegions(root []ast.Stmt) []*ast.RegionStmt {
	var out []*ast.RegionStmt
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if rs, ok := v.Interface().(*ast.RegionStmt); ok && rs.Lazy && isSynthesizedAutoRegion(rs.Name) {
				out = append(out, rs)
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(root))
	return out
}
