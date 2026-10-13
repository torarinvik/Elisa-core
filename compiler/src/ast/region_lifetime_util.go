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

// LoopBodyGrowsOuterContainerWithIterationData reports whether the loop body grows an outer
// container (see LoopBodyGrowsOuterContainer) with a value that might reference storage allocated
// inside the iteration. Growing an outer container is NOT itself a reason to keep iteration-local
// allocations alive for the whole loop: the outer container grows in its own arena (its region
// tag), so wrapping the body in a per-iteration region only changes where the body's own fresh
// allocations live. What would be unsound is pushing a value that points INTO the per-iteration
// region (a header copied out of a nested darray, a fresh literal, a call result that may be
// region-polymorphic). This check is purely syntactic and conservative: every argument of every
// outer growth call must be built from literals, outer identifiers, scalar body-declared locals,
// scalar element reads of body-declared darrays of scalars, `.count`/`.len`, arithmetic, and
// scalar conversions. Anything else (any other call, a list literal, a body-declared non-scalar
// binding, a lambda) counts as "might carry iteration data", so the caller must not tighten.
func LoopBodyGrowsOuterContainerWithIterationData(body []Stmt) bool {
	declared := map[string]TypeExpr{}
	declaredScalarArray := map[string]bool{}
	declaredScalar := map[string]bool{}
	var collect func(v reflect.Value)
	collect = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if vd, ok := v.Interface().(*VarDeclStmt); ok {
				if _, seen := declared[vd.Name]; seen {
					// Redeclared/shadowed name: be conservative, treat as neither scalar kind.
					declaredScalar[vd.Name] = false
					declaredScalarArray[vd.Name] = false
				} else {
					declared[vd.Name] = vd.Type
					declaredScalar[vd.Name] = typeExprIsScalar(vd.Type)
					declaredScalarArray[vd.Name] = typeExprIsScalarDArray(vd.Type)
				}
			}
			collect(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collect(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				collect(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				collect(v.Index(i))
			}
		}
	}
	collect(reflect.ValueOf(body))
	// Any name bound inside the body by something other than a VarDecl (an iterator binder, a
	// match/`is` binder, a catch binder, ...) may alias iteration data. We do not enumerate binder
	// shapes: every string field of every non-Ident node in the body is collected, and an identifier
	// whose name appears there is treated as body-bound (unsafe unless it is a scalar VarDecl).
	// Over-collection (e.g. a field name) only makes the check more conservative.
	boundInBody := map[string]bool{}
	var collectBound func(v reflect.Value, inIdent bool)
	collectBound = func(v reflect.Value, inIdent bool) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.String:
			if !inIdent {
				boundInBody[v.String()] = true
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			_, isIdent := v.Interface().(*Ident)
			collectBound(v.Elem(), isIdent)
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collectBound(v.Elem(), false)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				collectBound(v.Field(i), inIdent)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				collectBound(v.Index(i), false)
			}
		}
	}
	collectBound(reflect.ValueOf(body), false)
	iterBinderOverBody := boundInBody

	var safeArg func(e Expr) bool
	safeArg = func(e Expr) bool {
		switch n := e.(type) {
		case nil:
			return false
		case *IntLit, *FloatLit, *BoolLit, *CharLit, *StringLit, *NullLit:
			return true
		case *ParenExpr:
			return safeArg(n.Inner)
		case *Ident:
			if _, inBody := declared[n.Name]; inBody {
				return declaredScalar[n.Name]
			}
			if iterBinderOverBody[n.Name] {
				return false
			}
			return true // outer binding or loop index: its value predates the iteration region
		case *BinaryExpr:
			return n.LoweredCall == nil && safeArg(n.Left) && safeArg(n.Right)
		case *UnaryExpr:
			return n.LoweredCall == nil && safeArg(n.Operand)
		case *CastExpr:
			return typeExprIsScalar(n.Target) && safeArg(n.Operand)
		case *IndexExpr:
			if n.Index2 != nil {
				return false
			}
			if id, ok := n.Object.(*Ident); ok {
				if _, inBody := declared[id.Name]; inBody {
					return declaredScalarArray[id.Name] && safeArg(n.Index)
				}
				if iterBinderOverBody[id.Name] {
					return false
				}
				return safeArg(n.Index) // element of an outer container: predates the iteration
			}
			return false
		case *FieldExpr:
			switch n.Field {
			case "count", "len":
				if _, ok := n.Object.(*Ident); ok {
					return true // a length is a scalar regardless of the receiver
				}
			}
			return false
		case *CallExpr:
			// `x.u8()` / `x.usize()` style scalar conversions with no arguments.
			if fe, ok := n.Func.(*FieldExpr); ok && len(n.Args) == 0 && scalarTypeName(fe.Field) {
				return safeArg(fe.Object)
			}
			return false
		}
		return false
	}

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
					id, isIdent := fe.Object.(*Ident)
					if !isIdent {
						found = true // growth through a field chain / call result: give up
						return
					}
					if _, inBody := declared[id.Name]; !inBody {
						for _, arg := range call.Args {
							if !safeArg(arg) {
								found = true
								return
							}
						}
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

func scalarTypeName(name string) bool {
	switch name {
	case "u8", "u16", "u32", "u64", "i8", "i16", "i32", "i64", "usize", "isize", "uintptr",
		"int", "uint", "f32", "f64", "bool", "char":
		return true
	}
	return false
}

func unwrapMutableTypeExpr(t TypeExpr) TypeExpr {
	for {
		mt, ok := t.(*MutableType)
		if !ok || mt.Linear {
			return t
		}
		t = mt.Elem
	}
}

// typeExprIsScalar reports whether a declared type is syntactically a builtin scalar.
func typeExprIsScalar(t TypeExpr) bool {
	switch n := unwrapMutableTypeExpr(t).(type) {
	case *NamedType:
		return n.Region == "" && scalarTypeName(n.Name)
	case *BuiltinTypeExpr:
		return len(n.TypeArgs) == 0 && len(n.ValueArgs) == 0 && scalarTypeName(n.Name)
	}
	return false
}

// typeExprIsScalarDArray reports whether a declared type is syntactically `darray[<scalar>]`/`dstr`.
func typeExprIsScalarDArray(t TypeExpr) bool {
	switch n := unwrapMutableTypeExpr(t).(type) {
	case *NamedType:
		return n.Name == "dstr"
	case *BuiltinTypeExpr:
		if n.Name == "dstr" {
			return true
		}
		return n.Name == "darray" && len(n.TypeArgs) == 1 && typeExprIsScalar(n.TypeArgs[0])
	}
	return false
}
