package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// A retained update accepts an edit to a plain struct declaration (fields added, removed,
// retyped or given new defaults). The scratch declaration phase has already run on the edited
// program and every declaration-phase result was compared, so the live analysis only has to take
// the new field table, the new declaration, drop the memos computed from the old shape, and
// re-analyze the functions whose types or text reach the struct.

// retainedStructEdit pairs a struct's live declaration with its edited one.
type retainedStructEdit struct {
	live, edit *ast.StructDecl
	scope      scopedDecl
	st         *StructType
}

// retainedPlainStruct: a struct with none of the features whose declaration-phase effects
// extend past its own field table (generics, regions, typestate, layout modes, annotations,
// affinity, invariants, ghost / tail / packed fields).
func retainedPlainStruct(d *ast.StructDecl) bool {
	if d == nil || len(d.Annotations) != 0 || len(d.TypeParams) != 0 || len(d.RegionParams) != 0 ||
		d.RegionOwner != "" || len(d.GenericParams) != 0 || d.HasStateParam || len(d.NamedStateCases) != 0 ||
		len(d.TerminalStateCases) != 0 || len(d.DerivedStates) != 0 || d.HasDerivedStateBlock ||
		len(d.StateTransitions) != 0 || d.Affine || d.Resource || d.Droppable || d.ReprC ||
		d.Layout != ast.StructLayoutDefault || len(d.Invariants) != 0 {
		return false
	}
	for _, f := range d.Fields {
		if len(f.Annotations) != 0 || f.IsTail || f.Ghost || f.BitGroup != nil {
			return false
		}
	}
	return true
}

// Explicit region arguments preserve their binding protocol when only fields
// change. Other generic binders still need their declaration machinery rebuilt.
func retainedStructFieldEditAllowed(old, current *ast.StructDecl) bool {
	if retainedPlainStruct(old) && retainedPlainStruct(current) {
		return true
	}
	if old.Name != current.Name || !reflect.DeepEqual(old.RegionParams, current.RegionParams) || len(old.RegionParams) == 0 {
		return false
	}
	regionOnly := func(d *ast.StructDecl) bool {
		for _, p := range d.GenericParams {
			if p.Kind != ast.GenericParamRegion || p.InterfaceBound != "" || p.StateOwner != "" || len(p.StateCases) != 0 {
				return false
			}
		}
		shape := *d
		shape.RegionParams, shape.GenericParams = nil, nil
		return retainedPlainStruct(&shape)
	}
	if !regionOnly(old) || !regionOnly(current) || len(old.GenericParams) != len(current.GenericParams) {
		return false
	}
	for i, p := range old.GenericParams {
		if p.Name != current.GenericParams[i].Name {
			return false
		}
	}
	return true
}

// retainedTypeMemos are analyzer memos keyed by type whose answers depend on struct fields.
var retainedTypeMemos = []string{
	"storeScanNoStorageCache", "storeScanViewOnlyCache", "storeScanSharesCache", "resolvedStructFieldsMemo",
	"affineHandleMemo", "typeSeenScratch", "borrowedOwnerRefMemo", "regionRefsMemo",
	"mutableBorrowCapabilityMemo", "containerRegionMemo", "storeScanAddressMemo", "storeScanAddressSeen",
}

var retainedTypeMemoNames = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range retainedTypeMemos {
		m[n] = true
	}
	return m
}()

func (a *Analyzer) clearRetainedTypeMemos() {
	v := reflect.ValueOf(a).Elem()
	for _, name := range retainedTypeMemos {
		f := v.FieldByName(name)
		if f.IsValid() && f.Kind() == reflect.Map && f.CanSet() {
			f.Set(reflect.MakeMap(f.Type()))
		}
	}
}

// findLiveStructEdits locates each edited struct's live declaration and type.
func (r *RetainedAnalysis) findLiveStructEdits(edits [][2]*ast.StructDecl) ([]retainedStructEdit, string) {
	a := r.a
	byKey := map[string]scopedDecl{}
	for _, sd := range r.structDecls() {
		if d, ok := sd.Decl.(*ast.StructDecl); ok {
			byKey[retainedLineKey(d.Pos())+"|"+d.Name] = sd
		}
	}
	out := make([]retainedStructEdit, 0, len(edits))
	for _, e := range edits {
		sd, ok := byKey[retainedLineKey(e[0].Pos())+"|"+e[0].Name]
		if !ok {
			return nil, "struct " + e[0].Name + " not found in the analysis"
		}
		live := sd.Decl.(*ast.StructDecl)
		st, _ := a.namedTypes[joinQualifiedName(sd.Namespace, live.Name)].(*StructType)
		if st == nil || st.Builtin || len(st.TypeParams) != 0 {
			return nil, "struct " + e[0].Name + " has no plain analyzed type"
		}
		out = append(out, retainedStructEdit{live: live, edit: e[1], scope: sd, st: st})
	}
	return out, ""
}

// structDecls lists the live analysis's struct declarations with their scope.
func (r *RetainedAnalysis) structDecls() []scopedDecl {
	return r.a.flattenScopedDecls(r.a.file.Decls, "", nil)
}

// retainedUsesTypes reports whether fn's types or text reach one of the edited types.
func (a *Analyzer) retainedUsesTypes(fn *ast.FuncDecl, targets map[Type]bool, names map[string]bool, memo map[Type]bool) bool {
	if a.retained != nil {
		if uses, ok := a.retained.bodyUses[fn]; ok {
			for _, name := range uses.names {
				if names[name] {
					return true
				}
			}
			if sym := a.funcDeclSymbols[fn]; sym != nil && retainedTypeReaches(sym.Type, targets, memo) {
				return true
			}
			for _, typ := range uses.types {
				if retainedTypeReaches(typ, targets, memo) {
					return true
				}
			}
			return false
		}
	}
	if retainedMentionsAny(fn, names) {
		return true
	}
	if sym := a.funcDeclSymbols[fn]; sym != nil && retainedTypeReaches(sym.Type, targets, memo) {
		return true
	}
	hit := false
	visit := func(node any) {
		if hit {
			return
		}
		if e, ok := node.(ast.Expr); ok {
			if t, ok := a.exprTypes[e]; ok && retainedTypeReaches(t, targets, memo) {
				hit = true
			}
		}
	}
	walkStoreScanNodes(funcScanStmts(fn), visit)
	if !hit {
		walkStoreScanNodes(funcDefaultArgStmts(fn), visit)
	}
	return hit
}

// retainedTypeReaches: t is, or is built from, one of the targets. Other named types are not
// entered: a value of them reaches a target only through an expression typed as the target.
// A struct with region-annotated fields is analyzed through per-binding copies of its type, so
// a struct type also matches a target of the same qualified name.
func retainedTypeReaches(t Type, targets map[Type]bool, memo map[Type]bool) bool {
	if t == nil {
		return false
	}
	if hit, ok := memo[t]; ok {
		return hit
	}
	memo[t] = false
	targetNames := map[string]bool{}
	for target := range targets {
		if st, ok := target.(*StructType); ok {
			targetNames[st.Name] = true
		}
	}
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value) bool
	rec = func(v reflect.Value) bool {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return false
			}
			seen[v.Pointer()] = true
			switch n := v.Interface().(type) {
			case *StructType:
				return targets[n] || targetNames[n.Name]
			case *EnumType:
				return targets[n]
			case *ConstEnumType:
				return targets[n]
			}
			return rec(v.Elem())
		case reflect.Interface:
			return !v.IsNil() && rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if f := v.Field(i); f.CanInterface() && rec(f) {
					return true
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				if rec(v.Index(i)) {
					return true
				}
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				if rec(iter.Value()) {
					return true
				}
			}
		}
		return false
	}
	hit := rec(reflect.ValueOf(t))
	memo[t] = hit
	return hit
}

// retainedScalarField: a field type that holds no address, so adding or removing it cannot
// change what a value of the struct carries.
func retainedScalarField(t Type) bool {
	switch t := t.(type) {
	case *BitIntType:
		return true
	case *BuiltinType:
		switch t.Name {
		case "bool", "u8", "u16", "u32", "u64", "i8", "i16", "i32", "i64", "usize", "isize", "f32", "f64", "char":
			return true
		}
	}
	return false
}

// retainedFlowNeutral: the edit kept every surviving field's type and added or removed only
// scalars, so store-flow summaries computed from the old shape still hold for functions whose
// bodies did not change.
func retainedFlowNeutral(old, cur map[string]Field) bool {
	for name, f := range old {
		if g, ok := cur[name]; ok {
			if g.Type.String() != f.Type.String() || g.Mutable != f.Mutable {
				return false
			}
		} else if !retainedScalarField(f.Type) {
			return false
		}
	}
	for name, g := range cur {
		if _, ok := old[name]; !ok && !retainedScalarField(g.Type) {
			return false
		}
	}
	return true
}

// applyStructEdits replaces each live declaration and re-populates its type's fields in place,
// so every type already pointing at the struct sees the new shape. It reports whether every
// edit was flow-neutral (retainedFlowNeutral).
func (r *RetainedAnalysis) applyStructEdits(edits []retainedStructEdit) bool {
	a := r.a
	replace := map[ast.Decl]ast.Decl{}
	for _, e := range edits {
		replace[e.live] = e.edit
	}
	var swap func(decls []ast.Decl)
	swap = func(decls []ast.Decl) {
		for i, d := range decls {
			if n, ok := replace[d]; ok {
				decls[i] = n
			}
			if ns, ok := d.(*ast.NamespaceDecl); ok {
				swap(ns.Decls)
			}
		}
	}
	swap(a.file.Decls)
	for i, sd := range r.decls {
		if n, ok := replace[sd.Decl]; ok {
			r.decls[i].Decl = n
		}
	}
	scoped := make([]scopedDecl, 0, len(edits))
	olds := make([]map[string]Field, len(edits))
	for i, e := range edits {
		olds[i] = e.st.Fields
		e.st.Decl = e.edit
		e.st.Fields = map[string]Field{}
		e.st.GhostFieldOrder, e.st.StoreFieldOrder, e.st.HasPackedGroups = nil, nil, false
		e.st.declarationFieldOrder = astFieldDeclNames(e.edit.Fields)
		sd := e.scope
		sd.Decl = e.edit
		scoped = append(scoped, sd)
	}
	a.populateStructFields(scoped)
	a.clearRetainedTypeMemos()
	for i, e := range edits {
		if !retainedFlowNeutral(olds[i], e.st.Fields) {
			return false
		}
	}
	return true
}

// retainedHoldsByValue: a value of t stores one of the targets inline (fields, array elements,
// optionals, tuples, enum payloads), so its layout follows theirs. References, darrays and views
// hold an address and do not.
func retainedHoldsByValue(t Type, targets map[Type]bool, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	if targets[t] {
		return true
	}
	seen[t] = true
	switch n := t.(type) {
	case *StructType:
		for _, f := range n.Fields {
			if retainedHoldsByValue(f.Type, targets, seen) {
				return true
			}
		}
	case *EnumType:
		for _, v := range n.Variants {
			for _, p := range v.Payload {
				if retainedHoldsByValue(p, targets, seen) {
					return true
				}
			}
		}
	case *GenericInstanceType:
		return retainedHoldsByValue(n.Base, targets, seen)
	case *ArrayType:
		return retainedHoldsByValue(n.Elem, targets, seen)
	case *OptionalType:
		return retainedHoldsByValue(n.Value, targets, seen)
	case *TupleType:
		for _, f := range n.Fields {
			if retainedHoldsByValue(f.Type, targets, seen) {
				return true
			}
		}
	}
	return false
}

// widenLayoutTargets adds every named type that holds a target by value: its layout changed
// with the target's, so its users are re-analyzed too.
func (a *Analyzer) widenLayoutTargets(targets map[Type]bool) {
	for {
		grew := false
		for _, t := range a.namedTypes {
			if targets[t] {
				continue
			}
			switch t.(type) {
			case *StructType, *EnumType:
			default:
				continue
			}
			if retainedHoldsByValue(t, targets, map[Type]bool{}) {
				targets[t] = true
				grew = true
			}
		}
		if !grew {
			return
		}
	}
}

// removeLiveStructs drops removed structs from the live analysis: their declaration and type.
func (r *RetainedAnalysis) removeLiveStructs(sds []scopedDecl) {
	a := r.a
	gone := map[ast.Decl]bool{}
	for _, sd := range sds {
		gone[sd.Decl] = true
		name := joinQualifiedName(sd.Namespace, sd.Decl.(*ast.StructDecl).Name)
		delete(a.namedTypes, name)
		delete(a.privateTypeNames, name)
		delete(a.declVisibility, sd.Decl)
	}
	decls := r.decls[:0]
	for _, sd := range r.decls {
		if !gone[sd.Decl] {
			decls = append(decls, sd)
		}
	}
	r.decls = decls
	var prune func(decls []ast.Decl) []ast.Decl
	prune = func(decls []ast.Decl) []ast.Decl {
		out := decls[:0]
		for _, d := range decls {
			if n, ok := d.(*ast.NamespaceDecl); ok {
				n.Decls = prune(n.Decls)
			}
			if !gone[d] {
				out = append(out, d)
			}
		}
		return out
	}
	a.file.Decls = prune(a.file.Decls)
	a.clearRetainedTypeMemos()
}

// oldMemberBlock is the namespace of the live block holding a declaration of the pristine
// program, by position; "" at top level.
func (r *RetainedAnalysis) oldMemberBlock(d ast.Decl) string {
	key := retainedLineKey(d.Pos())
	for _, sd := range r.decls {
		if retainedLineKey(sd.Decl.Pos()) == key {
			return sd.Namespace
		}
	}
	return ""
}
