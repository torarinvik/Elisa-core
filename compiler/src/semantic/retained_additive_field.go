package semantic

import (
	"reflect"
	"strings"

	"elisacore/src/ast"
)

// retainedAdditiveScalarEdit: current keeps every field of old unchanged and in order,
// and only inserts new defaulted scalar fields. Such an edit is invisible to a function
// that never builds, destructures, sizes, zeroes, compares or default-declares a value
// of the struct: its field reads still resolve to the same fields with the same types.
func (a *Analyzer) retainedAdditiveScalarEdit(old, current *ast.StructDecl) bool {
	if !retainedPlainStruct(old) || !retainedPlainStruct(current) || len(current.Fields) <= len(old.Fields) {
		return false
	}
	i := 0
	for _, f := range current.Fields {
		if i < len(old.Fields) && f.Name == old.Fields[i].Name {
			o := old.Fields[i]
			if f.Mutable != o.Mutable || f.Private != o.Private || f.Ghost != o.Ghost || f.IsTail != o.IsTail ||
				!retainedSameShape(f.Type, o.Type) || !retainedSameShape(f.BitGroup, o.BitGroup) || !retainedSameShape(f.DefaultValue, o.DefaultValue) {
				return false
			}
			i++
			continue
		}
		if f.DefaultValue == nil || f.Ghost || f.IsTail || len(f.Annotations) != 0 {
			return false
		}
		named, ok := f.Type.(*ast.NamedType)
		if !ok {
			return false
		}
		if known, ok := a.namedTypes[named.Name]; ok {
			if _, builtin := known.(*BuiltinType); !builtin {
				return false
			}
		}
		if named.Region != "" || !retainedScalarField(&BuiltinType{Name: named.Name}) {
			return false
		}
		if !retainedLiteralDefault(f.DefaultValue) {
			return false
		}
	}
	return i == len(old.Fields)
}

// retainedLiteralDefault: a default that names nothing, so it adds no dependency.
func retainedLiteralDefault(e ast.Expr) bool {
	switch e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit:
		return true
	}
	return false
}

// retainedSameShape compares two AST subtrees ignoring source positions.
func retainedSameShape(x, y any) bool {
	return retainedShapeEqual(reflect.ValueOf(x), reflect.ValueOf(y))
}

func retainedShapeEqual(x, y reflect.Value) bool {
	if x.IsValid() != y.IsValid() {
		return false
	}
	if !x.IsValid() {
		return true
	}
	if x.Type() != y.Type() {
		return false
	}
	switch x.Kind() {
	case reflect.Pointer, reflect.Interface:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		return retainedShapeEqual(x.Elem(), y.Elem())
	case reflect.Struct:
		for k := 0; k < x.NumField(); k++ {
			if x.Type().Field(k).Name == "Position" {
				continue
			}
			if !retainedShapeEqual(x.Field(k), y.Field(k)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if x.Len() != y.Len() {
			return false
		}
		for k := 0; k < x.Len(); k++ {
			if !retainedShapeEqual(x.Index(k), y.Index(k)) {
				return false
			}
		}
		return true
	case reflect.Map:
		return x.Len() == 0 && y.Len() == 0
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return x.IsNil() && y.IsNil()
	}
	if !x.CanInterface() {
		return false
	}
	return x.Interface() == y.Interface()
}

// retainedPackedStoresReaching names the packed stores of enums that reach a target. A
// packed store's threading mode follows its enum's payload layout, so an implicit store
// argument of such an enum observes the edit.
func (a *Analyzer) retainedPackedStoresReaching(targets map[Type]bool, memo map[Type]bool) map[string]bool {
	stores := map[string]bool{}
	for name, typ := range a.namedTypes {
		if _, ok := typ.(*EnumType); ok && retainedTypeReaches(typ, targets, memo) {
			stores[sanitizeImplicitTempBase(name)] = true
			stores[sanitizeImplicitTempBase(name[strings.LastIndex(name, ".")+1:])] = true
		}
	}
	return stores
}

// retainedObservesFieldSet: fn has a node whose meaning depends on the full field list of
// a target (construction, destructuring, layout queries, zeroing, whole-value comparison,
// an uninitialized declaration). Nodes without a recorded type count as observing.
func (a *Analyzer) retainedObservesFieldSet(fn *ast.FuncDecl, targets map[Type]bool, stores map[string]bool, memo map[Type]bool) bool {
	if !retainedPlainFunction(fn) || len(fn.Annotations) != 0 {
		return true
	}
	hit := false
	reaches := func(e ast.Expr) bool {
		t, ok := a.lookupExprType(e)
		return !ok || retainedTypeReaches(t, targets, memo)
	}
	var typedReaches func(e ast.Expr) bool
	typedReaches = func(e ast.Expr) bool {
		if t, ok := a.lookupExprType(e); ok {
			return retainedTypeReaches(t, targets, memo)
		}
		if p, ok := e.(*ast.ParenExpr); ok {
			return typedReaches(p.Inner)
		}
		return false
	}
	visit := func(node any) {
		if hit {
			return
		}
		switch n := node.(type) {
		case *ast.SizeofExpr, *ast.AlignofExpr, *ast.OffsetofExpr, *ast.MatchStructPattern,
			*ast.MoveBindStructPattern, *ast.LetDestructureStmt:
			hit = true
		case *ast.StructLitExpr:
			hit = reaches(n)
		case *ast.ZeroedLit:
			hit = reaches(n)
		case *ast.RecordUpdateExpr:
			hit = reaches(n)
		case *ast.BinaryExpr:
			// Only a whole-value operand can compare or combine the struct; logical
			// chains and variant tests leave their operands untyped.
			hit = typedReaches(n.Left) || typedReaches(n.Right)
		case *ast.CallExpr:
			for _, arg := range n.ResolvedImplicitArgs {
				if id, ok := arg.(*ast.Ident); ok && strings.HasPrefix(id.Name, "__packed_store_") {
					base := strings.TrimPrefix(strings.TrimPrefix(id.Name, PackedStoreImplicitUsePrefix), "__packed_store_")
					if base == "" || stores[base] {
						hit = true
					}
				}
			}
		case *ast.VarDeclStmt:
			hit = n.Value == nil
		}
	}
	walkStoreScanNodes(funcScanStmts(fn), visit)
	if !hit {
		walkStoreScanNodes(funcDefaultArgStmts(fn), visit)
	}
	return hit
}
