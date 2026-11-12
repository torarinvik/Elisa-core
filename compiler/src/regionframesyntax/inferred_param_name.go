package regionframesyntax

import (
	"elisacore/src/ast"
	"strconv"
)

func InferredRegionParamName(fn *ast.FuncDecl, parameter string) string {
	base := "__rg_" + parameter
	used := make(map[string]bool, len(fn.TypeParams)+len(fn.RegionParams)+len(fn.PermissionParams)+len(fn.GenericParams))
	for _, name := range fn.TypeParams {
		used[name] = true
	}
	for _, name := range fn.RegionParams {
		used[name] = true
	}
	for _, name := range fn.PermissionParams {
		used[name] = true
	}
	for _, param := range fn.GenericParams {
		used[param.Name] = true
	}
	name := base
	for suffix := 1; used[name]; suffix++ {
		name = base + "_" + strconv.Itoa(suffix)
	}
	return name
}
