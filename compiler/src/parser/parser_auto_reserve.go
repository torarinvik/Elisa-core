package parser

import (
	"os"
	"reflect"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Auto-reservation (region inference, Phase A): when a freshly-declared inferred-region darray
// is filled by a counting loop, the compiler pre-sizes it so it never reallocates during the
// fill. A growing buffer that reallocates leaves a dead hole (or relocates, invalidating interior
// refs); pre-sizing avoids that AND turns the darray into a fixed-footprint citizen that packs
// densely in the region stack (it can no longer block a sibling's tail growth).
//
//	xs: darray[T] = []                 xs: darray[T] = []
//	for i in 0..<n:           ===>      xs.reserve(n)           # inserted
//	    xs.push(f(i))                   for i in 0..<n:
//	                                        xs.push(f(i))
//
// `extend` is treated as a fill too. The bound is a conservative reservation: it may not cover
// every element appended by each extension, but it still removes avoidable early growth without
// changing values. `push([a, b, c])` contributes its literal element count to the reserve bound.
//
// This is a pure optimization: reserve only pre-allocates capacity (it never changes observable
// length or values), and over-reserving is safe. v1 fires only for the side-effect-free counting
// shape `for i in 0..<END` with a literal-0 start and END an identifier/integer — so the inserted
// reserve evaluates END identically to the loop itself. Opt out with ELISA_NO_AUTO_RESERVE.
var autoReserveDisabled = os.Getenv("ELISA_NO_AUTO_RESERVE") != ""

// autoReserveBoundedFills rewrites a statement list, inserting a `reserve` before each eligible
// (fresh-darray decl, counting-fill loop) adjacent pair. Applied per parsed block, so it reaches
// every nesting level.
func autoReserveBoundedFills(stmts []ast.Stmt) []ast.Stmt {
	if autoReserveDisabled || len(stmts) < 2 {
		return stmts
	}
	out := make([]ast.Stmt, 0, len(stmts)+1)
	for i := 0; i < len(stmts); i++ {
		out = append(out, stmts[i])
		vd, ok := stmts[i].(*ast.VarDeclStmt)
		if !ok || !isFreshInferredDArrayDecl(vd) || i+1 >= len(stmts) {
			continue
		}
		loop, ok := stmts[i+1].(*ast.ForStmt)
		if !ok {
			continue
		}
		bound, ok := countingFillBound(loop, vd.Name)
		if !ok {
			continue
		}
		out = append(out, makeReserveStmt(vd.Name, bound, loop.Position))
	}
	return out
}

// isFreshInferredDArrayDecl reports whether vd declares `name: [mutable] darray[T] = []` with no
// explicit `@r` region — i.e. a darray inference placed in the enclosing synthesized region.
func isFreshInferredDArrayDecl(vd *ast.VarDeclStmt) bool {
	if vd == nil {
		return false
	}
	lit, ok := vd.Value.(*ast.ListLitExpr)
	if !ok || lit.Brace || len(lit.Elems) != 0 {
		return false
	}
	te := vd.Type
	if mt, ok := te.(*ast.MutableType); ok {
		te = mt.Elem
	}
	bt, ok := te.(*ast.BuiltinTypeExpr)
	return ok && bt.Name == "darray" && bt.Region == ""
}

// countingFillBound returns the upper-bound expression for a `for <i> in 0..<END:` loop whose body
// pushes to name, when END is a side-effect-free identifier/integer. The returned node is a fresh
// clone so it does not alias the loop's own bound.
func countingFillBound(loop *ast.ForStmt, name string) (ast.Expr, bool) {
	if loop == nil || loop.Reverse || loop.Op != lexer.TOKEN_RANGE_LT || loop.Step != nil {
		return nil, false
	}
	if start, ok := loop.Start.(*ast.IntLit); !ok || start.Value != "0" {
		return nil, false
	}
	clone := cloneBoundExpr(loop.End)
	if clone == nil {
		return nil, false // not a pure ident/int bound
	}
	perIteration, ok := bodyGrowthPerIteration(loop.Body, name)
	if !ok {
		return nil, false
	}
	if perIteration > 1 {
		clone = &ast.BinaryExpr{
			Position: clone.Pos(),
			Op:       lexer.TOKEN_STAR,
			Left:     clone,
			Right:    &ast.IntLit{Position: loop.Position, Value: strconv.Itoa(perIteration)},
		}
	}
	return clone, true
}

// cloneBoundExpr returns a fresh copy of a side-effect-free bound (identifier or integer literal),
// or nil if the expression is anything else (a call, arithmetic, field access — not v1).
func cloneBoundExpr(e ast.Expr) ast.Expr {
	switch n := e.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	}
	return nil
}

// bodyGrowthPerIteration returns the syntactically known minimum number of elements appended to
// name per loop iteration by `push(...)` / `extend(...)` calls. Scalar push and extend each count
// as 1; `push([a, b, c])` counts as 3.
func bodyGrowthPerIteration(body []ast.Stmt, name string) (int, bool) {
	growth := 0
	disqualified := false
	var walk func(v reflect.Value, loopDepth int)
	walk = func(v reflect.Value, loopDepth int) {
		if disqualified {
			return
		}
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if field, ok := call.Func.(*ast.FieldExpr); ok && (field.Field == "push" || field.Field == "extend") {
					if recv, ok := field.Object.(*ast.Ident); ok && recv.Name == name {
						if loopDepth > 0 {
							disqualified = true
							return
						}
						growth += growthCallElementCount(field.Field, call)
						return
					}
				}
			}
			if node, ok := v.Interface().(ast.Node); ok && autoReserveLoopNode(node) {
				walk(v.Elem(), loopDepth+1)
				return
			}
			walk(v.Elem(), loopDepth)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), loopDepth)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), loopDepth)
			}
		}
	}
	walk(reflect.ValueOf(body), 0)
	return growth, growth > 0 && !disqualified
}

func growthCallElementCount(method string, call *ast.CallExpr) int {
	if method != "push" || call == nil || len(call.Args) != 1 {
		return 1
	}
	list, ok := call.Args[0].(*ast.ListLitExpr)
	if !ok || list == nil || list.Brace || len(list.Elems) == 0 {
		return 1
	}
	return len(list.Elems)
}

func autoReserveLoopNode(n ast.Node) bool {
	switch n.(type) {
	case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt, *ast.ParallelForStmt:
		return true
	}
	return false
}

// makeReserveStmt builds `name.reserve(bound)` as a bare expression statement.
func makeReserveStmt(name string, bound ast.Expr, pos lexer.Pos) ast.Stmt {
	return &ast.ExprStmt{
		Position: pos,
		Expr: &ast.CallExpr{
			Position: pos,
			Func:     &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: name}, Field: "reserve"},
			Args:     []ast.Expr{bound},
		},
	}
}
