package semantic

import (
	"elisacore/src/ast"
	"maps"
	"reflect"
	"sync"
)

// The typed fast path is valid only for the exact inline ownership schema below.
// Future fields or field-type changes fall back to the reflection clone.
type specializationCloneField struct {
	name string
	typ  reflect.Type
}

func specializationCloneSchemaMatches(t reflect.Type, expected []specializationCloneField) bool {
	if t.Kind() != reflect.Struct || t.NumField() != len(expected) {
		return false
	}
	for i, f := range expected {
		field := t.Field(i)
		if field.Name != f.name || field.Type != f.typ {
			return false
		}
	}
	return true
}

var specializationRegionSchema = sync.OnceValue(func() bool {
	shape := func(t reflect.Type, fields ...specializationCloneField) bool {
		return specializationCloneSchemaMatches(t, fields)
	}
	f := func(name string, v any) specializationCloneField {
		return specializationCloneField{name, reflect.TypeOf(v)}
	}
	return shape(reflect.TypeFor[regionRefState](),
		f("Deps", map[*Symbol]regionDependencyState(nil)), f("StoreDeps", map[*Symbol]packedStoreDependencyState(nil)),
		f("DirectParamDep", int(0)), f("HasDirectParamDep", false), f("ParamDeps", IntBitSet{}),
		f("Fields", map[string]regionRefState(nil)), f("PackedStoreSummary", PackedStoreProvenance{}),
		f("PackedStoreSummaryKnown", false), f("ParamOnlySummary", false)) &&
		shape(reflect.TypeFor[IntBitSet](), f("inline", uint64(0)), f("extra", []uint64(nil))) &&
		shape(reflect.TypeFor[regionDependencyState](), f("Generation", int(0)), f("Valid", false), f("InvalidatedBy", "")) &&
		shape(reflect.TypeFor[packedStoreDependencyState](), f("Type", (*PackedEnumStoreType)(nil))) &&
		shape(reflect.TypeFor[PackedStoreProvenance](), f("HasPackedStoreDeps", false), f("HasFrozenPackedStoreDeps", false), f("HasNonFrozenPackedStoreDeps", false), f("HasNonStoreProvenance", false))
})

// Copy inline maps, preserve pointer identities and slice
// backing with restricted capacity, as the reflection clone does. The active
// path tracks cycles; sibling aliases remain separate as in the current clone.
func cloneSpecializationReturnElementSummaries(src map[*ast.FuncDecl]regionRefState) map[*ast.FuncDecl]regionRefState {
	return cloneSpecializationReturnElementSummariesGuarded(src, specializationRegionSchema())
}

func cloneSpecializationReturnElementSummariesGuarded(src map[*ast.FuncDecl]regionRefState, allowed bool) map[*ast.FuncDecl]regionRefState {
	if !allowed {
		return deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]regionRefState)
	}
	if src == nil {
		return nil
	}
	dst := make(map[*ast.FuncDecl]regionRefState, len(src))
	for key, value := range src {
		dst[key] = cloneSpecializationRegionRef(value, nil)
	}
	return dst
}
func cloneSpecializationRegionRef(src regionRefState, active map[uintptr]map[string]regionRefState) regionRefState {
	dst := src
	dst.Deps = maps.Clone(src.Deps)
	dst.StoreDeps = maps.Clone(src.StoreDeps)
	if src.ParamDeps.extra != nil {
		dst.ParamDeps.extra = src.ParamDeps.extra[:len(src.ParamDeps.extra):len(src.ParamDeps.extra)]
	}
	if src.Fields != nil {
		id := reflect.ValueOf(src.Fields).Pointer()
		if prior, ok := active[id]; ok {
			dst.Fields = prior
			return dst
		}
		dst.Fields = make(map[string]regionRefState, len(src.Fields))
		if active == nil {
			active = make(map[uintptr]map[string]regionRefState)
		}
		active[id] = dst.Fields
		for key, value := range src.Fields {
			dst.Fields[key] = cloneSpecializationRegionRef(value, active)
		}
		delete(active, id)
	}
	return dst
}
