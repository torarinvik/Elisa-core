package semantic

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// earlyFreeableObject returns the byte offset of the last top-level statement referencing `name`,
// when that object can be freed there: it is referenced ONLY as a method/field receiver or an index
// base (never aliased — no address taken, no bare-value use that could copy its buffer pointer), and
// it dies strictly before the region's final statement. ok=false means keep it to region exit.
func earlyFreeableObject(topLevel []ast.Stmt, name string, dependencies *earlyFreeGraph) (int, bool) {
	if objectMayEscape(topLevel, name) {
		return 0, false
	}
	// A local whose value was computed from the object (`v = buf.as_sview()`, an enum payload or
	// tuple holding such a view, a closure capturing it) may still point into its buffer: the
	// object is live until the last use of ANY such derived local, not just its own last mention.
	if *dependencies == nil {
		*dependencies = buildEarlyFreeGraph(topLevel)
	}
	derived := dependencies.derivedNames(name)
	mentions := func(name string) bool { return derived[name] }
	for i := len(topLevel) - 1; i >= 0; i-- {
		if reflectSubtreeHasIdent(reflect.ValueOf(topLevel[i]), mentions) {
			if i == len(topLevel)-1 {
				break // Still live at region exit.
			}
			return topLevel[i].Pos().Offset, true
		}
	}
	return 0, false
}

// Each binding or store connects all of its input names to its output names.
// Keep this syntax-only graph for every early-free query in the same region.
type earlyFreeGraph map[string][][]string

func buildEarlyFreeGraph(body []ast.Stmt) earlyFreeGraph {
	dependencies := make(earlyFreeGraph)
	seenInputs := make(map[string]bool)
	add := func(value reflect.Value, outputs []string) {
		if len(outputs) == 0 {
			return
		}
		reflectSubtreeHasIdent(value, func(input string) bool {
			if !seenInputs[input] {
				seenInputs[input] = true
				dependencies[input] = append(dependencies[input], outputs)
			}
			return false
		})
	}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			typ := v.Type()
			// A declared scalar copies a value, so it cannot retain the buffer.
			scalar := typ == reflect.TypeFor[ast.VarDeclStmt]() && earlyFreeScalarDeclType(v.Interface().(ast.VarDeclStmt).Type)
			if !scalar {
				if value := reflectFieldByName(v, "Value"); value.IsValid() {
					var outputs []string
					for _, field := range [...]string{"Name", "Names", "Target", "Pattern", "Store"} {
						outputs = append(outputs, earlyFreeBoundNames(reflectFieldByName(v, field))...)
					}
					clear(seenInputs)
					add(value, outputs)
				}
			}
			if typ == reflect.TypeFor[ast.CallExpr]() {
				call := v.Interface().(ast.CallExpr)
				var outputs []string
				if field, ok := call.Func.(*ast.FieldExpr); ok {
					outputs = append(outputs, earlyFreePlaceRoot(field.Object))
				}
				for _, arg := range call.Args {
					if unary, ok := arg.(*ast.UnaryExpr); ok && unary.Op == lexer.TOKEN_AMPERSAND {
						outputs = append(outputs, earlyFreePlaceRoot(unary.Operand))
					}
					if addr, ok := arg.(*ast.AddrOfExpr); ok {
						outputs = append(outputs, earlyFreePlaceRoot(addr.Operand))
					}
				}
				clear(seenInputs)
				for _, arg := range call.Args {
					add(reflect.ValueOf(arg), outputs)
				}
			}
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
	return dependencies
}

// derivedNames computes the transitive closure of the original conservative
// rules. Cyclic bindings stop when each name has been visited.
func (graph earlyFreeGraph) derivedNames(name string) map[string]bool {
	derived := map[string]bool{name: true}
	work := []string{name}
	for len(work) > 0 {
		input := work[len(work)-1]
		work = work[:len(work)-1]
		for _, outputs := range graph[input] {
			for _, output := range outputs {
				if output != "" && !derived[output] {
					derived[output] = true
					work = append(work, output)
				}
			}
		}
	}
	return derived
}

// earlyFreeScalarDeclType: a declared plain value scalar (not `uintptr`, which can hold an address).
func earlyFreeScalarDeclType(t ast.TypeExpr) bool {
	named, ok := t.(*ast.NamedType)
	if !ok || named == nil || named.Region != "" {
		return false
	}
	switch named.Name {
	case "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "isize", "usize", "f32", "f64", "bool", "char":
		return true
	}
	return false
}

// earlyFreeBoundNames collects every `Name` string and identifier under a binding/target field.
func earlyFreeBoundNames(v reflect.Value) []string {
	out := []string{}
	var walk func(v reflect.Value, fieldName string)
	walk = func(v reflect.Value, fieldName string) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.String:
			if fieldName == "Name" || fieldName == "Names" {
				out = append(out, v.String())
			}
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok {
				out = append(out, id.Name)
				return
			}
			walk(v.Elem(), fieldName)
		case reflect.Struct:
			t := v.Type()
			rfs := reflectFields(t)
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), rfs[i].Name)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), fieldName)
			}
		}
	}
	walk(v, "Name")
	return out
}
