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
// These are the "strange lifetimes" that forced MLKit toward a GC. Coarsening them into one
// enclosing region is always memory-SAFE (it just retains longer), so this is reported through
// the graduated `-Wperf` lever (warn by default, hard error under -Wperf) rather than a hard
// ban — see docs/70 and [[performance-friction-design]].
//
// Phase 1 changes nothing about what compiles (warnings only) and no codegen. It also yields
// the frequency data that decides whether interleaving should become a default error (Phase 3).
//
// Explicit regions (`region r(...)`, `def f[region r]`, an `@r` type annotation) are NOT
// inferred and are exempt: the user keeps full manual control.

// checkRegionLifetimes analyzes every inferred (synthesized `in auto:`) region in fn.
func (a *Analyzer) checkRegionLifetimes(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	for _, region := range collectSynthesizedRegions(fn.Body) {
		a.analyzeRegionLifetimes(region)
	}
}

type regionLifetimeLocal struct {
	name  string
	birth int // declaration offset
	death int // last-use offset (loop-clamped); == birth if never used after decl
}

// analyzeRegionLifetimes flags interleaved object lifetimes within one inferred region.
func (a *Analyzer) analyzeRegionLifetimes(region *ast.RegionStmt) {
	// 1. Top-level fresh allocations in this region (not nested in loops/ifs/sub-regions —
	// those belong to sub-scopes or are churn, out of scope for interleaving).
	locals := make([]regionLifetimeLocal, 0, 4)
	byName := map[string]int{}
	for _, stmt := range region.Body {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok || !a.isFreshRegionAllocation(vd) {
			continue
		}
		if _, dup := byName[vd.Name]; dup {
			continue // top-level shadowing — skip, can't disambiguate by name
		}
		byName[vd.Name] = len(locals)
		locals = append(locals, regionLifetimeLocal{name: vd.Name, birth: vd.Position.Offset, death: vd.Position.Offset})
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
				flagged[j] = true
				a.perfLint(declPosFor(region, lj.name),
					"interleaved object lifetimes in an inferred region: %q is still live when %q is created but is last used before %q dies — their lifetimes cross, so they cannot be placed in lifetime-tightened nested regions. Give one an explicit region (`in r:` / `region r(...)`), or accept that both are retained until scope exit.",
					li.name, lj.name, lj.name)
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
	for _, stmt := range region.Body {
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
