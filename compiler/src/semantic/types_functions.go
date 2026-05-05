package semantic

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
)

func RequestedAlignment(t Type) (int, bool) {
	if t == nil {
		return 0, false
	}
	t = StripAggregateStateType(t)
	switch tt := t.(type) {
	case *StructType:
		if tt != nil && tt.HasAlignment {
			return tt.Alignment, true
		}
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if ok && base != nil && base.HasAlignment {
			return base.Alignment, true
		}
	}
	return 0, false
}

func (t *GenericInstanceType) String() string {
	parts := make([]string, 0, len(t.Args))
	for _, arg := range t.Args {
		parts = append(parts, arg.String())
	}
	return fmt.Sprintf("%s[%s]", t.Name, strings.Join(parts, ", "))
}
func (t *AggregateStateType) String() string {
	if t == nil || t.Base == nil {
		return "<invalid-aggregate-state>"
	}
	states := aggregateStateStates(t)
	parts := make([]string, 0, len(states))
	for _, state := range states {
		parts = append(parts, ast.RefStateMarker(ast.RefState(state)))
	}
	return fmt.Sprintf("%s[%s]", t.Base.String(), strings.Join(parts, ", "))
}

func funcTypeExplicitParamCount(t *FuncType) int {
	if t == nil {
		return 0
	}
	if t.ExplicitParamCount != 0 || len(t.ExplicitParamNames) != 0 || len(t.ImplicitParamNames) != 0 {
		return t.ExplicitParamCount
	}
	return len(t.Params)
}

func funcTypeExplicitParamHasDefault(t *FuncType, index int) bool {
	if t == nil || index < 0 || index >= funcTypeExplicitParamCount(t) {
		return false
	}
	if index >= len(t.ExplicitParamHasDefault) {
		return false
	}
	return t.ExplicitParamHasDefault[index]
}

func funcTypeExplicitParamDefaultExpr(t *FuncType, index int) ast.Expr {
	if !funcTypeExplicitParamHasDefault(t, index) {
		return nil
	}
	if index >= len(t.ExplicitParamDefaultExprs) {
		return nil
	}
	return t.ExplicitParamDefaultExprs[index]
}

func funcTypeHasAnyExplicitDefault(t *FuncType) bool {
	if t == nil {
		return false
	}
	for i := 0; i < funcTypeExplicitParamCount(t); i++ {
		if funcTypeExplicitParamHasDefault(t, i) {
			return true
		}
	}
	return false
}

func funcTypeImplicitParamTypes(t *FuncType) []Type {
	if t == nil {
		return nil
	}
	explicitCount := funcTypeExplicitParamCount(t)
	if explicitCount < 0 {
		explicitCount = 0
	}
	if explicitCount > len(t.Params) {
		explicitCount = len(t.Params)
	}
	return t.Params[explicitCount:]
}

func (t *FuncType) String() string {
	explicitCount := funcTypeExplicitParamCount(t)
	if explicitCount > len(t.Params) {
		explicitCount = len(t.Params)
	}
	parts := make([]string, 0, explicitCount)
	for _, p := range t.Params[:explicitCount] {
		parts = append(parts, p.String())
	}
	implicitParts := make([]string, 0, len(t.ImplicitParamNames))
	for i, name := range t.ImplicitParamNames {
		index := explicitCount + i
		if index >= len(t.Params) || t.Params[index] == nil {
			implicitParts = append(implicitParts, name+": <invalid>")
			continue
		}
		implicitParts = append(implicitParts, name+": "+t.Params[index].String())
	}
	generics := make([]string, 0, len(t.GenericParams)+len(t.RegionParams)+len(t.PermissionParams))
	if len(t.GenericParams) != 0 {
		for _, param := range t.GenericParams {
			switch param.Kind {
			case ast.GenericParamRefStorage:
				generics = append(generics, "refstorage "+param.Name)
			case ast.GenericParamRefState:
				generics = append(generics, "refstate "+param.Name)
			default:
				if param.InterfaceBound != "" {
					generics = append(generics, param.Name+": "+param.InterfaceBound)
				} else {
					generics = append(generics, param.Name)
				}
			}
		}
	} else {
		for _, param := range t.TypeParams {
			generics = append(generics, param)
		}
		for _, param := range t.RefStorageParams {
			generics = append(generics, "refstorage "+param)
		}
		for _, param := range t.RefStateParams {
			generics = append(generics, "refstate "+param)
		}
	}
	for _, param := range t.RegionParams {
		generics = append(generics, "region "+param)
	}
	for _, param := range t.PermissionParams {
		generics = append(generics, "permission "+param)
	}
	prefix := ""
	if len(generics) > 0 {
		prefix = "[" + strings.Join(generics, ", ") + "]"
	}
	if t.Variadic {
		parts = append(parts, "...")
	}
	withClause := ""
	if len(implicitParts) != 0 {
		withClause = " with " + strings.Join(implicitParts, ", ")
	}
	if t.Return == nil {
		return fmt.Sprintf("func%s(%s)%s%s", prefix, strings.Join(parts, ", "), withClause, permissionFamiliesString(t.Permissions))
	}
	return fmt.Sprintf("func%s(%s)%s -> %s%s", prefix, strings.Join(parts, ", "), withClause, t.Return.String(), permissionFamiliesString(t.Permissions))
}
