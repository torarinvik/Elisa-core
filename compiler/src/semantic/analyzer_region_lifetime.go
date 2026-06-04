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
// Explicit regions (`region r(...)`, `def f[region r]`, an `@r` type annotation) are NOT
// inferred and are exempt: the user keeps full manual control. With interleaving banned, every
// inferred region's surviving objects nest or are disjoint — a clean forest the auto-tightening
// transform (Phase 2) relies on.

// checkRegionLifetimes analyzes every inferred (synthesized `in auto:`) region in fn.
func (a *Analyzer) checkRegionLifetimes(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	for _, region := range collectSynthesizedRegions(fn.Body) {
		a.analyzeRegionLifetimes(region)
		a.hintLoopRetention(region)
	}
}

// hintLoopRetention nudges toward the canonical retention win: a fresh allocation declared in a
// loop body that lives ONLY within the iteration but accumulates in the enclosing inferred region
// for the whole loop (unbounded retention). Wrapping the loop body in `in auto:` frees it per
// iteration. This is a graduated hint (perfLint: warn by default), and the suggested fix is
// validated by the sound escape checker when applied — so it is safe regardless of hint precision.
func (a *Analyzer) hintLoopRetention(region *ast.RegionStmt) {
	a.walkForLoops(reflect.ValueOf(region.Body))
}

func (a *Analyzer) walkForLoops(v reflect.Value) {
	if !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		switch n := v.Interface().(type) {
		case *ast.RegionStmt, *ast.InStoreStmt:
			return // sub-region owns its own allocations; analyzed separately
		case *ast.WhileStmt:
			a.hintLoopBody(n.Body)
		case *ast.ForStmt:
			a.hintLoopBody(n.Body)
		case *ast.IterForStmt:
			a.hintLoopBody(n.Body)
		}
		a.walkForLoops(v.Elem())
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		a.walkForLoops(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			a.walkForLoops(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			a.walkForLoops(v.Index(i))
		}
	}
}

func (a *Analyzer) hintLoopBody(body []ast.Stmt) {
	// If the body grows a container declared OUTSIDE it (the accumulator pattern), wrapping the
	// body in `in auto:` would itself be a growth-escape error — so the suggested fix wouldn't
	// apply. Suppress the hint in that case.
	if loopBodyGrowsOuterContainer(body) {
		return
	}
	for _, s := range body {
		vd, ok := s.(*ast.VarDeclStmt)
		if !ok || !a.isFreshRegionAllocation(vd) {
			continue
		}
		if allocationIsIterationLocal(vd.Name, body) {
			a.perfLint(vd.Position,
				"%q is allocated every iteration and used only within the iteration, but it accumulates in the enclosing inferred region for the whole loop. Wrap the loop body in `in auto:` (or an explicit `region`) to free it each iteration and bound peak memory.",
				vd.Name)
		}
	}
}

// loopBodyGrowsOuterContainer reports whether the loop body grows (push/extend/reserve/cstr) a
// container that is NOT declared somewhere within the body — i.e. an outer-region container.
// Wrapping such a body in `in auto:` would make that growth a use-after-free escape error, so the
// retention hint must not suggest it.
func loopBodyGrowsOuterContainer(body []ast.Stmt) bool {
	declared := map[string]bool{}
	var collectDecls func(v reflect.Value)
	collectDecls = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if vd, ok := v.Interface().(*ast.VarDeclStmt); ok {
				declared[vd.Name] = true
			}
			collectDecls(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collectDecls(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				collectDecls(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				collectDecls(v.Index(i))
			}
		}
	}
	collectDecls(reflect.ValueOf(body))

	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if fe, ok := call.Func.(*ast.FieldExpr); ok && isContainerGrowthMethod(fe.Field) {
					if id, ok := fe.Object.(*ast.Ident); ok && !declared[id.Name] {
						found = true
						return
					}
				}
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
	rec(reflect.ValueOf(body))
	return found
}

func isContainerGrowthMethod(name string) bool {
	switch name {
	case "push", "extend", "reserve", "cstr":
		return true
	}
	return false
}

// allocationIsIterationLocal is a SOUND-conservative check (the reusable liveness foundation for
// the Phase 2 auto-transform): a local does not escape its iteration if every occurrence of its
// name is a self-op — the object of a method/field access (`x.push`, `x.count`), the object of a
// single index (`x[i]`, returns a copy), or a `for v in x` source. Any other occurrence (a bare
// arg `f(x)`, a borrow `&x`, a slice/view `x[a:b]`, an alias `y = x`, a return, a capture) is
// treated as a potential escape, so the local is NOT considered iteration-local.
func allocationIsIterationLocal(name string, body []ast.Stmt) bool {
	safe := map[*ast.Ident]bool{}
	var all []*ast.Ident
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
			switch n := v.Interface().(type) {
			case *ast.FieldExpr:
				if id, ok := n.Object.(*ast.Ident); ok && id.Name == name {
					safe[id] = true
				}
			case *ast.IndexExpr:
				if id, ok := n.Object.(*ast.Ident); ok && id.Name == name {
					safe[id] = true
				}
			case *ast.IterForStmt:
				if id, ok := n.Source.(*ast.Ident); ok && id.Name == name {
					safe[id] = true
				}
			case *ast.Ident:
				if n.Name == name {
					all = append(all, n)
				}
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
	rec(reflect.ValueOf(body))
	if len(all) == 0 {
		return false
	}
	for _, id := range all {
		if !safe[id] {
			return false
		}
	}
	return true
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
				a.errorf(declPosFor(region, lj.name),
					"interleaved object lifetimes in an inferred region: %q is still live when %q is created but is last used before %q dies — their lifetimes cross, so they cannot be placed on a region stack (a region frees in LIFO order). Put one in an explicit region (`in r:` / `region r(...)`) so the lifetimes nest, or reorder so one fully contains the other.",
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
