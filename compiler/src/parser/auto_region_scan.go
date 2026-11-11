package parser

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/astscan"
)

// Results belong to one read-only scan; no AST or result survives a parse.
// Scalar bodies do not allocate a memo.
type autoRegionScan struct {
	exprBlocks      map[*ast.ExprBlock]bool
	exprBlockBodies map[*ast.ExprBlock]bool
}

// functionBodyNeedsAutoRegion reports whether a statement list contains an allocation that
// needs an ambient region. The structural walk skips nested region scopes, while the
// conservative whole-body expression prepass may still see allocations inside one; wrapping
// those twice is a harmless lazy no-op.
func functionBodyNeedsAutoRegion(stmts []ast.Stmt) bool {
	var scan autoRegionScan
	return scan.functionBodyNeedsAutoRegion(stmts)
}

func (scan *autoRegionScan) exprBlockBodyNeedsAutoRegion(block *ast.ExprBlock) bool {
	if value, ok := scan.exprBlockBodies[block]; ok {
		return value
	}
	value := scan.functionBodyNeedsAutoRegion(block.Stmts)
	if scan.exprBlockBodies == nil {
		scan.exprBlockBodies = make(map[*ast.ExprBlock]bool)
	}
	scan.exprBlockBodies[block] = value
	return value
}

func (scan *autoRegionScan) functionBodyNeedsAutoRegion(stmts []ast.Stmt) bool {
	var hasExprBlocks bool
	if scan.containsAutoRegionAllocation(stmts, &hasExprBlocks) {
		return true
	}
	// Value blocks (docs/119): a loop EXPRESSION (`for … |acc| -> acc:`), a multi-line
	// if/match expression branch, or a bare block binding carries its statements inside
	// an ExprBlock EXPRESSION, which the statement walk below never reaches — so a
	// container built inside one (`seen: darray[T] = []` in a value-loop body) was left
	// with no ambient region at all ("push has no region to grow into", NoGrowthRegionMessage).
	// Statement-position loop headers desugar to IfStmt/WhileStmt and are walked
	// directly; this covers the value forms. Over-wrap (a block nested inside its own
	// region scope) is a harmless lazy no-op region, same as the whole-body prepass above.
	if hasExprBlocks && scan.exprBlocksNeedAutoRegion(stmts) {
		return true
	}
	return scan.statementBodyNeedsAutoRegion(stmts, true)
}

// The allocation and expression-block prepasses already cover every descendant
// of this statement list. Repeating them at every statement nesting level is
// quadratic for deep nonallocating control flow.
func (scan *autoRegionScan) statementBodyNeedsAutoRegion(stmts []ast.Stmt, checkForwarding bool) bool {
	// A struct-constructed local whose address is forwarded to a call (`x = T(...)`/`x = Builder()`;
	// `grow(&x)`) has its region-less container fields grown INTERPROCEDURALLY by a (region-poly)
	// callee. The growth is invisible to the syntactic allocation checks above, so without an ambient
	// auto region the call site has no region to thread into the callee's `@r` struct-ref param —
	// the "cannot infer region parameter" gap. Synthesizing the region lets recordStructLocalAllocRegion
	// bind the local to it and the call thread it through; the escape checker remains the hard backstop.
	if checkForwarding {
		forwards, hasConstructed := bodyForwardsConstructedStructLocalInfo(stmts)
		if forwards {
			return true
		}
		// If no construction exists anywhere in this subtree, no descendant
		// statement list can have a forwarding candidate either.
		checkForwarding = hasConstructed
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.RegionStmt:
			if len(s.Body) != 0 {
				continue // scoped region owns its body's allocations
			}
		case *ast.InStoreStmt:
			continue // `in <owner>:` establishes an ambient owner
		case *ast.CanStmt:
			if scan.statementBodyNeedsAutoRegion(s.Body, checkForwarding) {
				return true
			}
		case *ast.IfStmt:
			if scan.statementBodyNeedsAutoRegion(s.Then, checkForwarding) || scan.statementBodyNeedsAutoRegion(s.Else, checkForwarding) {
				return true
			}
		case *ast.ForStmt:
			if scan.statementBodyNeedsAutoRegion(s.Body, checkForwarding) {
				return true
			}
		case *ast.IterForStmt:
			// `for x in coll:` (iterator form) — a container literal built inside such a loop that
			// the loop tightener leaves untightened (e.g. it forwards the container to a grower, so
			// it isn't iteration-local) still needs the function-body auto region as its home, just
			// like a ForStmt body. Missing this case left such a container with NO region at all.
			if scan.statementBodyNeedsAutoRegion(s.Body, checkForwarding) {
				return true
			}
		case *ast.WhileStmt:
			if scan.statementBodyNeedsAutoRegion(s.Body, checkForwarding) {
				return true
			}
		case *ast.ScopeStmt:
			if scan.statementBodyNeedsAutoRegion(s.Body, checkForwarding) {
				return true
			}
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				if scan.statementBodyNeedsAutoRegion(arm.Body, checkForwarding) {
					return true
				}
			}
		case *ast.StaticIfStmt:
			// `static if`/`static else` branches are selected after parsing, so a container
			// built in any branch needs the function-body region exactly like a runtime `if`.
			// Over-wrapping a branch that is later discarded is a harmless lazy no-op region.
			if scan.statementBodyNeedsAutoRegion(s.Then, checkForwarding) || scan.statementBodyNeedsAutoRegion(s.Else, checkForwarding) {
				return true
			}
			for _, elif := range s.Elifs {
				if scan.statementBodyNeedsAutoRegion(elif.Body, checkForwarding) {
					return true
				}
			}
		case *ast.StaticBlockStmt:
			if scan.statementBodyNeedsAutoRegion(s.Body, checkForwarding) {
				return true
			}
		case *ast.VarDeclStmt:
			// Only an allocating literal initializer (`= []`, `= [a, b]`, `= {}`, a
			// comprehension) actually allocates and needs a region. A container local
			// assigned from a view/copy/call (`= other`) does not — excluding those avoids
			// false positives on inspection/forwarding code. Some builders (`[x for ...]`,
			// `each`) infer their darray result type from the expression itself, so they
			// should synthesize an auto region even when the local has no type annotation.
			if (s.Type == nil && (isInferredDArrayBuilder(s.Value) || blockLaterUsesUntypedListLiteralAsDArray(stmts, s.Name))) || (isRegionlessContainerType(s.Type) && scan.isAllocatingLiteral(s.Value)) {
				return true
			}
			// A struct constructed with container-literal field arguments (`Bag([], [])`) allocates
			// those fields region-lessly exactly like a bare container local, so it likewise needs an
			// ambient auto region — both so the fields have a home and so a call site can thread that
			// region into a callee's struct-ref region param.
			if scan.structLiteralAllocatesContainerArg(s.Value) {
				return true
			}
		case *ast.ReturnStmt:
			// A directly returned comprehension, `each` query, allocating struct
			// construction, ownerless allocation, or composite tail containing a
			// contextual list literal needs an ambient region. A plain top-level list
			// literal remains region-polymorphic without this scope.
			if scan.exprBlockTailNeedsAutoRegion(s.Value) {
				return true
			}
		}
	}
	return false
}

// bodyForwardsConstructedStructLocal reports whether the body declares a local from a struct
// construction (`x: T = T(...)` or `x = Builder()` — both parse as *ast.StructLitExpr with
// Brace==false, as does the brace form `T{...}`) and later passes `&x` (or the bare `x`) as a call
// argument, including as a UFCS receiver (`x.grow(...)`). That local's region-less container fields
// are grown interprocedurally by the callee, so the function needs an ambient auto region for the call
// to thread into the callee's struct-ref region param. CONSERVATIVE + SAFE: a non-allocating match
// gets a harmless lazy no-op region and the escape checker is the hard backstop against a too-eager wrap.
func unwrapParenExprAST(e ast.Expr) ast.Expr {
	for {
		paren, ok := e.(*ast.ParenExpr)
		if !ok || paren == nil {
			return e
		}
		e = paren.Inner
	}
}

// constructedFieldGrowthMethods are the container methods that may allocate backing storage
// for a struct-local field receiver (see bodyForwardsConstructedStructLocal).
var constructedFieldGrowthMethods = map[string]bool{
	"push": true, "extend": true, "insert": true, "resize": true, "reserve": true,
	"append": true, "put": true, "add": true,
}

// typeExprIsNamedAggregate reports whether a declared local type is a by-value named type
// (modulo `mutable`) — not a reference, view, or builtin container.
func typeExprIsNamedAggregate(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	nt, ok := typ.(*ast.NamedType)
	if !ok || nt == nil {
		return false
	}
	switch nt.Name {
	case "dstr", "sview", "cstr", "astr", "Arena":
		return false
	}
	return true
}

func bodyForwardsConstructedStructLocal(stmts []ast.Stmt) bool {
	found, _ := bodyForwardsConstructedStructLocalInfo(stmts)
	return found
}

func bodyForwardsConstructedStructLocalInfo(stmts []ast.Stmt) (bool, bool) {
	constructed := map[string]bool{}
	callConstructed := map[string]bool{}
	type alias struct{ name, target string }
	var aliases []alias
	astscan.WalkPruned(stmts, func(node any) bool {
		decl, ok := node.(*ast.VarDeclStmt)
		if !ok || decl.Name == "" {
			return true
		}
		switch value := unwrapParenExprAST(decl.Value).(type) {
		case *ast.Ident:
			if value != nil {
				aliases = append(aliases, alias{decl.Name, value.Name})
			}
		case *ast.StructLitExpr:
			constructed[decl.Name] = true
		case *ast.CallExpr:
			if typeExprIsNamedAggregate(decl.Type) {
				// Call-produced aggregates need an explicit address for whole-local forwarding.
				constructed[decl.Name] = true
				callConstructed[decl.Name] = true
			}
		}
		return true
	})
	// A by-value local copy of a constructed struct still carries the same container
	// backing regions. Treat simple identifier/parenthesized copies as aliases for the
	// purpose of deciding whether the function needs an ambient region. Semantic analysis
	// records and validates the actual source symbol's region before threading it, so this
	// syntax-only prepass cannot grant a borrow or bypass reassignment/liveness checks.
	if len(constructed) == 0 {
		return false, false
	}
	for changed := true; changed; {
		changed = false
		for _, binding := range aliases {
			if !constructed[binding.name] && constructed[binding.target] {
				constructed[binding.name] = true
				changed = true
			}
		}
	}
	argForwardsConstructed := func(args []ast.Expr) bool {
		for _, arg := range args {
			inner := unwrapParenExprAST(arg)
			isAddr := false
			if addr, ok := inner.(*ast.AddrOfExpr); ok && addr != nil {
				inner = unwrapParenExprAST(addr.Operand)
				isAddr = true
			}
			if id, ok := inner.(*ast.Ident); ok && id != nil && constructed[id.Name] && (isAddr || !callConstructed[id.Name]) {
				return true
			}
		}
		return false
	}
	// docs/120 §8 field-path lmut forwarding: `report.cache <- grow(report.cache)` passes a
	// struct FIELD of a constructed local by reference, so the callee may grow that field's
	// region-less containers exactly as it would the whole local's. Only an explicit argument
	// counts (not a UFCS receiver, which is how every field method read is spelled).
	fieldArgForwardsConstructed := func(args []ast.Expr) bool {
		for _, arg := range args {
			inner := unwrapParenExprAST(arg)
			if addr, ok := inner.(*ast.AddrOfExpr); ok && addr != nil {
				inner = unwrapParenExprAST(addr.Operand)
			}
			field, ok := inner.(*ast.FieldExpr)
			if !ok || field == nil {
				continue
			}
			for {
				obj := unwrapParenExprAST(field.Object)
				if next, ok := obj.(*ast.FieldExpr); ok && next != nil {
					field = next
					continue
				}
				if id, ok := obj.(*ast.Ident); ok && id != nil && constructed[id.Name] {
					return true
				}
				break
			}
		}
		return false
	}
	found := false
	astscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		switch expr := node.(type) {
		case *ast.CallExpr:
			_, args, isCall := ast.PrepassCallShape(expr)
			found = (isCall && argForwardsConstructed(args)) ||
				fieldArgForwardsConstructed(expr.Args) ||
				callGrowsConstructedField(expr, constructed)
		case *ast.StructLitExpr:
			_, args, isCall := ast.PrepassCallShape(expr)
			found = isCall && argForwardsConstructed(args)
		}
		return !found
	})
	return found, true
}

// Direct growth of a constructed local's field needs the same ambient region
// as growth of a bare container local. The path can include indexed elements.
func callGrowsConstructedField(call *ast.CallExpr, constructed map[string]bool) bool {
	growth, ok := call.Func.(*ast.FieldExpr)
	if !ok || growth == nil || !constructedFieldGrowthMethods[growth.Field] {
		return false
	}
	obj := unwrapParenExprAST(growth.Object)
	isPath := false
	for {
		switch node := obj.(type) {
		case *ast.FieldExpr:
			isPath = true
			obj = unwrapParenExprAST(node.Object)
		case *ast.IndexExpr:
			isPath = true
			obj = unwrapParenExprAST(node.Object)
		case *ast.Ident:
			return node != nil && isPath && constructed[node.Name]
		default:
			return false
		}
	}
}

// isAutoAllocValue reports whether an initializer is a `new[auto] T(...)` allocation — a fresh
// inferred-region heap allocation, the AllocExpr analogue of a container literal. It makes the
// region-synthesis treat new[auto] exactly like a container: its lifetime is detected (function
// scope vs per-iteration) and it is placed in the matching inferred region, no explicit `in auto:`.
func isAutoAllocValue(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	alloc, ok := value.(*ast.AllocExpr)
	return ok && alloc != nil && (alloc.AutoRegion || alloc.Owner == nil)
}

var autoRegionExprBlockType = reflect.TypeFor[*ast.ExprBlock]()

// Remember value blocks during the allocation walk so ordinary bodies do not
// need another whole-tree scan to look for them.
func (scan *autoRegionScan) containsAutoRegionAllocation(stmts []ast.Stmt, hasExprBlocks *bool) bool {
	found := false
	astscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		switch node := node.(type) {
		case *ast.ExprBlock:
			*hasExprBlocks = true
		case *ast.AllocExpr:
			found = node.AutoRegion || node.Owner == nil
		case *ast.StructLitExpr:
			found = scan.structLiteralAllocatesContainerArg(node)
		}
		return !found
	})
	return found
}

// exprBlocksNeedAutoRegion reports whether any value-position ExprBlock nested anywhere
// in stmts needs an ambient auto region. ExprBlocks live in EXPRESSION position (a
// decl/assign RHS, an ExprStmt, a match-expression arm), so the reflect walk mirrors
// the allocation prepass: find every block wherever it hides, scan its statement list,
// then check its tail expression and any nested blocks.
func exprBlocksNeedAutoRegion(stmts []ast.Stmt) bool {
	var scan autoRegionScan
	return scan.exprBlocksNeedAutoRegion(stmts)
}

func (scan *autoRegionScan) exprBlocksNeedAutoRegion(stmts []ast.Stmt) bool {
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
			if eb, ok := v.Interface().(*ast.ExprBlock); ok && eb != nil {
				if result, exists := scan.exprBlocks[eb]; exists {
					found = result
					return
				}
				if scan.exprBlocks == nil {
					scan.exprBlocks = make(map[*ast.ExprBlock]bool)
				}
				found = scan.exprBlockBodyNeedsAutoRegion(eb)
				if !found {
					// Stmts were checked above; the tail can itself be a scope-requiring
					// comprehension/query or a struct literal with allocating fields.
					found = scan.exprBlockTailNeedsAutoRegion(eb.Value)
					if !found {
						rec(reflect.ValueOf(eb.Value))
					}
				}
				scan.exprBlocks[eb] = found
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for _, i := range autoRegionChildFields(v.Type()) {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmts))
	return found
}
