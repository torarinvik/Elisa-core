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

// retainedTypeMemos are analyzer memos keyed by type whose answers depend on struct fields.
var retainedTypeMemos = []string{
	"storeScanNoStorageCache", "storeScanViewOnlyCache", "storeScanSharesCache", "resolvedStructFieldsMemo",
	"affineHandleMemo", "typeSeenScratch", "borrowedOwnerRefMemo", "regionRefsMemo",
	"mutableBorrowCapabilityMemo", "containerRegionMemo", "storeScanAddressMemo", "storeScanAddressSeen",
}

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
	seen := map[string]bool{}
	for _, e := range edits {
		// The declaration walk can list a declaration twice; it is one edit.
		key := retainedLineKey(e[0].Pos()) + "|" + e[0].Name
		if seen[key] {
			continue
		}
		seen[key] = true
		sd, ok := byKey[key]
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
func retainedTypeReaches(t Type, targets map[Type]bool, memo map[Type]bool) bool {
	if t == nil {
		return false
	}
	if hit, ok := memo[t]; ok {
		return hit
	}
	memo[t] = false
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
				return targets[n]
			case *EnumType:
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
