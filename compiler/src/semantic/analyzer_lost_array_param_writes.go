package semantic

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/astscan"
)

// checkLostArrayParamWrites rejects a by-value `mutable T[N]` parameter that the body writes but
// never observes: no read of it reaches anything except further writes to itself. A fixed array
// parameter is a COPY, so those writes vanish when the function returns. Code ported from C (where
// an array parameter is a pointer) almost always meant `p: mutable T[N]&` with `&array` at the call.
// A parameter that is read, returned, passed on, iterated or captured is a legitimate scratch copy.
func (a *Analyzer) checkLostArrayParamWrites(fn *ast.FuncDecl, params []ast.ParamDecl, paramTypes []Type) {
	if fn == nil || len(fn.Body) == 0 {
		return
	}
	for i, param := range params {
		if i >= len(paramTypes) || !a.paramIsMutable(param) || param.Name == "" || param.Name == "_" {
			continue
		}
		if array, ok := paramTypes[i].(*ArrayType); !ok || array == nil {
			continue
		}
		if lostArrayParamWrites(fn.Body, param.Name) {
			a.errorf(param.Position, "%s", LostArrayParamWritesMessage(param.Name))
		}
	}
}

// lostArrayParamWrites reports whether body writes `name` at least once and every other mention of
// `name` sits inside one of those self-writes. A body that rebinds the name anywhere is skipped.
func lostArrayParamWrites(body []ast.Stmt, name string) bool {
	writes, insideWrites, total := 0, 0, 0
	rebound := false
	countIdents := func(node any) int {
		count := 0
		astscan.WalkPointers(node, func(visited any) {
			if ident, ok := visited.(*ast.Ident); ok && ident.Name == name {
				count++
			}
		}, map[uintptr]bool{})
		return count
	}
	astscan.WalkPointers(body, func(visited any) {
		switch node := visited.(type) {
		case *ast.Ident:
			if node.Name == name {
				total++
			}
		case *ast.AssignStmt:
			if writeTargetRoot(node.Target) == name {
				writes++
				insideWrites += countIdents(node.Target) + countIdents(node.Value)
			}
		case *ast.AugAssignStmt:
			if node.CollectionAppend == nil && writeTargetRoot(node.Target) == name {
				writes++
				insideWrites += countIdents(node.Target) + countIdents(node.Value)
			}
		default:
			if bindsName(visited, name) {
				rebound = true
			}
		}
	}, map[uintptr]bool{})
	return !rebound && writes > 0 && total == insideWrites
}

// writeTargetRoot is the variable an assignment target writes through: `p`, `p[i]`, `p[i].x`.
func writeTargetRoot(target ast.Expr) string {
	for {
		switch node := target.(type) {
		case *ast.Ident:
			return node.Name
		case *ast.IndexExpr:
			target = node.Object
		case *ast.FieldExpr:
			target = node.Object
		case *ast.ParenExpr:
			target = node.Inner
		default:
			return ""
		}
	}
}

// bindsName reports whether a non-identifier node carries `Name == name`: a local, loop variable,
// lambda parameter or pattern binder that would shadow the parameter.
func bindsName(node any, name string) bool {
	value := reflect.ValueOf(node)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	field := value.FieldByName("Name")
	return field.IsValid() && field.Kind() == reflect.String && field.String() == name
}
