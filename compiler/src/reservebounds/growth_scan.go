package reservebounds

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

func CollectGrowthTargetCounts(body []ast.Stmt) (map[string]ast.Expr, []string) {
	counts := map[string]ast.Expr{}
	order := []string{}
	listed := map[string]bool{}
	disqualified := map[string]bool{}
	grow := func(name string, growth ast.Expr, pos lexer.Pos) {
		if !listed[name] {
			listed[name] = true
			order = append(order, name)
		}
		counts[name] = AddReserveExpr(counts[name], growth, pos)
	}
	var walk func(v reflect.Value, loopDepth int)
	walk = func(v reflect.Value, loopDepth int) {
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
					if recv, ok := field.Object.(*ast.Ident); ok {
						if loopDepth > 0 {
							disqualified[recv.Name] = true
							delete(counts, recv.Name)
							return
						}
						if disqualified[recv.Name] {
							return
						}
						grow(recv.Name, IntReserveExpr(growthCallElementCount(field.Field, call), call.Pos()), call.Pos())
						return
					}
				}
			}
			if node, ok := v.Interface().(ast.Node); ok && isLoopNode(node) {
				if loop, ok := node.(*ast.ForStmt); ok && loopDepth == 0 {
					if bound, ok := CountingLoopBoundExpr(loop); ok {
						inner, innerOrder := CollectGrowthTargetCounts(loop.Body)
						for _, name := range innerOrder {
							if disqualified[name] {
								continue
							}
							grow(name, MultiplyReserveExpr(bound, inner[name], loop.Position), loop.Position)
						}
						return
					}
				}
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
	// A receiver grown inside a nested non-counting loop after it was first counted is dropped
	// from counts; keep only the survivors, still in first-grown order.
	names := make([]string, 0, len(counts))
	for _, name := range order {
		if _, ok := counts[name]; ok {
			names = append(names, name)
		}
	}
	return counts, names
}

func CollectGrowthTargetNames(body []ast.Stmt) map[string]bool {
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
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if field, ok := call.Func.(*ast.FieldExpr); ok && (field.Field == "push" || field.Field == "extend") {
					if recv, ok := field.Object.(*ast.Ident); ok {
						names[recv.Name] = true
						return
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

func isLoopNode(node ast.Node) bool {
	switch node.(type) {
	case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt, *ast.ParallelForStmt:
		return true
	}
	return false
}
