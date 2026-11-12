package parserregionscan

import (
	"elisacore/src/ast"
	"elisacore/src/astprunedscan"
)

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

func BodyForwardsConstructedStructLocal(stmts []ast.Stmt) bool {
	found, _ := BodyForwardsConstructedStructLocalInfo(stmts)
	return found
}

func BodyForwardsConstructedStructLocalInfo(stmts []ast.Stmt) (bool, bool) {
	constructed := map[string]bool{}
	callConstructed := map[string]bool{}
	type alias struct{ name, target string }
	var aliases []alias
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		decl, ok := node.(*ast.VarDeclStmt)
		if !ok || decl.Name == "" {
			return true
		}
		switch value := unwrapParenExprAST(decl.Value).(type) {
		case *ast.Ident:
			if value != nil {
				aliases = append(aliases, alias{decl.Name, value.Name})
			}
		case *ast.RecordUpdateExpr:
			// An update may retain the base's backing storage. This prepass
			// only requests an ambient region; semantic analysis checks the
			// actual updated fields before forwarding the base's region.
			if value != nil {
				if base, ok := unwrapParenExprAST(value.Base).(*ast.Ident); ok && base != nil {
					aliases = append(aliases, alias{decl.Name, base.Name})
				}
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
	astprunedscan.WalkPruned(stmts, func(node any) bool {
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
