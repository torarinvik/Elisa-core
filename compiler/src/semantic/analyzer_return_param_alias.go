package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// collectReturnParamAliasLocations scans a function's return statements for values that are a
// reference borrow rooted at a parameter — `return p`, `return &self.value`, `return self.refField`
// — and reports each as a param-rooted location ("p", "self.value", ...). This closes the
// laundering gap for FIELD borrows of a parameter, which the region/owner return provenance does
// not record (it tracks whole-param and region-owned-interior borrows only). The result feeds the
// call-argument alias checker via ReturnIsolation and influences nothing else.
func (a *Analyzer) collectReturnParamAliasLocations(fn *ast.FuncDecl, fnType *FuncType) []string {
	if a == nil || fn == nil || fnType == nil || fnType.Return == nil {
		return nil
	}
	// Only a function that returns a reference can hand back an alias of a parameter. Returning a
	// by-value copy (even of a field) creates no alias, so it is out of scope.
	if _, isRef := StripAggregateStateType(fnType.Return).(*RefType); !isRef {
		return nil
	}
	seen := map[string]bool{}
	var locations []string
	a.walkReturnStmts(fn.Body, func(ret *ast.ReturnStmt) {
		if ret == nil || ret.Value == nil {
			return
		}
		location := a.returnExprParamAliasLocation(ret.Value, fnType)
		if location == "" || seen[location] {
			return
		}
		seen[location] = true
		locations = append(locations, location)
	})
	return locations
}

// returnExprParamAliasLocation returns the param-rooted location a returned reference borrows, or
// "". It unwraps borrow/cast/move/paren wrappers and walks a field/index access chain down to a
// base identifier; if that identifier names a parameter and the value is genuinely a reference (an
// explicit `&…` borrow, or a ref-typed place), the parameter location is returned.
func (a *Analyzer) returnExprParamAliasLocation(value ast.Expr, fnType *FuncType) string {
	expr := value
	sawBorrow := false
unwrap:
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.AddrOfExpr:
			sawBorrow = true
			expr = n.Operand
		default:
			break unwrap
		}
	}
	suffix := ""
chain:
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			suffix = "." + n.Field + suffix
			expr = n.Object
		case *ast.IndexExpr:
			suffix = "[*]" + suffix
			expr = n.Object
		default:
			break chain
		}
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	if functionParamIndex(fnType, ident.Name) < 0 {
		return ""
	}
	if !sawBorrow {
		// No explicit `&` — only an alias if the returned place is itself reference-typed.
		if _, isRef := a.exprTypes[value].(*RefType); !isRef {
			return ""
		}
	}
	return ident.Name + suffix
}

// walkReturnStmts invokes visit for every ReturnStmt reachable in body, including nested control
// flow and expression blocks, but not crossing into nested function/lambda bodies.
func (a *Analyzer) walkReturnStmts(body []ast.Stmt, visit func(*ast.ReturnStmt)) {
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
			if iface := v.Interface(); iface != nil {
				if _, isLambda := iface.(*ast.LambdaExpr); isLambda {
					return
				}
				if ret, ok := iface.(*ast.ReturnStmt); ok {
					visit(ret)
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
}
