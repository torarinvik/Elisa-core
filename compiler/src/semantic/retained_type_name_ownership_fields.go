package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"sync"
)

type resolvedTypeNameOwnershipField struct {
	pathSuffix string
	index      int
	name       string
	published  bool
}

var (
	resolvedTypeNameASTPackage    = reflect.TypeOf((*ast.File)(nil)).Elem().PkgPath()
	resolvedTypeNamePositionType  = reflect.TypeOf(lexer.Pos{})
	resolvedTypeNameASTFieldCache sync.Map
	resolvedTypeNameOwnershipPlan sync.Map
)

var resolvedTypeNameFunctionHeaderFields = sync.OnceValue(func() []resolvedTypeNameOwnershipField {
	typ := reflect.TypeOf(ast.FuncDecl{})
	var plan []resolvedTypeNameOwnershipField
	for i, field := range reflectFields(typ) {
		if field.Name != "Body" && resolvedTypeNameFieldMayContainAST(field.Type) {
			plan = append(plan, resolvedTypeNameOwnershipField{
				index: i, name: field.Name, pathSuffix: "." + field.Name, published: field.IsExported(),
			})
		}
	}
	return plan
})

func resolvedTypeNameOwnershipFields(typ reflect.Type) []resolvedTypeNameOwnershipField {
	if cached, ok := resolvedTypeNameOwnershipPlan.Load(typ); ok {
		return cached.([]resolvedTypeNameOwnershipField)
	}
	var plan []resolvedTypeNameOwnershipField
	if typ.PkgPath() != resolvedTypeNameASTPackage {
		resolvedTypeNameOwnershipPlan.LoadOrStore(typ, plan)
		return nil
	}
	for i, field := range reflectFields(typ) {
		published := field.IsExported()
		if !published || resolvedTypeNameFieldMayContainAST(field.Type) {
			plan = append(plan, resolvedTypeNameOwnershipField{index: i, name: field.Name, pathSuffix: "." + field.Name, published: published})
		}
	}
	actual, _ := resolvedTypeNameOwnershipPlan.LoadOrStore(typ, plan)
	return actual.([]resolvedTypeNameOwnershipField)
}

func resolvedTypeNameFieldMayContainAST(typ reflect.Type) bool {
	if cached, ok := resolvedTypeNameASTFieldCache.Load(typ); ok {
		return cached.(bool)
	}
	seen := map[reflect.Type]bool{}
	var contains func(reflect.Type, int) bool
	contains = func(current reflect.Type, depth int) bool {
		if current == nil || depth > 32 || seen[current] {
			return false
		}
		if current.PkgPath() == resolvedTypeNameASTPackage {
			return true
		}
		seen[current] = true
		switch current.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			return contains(current.Elem(), depth+1)
		case reflect.Map:
			return contains(current.Key(), depth+1) || contains(current.Elem(), depth+1)
		case reflect.Interface:
			return current.PkgPath() == resolvedTypeNameASTPackage
		}
		return false
	}
	result := contains(typ, 0)
	actual, _ := resolvedTypeNameASTFieldCache.LoadOrStore(typ, result)
	return actual.(bool)
}
