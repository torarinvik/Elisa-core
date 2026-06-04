package ast

import "reflect"

// Shared region-lifetime syntactic checks, used by the semantic analyzer (loop-retention hint)
// and the parser (automatic loop-body region tightening). They are conservative: any doubt
// resolves to "not iteration-local" / "grows outer", so a caller that gates a transform on them
// never tightens an unsafe loop.

// AllocationIsIterationLocal reports whether a local named `name` does NOT escape the given loop
// body. It is iteration-local only if every occurrence of its name is a self-op — the object of a
// method/field access (`x.push`, `x.count`), the object of a single index (`x[i]`, a value copy),
// or a `for v in x` source. Any other occurrence (a bare arg `f(x)`, a borrow `&x`, a slice
// `x[a:b]`, an alias `y = x`, a return, or ANY use inside a lambda body — which may escape with the
// closure) is treated as a potential escape.
func AllocationIsIterationLocal(name string, body []Stmt) bool {
	safe := map[*Ident]bool{}
	var occurrences []*Ident
	escaped := false
	lambdaDepth := 0
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if escaped || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *LambdaExpr:
				lambdaDepth++
				rec(v.Elem())
				lambdaDepth--
				return
			case *FieldExpr:
				if lambdaDepth == 0 {
					if id, ok := n.Object.(*Ident); ok && id.Name == name {
						safe[id] = true
					}
				}
			case *IndexExpr:
				if lambdaDepth == 0 {
					if id, ok := n.Object.(*Ident); ok && id.Name == name {
						safe[id] = true
					}
				}
			case *IterForStmt:
				if lambdaDepth == 0 {
					if id, ok := n.Source.(*Ident); ok && id.Name == name {
						safe[id] = true
					}
				}
			case *Ident:
				if n.Name == name {
					if lambdaDepth > 0 {
						escaped = true // captured by a closure: may outlive the iteration
						return
					}
					occurrences = append(occurrences, n)
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
	if escaped || len(occurrences) == 0 {
		return false
	}
	for _, id := range occurrences {
		if !safe[id] {
			return false
		}
	}
	return true
}

// LoopBodyGrowsOuterContainer reports whether the loop body grows (push/extend/reserve/cstr) a
// container NOT declared somewhere within the body — an outer-region container. Wrapping such a
// body in `in auto:` would make that growth a use-after-free escape error, so a tightening
// transform must not apply.
func LoopBodyGrowsOuterContainer(body []Stmt) bool {
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
			if vd, ok := v.Interface().(*VarDeclStmt); ok {
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
			if call, ok := v.Interface().(*CallExpr); ok {
				if fe, ok := call.Func.(*FieldExpr); ok && containerGrowthMethod(fe.Field) {
					if id, ok := fe.Object.(*Ident); ok && !declared[id.Name] {
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

func containerGrowthMethod(name string) bool {
	switch name {
	case "push", "extend", "reserve", "cstr":
		return true
	}
	return false
}
