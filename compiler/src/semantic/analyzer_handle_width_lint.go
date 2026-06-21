package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
)

// The narrow-handle width lint (docs/76 Phase 6 tail / docs/82): when a constant-bounded
// counting loop in a function that OWNS its region scope builds nodes of a default-width
// (u32-handle) recursive enum, the store's population is locally provable to fit a narrower
// handle — suggest `layout(handle: u16)` (or u8). Evidence is strictly LOCAL (the docs/76
// rule: a constant bound in front of your eyes, never distant-usage inference), and the
// region owned by the same function means the store dies here too. Advisory under -Wperf.
func (a *Analyzer) checkNarrowableHandleWidths(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	if !a.funcOwnsRegion(fn) {
		return // store may outlive / be shared beyond what this function can see
	}
	a.findNarrowableHandleLoops(fn.Body)
}

func (a *Analyzer) findNarrowableHandleLoops(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagNarrowableHandleLoop(s)
			a.findNarrowableHandleLoops(s.Body)
		case *ast.WhileStmt:
			a.findNarrowableHandleLoops(s.Body)
		case *ast.IterForStmt:
			a.findNarrowableHandleLoops(s.Body)
		case *ast.IfStmt:
			a.findNarrowableHandleLoops(s.Then)
			a.findNarrowableHandleLoops(s.Else)
		case *ast.ScopeStmt:
			a.findNarrowableHandleLoops(s.Body)
		case *ast.CanStmt:
			a.findNarrowableHandleLoops(s.Body)
		case *ast.RegionStmt:
			a.findNarrowableHandleLoops(s.Body)
		case *ast.InStoreStmt:
			a.findNarrowableHandleLoops(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findNarrowableHandleLoops(arm.Body)
			}
		}
	}
}

func (a *Analyzer) flagNarrowableHandleLoop(loop *ast.ForStmt) {
	boundExpr, ok := semanticCountingLoopBoundExpr(loop)
	if !ok {
		return
	}
	lit, ok := boundExpr.(*ast.IntLit)
	if !ok || lit == nil {
		return
	}
	bound, err := strconv.ParseUint(strings.TrimRight(lit.Value, "u"), 10, 64)
	if err != nil || bound == 0 {
		return
	}
	// Count direct constructor allocations per iteration, per default-width recursive root.
	perRoot := map[*EnumType]uint64{}
	recursiveCall := false
	a.walkStaticStmts(loop.Body, func(e ast.Expr) bool {
		switch n := e.(type) {
		case *ast.AllocExpr:
			if enumType, _, ok := a.packedAllocConstructorInfo(n.Value); ok && enumType != nil && enumType.Packed {
				root := enumType.Root()
				if root.RecursivePlain && root.IndexWidth == "" {
					perRoot[root]++
				}
			}
		case *ast.CallExpr:
			if et, ok := a.regionBackedEnumConstructor(n); ok && et != nil {
				root := et.Root()
				if root.RecursivePlain && root.IndexWidth == "" {
					perRoot[root]++
				}
			} else if _, ok := n.Func.(*ast.Ident); ok {
				// A call to another function may construct an unbounded subtree —
				// the local bound is no longer the whole story; stay silent.
				recursiveCall = true
			}
		}
		return false
	})
	if recursiveCall || len(perRoot) == 0 {
		return
	}
	for root, perIter := range perRoot {
		total := bound * perIter
		switch {
		case total <= 254:
			a.perfLint(loop.Pos(), "this loop builds at most %d %s nodes into a region owned by this function; a u32 handle wastes bytes per edge — consider `enum %s layout(handle: u8)` (1 byte/edge) or `handle: u16`", total, root.Name, root.Name)
		case total <= 65534:
			a.perfLint(loop.Pos(), "this loop builds at most %d %s nodes into a region owned by this function; a u32 handle wastes bytes per edge — consider `enum %s layout(handle: u16)` (2 bytes/edge)", total, root.Name, root.Name)
		}
	}
}
