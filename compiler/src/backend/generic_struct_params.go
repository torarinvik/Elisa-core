package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// structGenericParams returns a read-only parameter list. Complete structured
// declarations can be borrowed; only legacy/inferred parameter synthesis needs
// a new slice. Callers must not mutate the returned parameters.
func structGenericParams(base *semantic.StructType) []ast.GenericParam {
	if base == nil {
		return nil
	}
	if len(base.GenericParams) != 0 {
		complete := len(base.RegionParams) == 0
		// Small declarations avoid a map; larger ones keep the original
		// linear synthesis path instead of doing a quadratic membership scan.
		if !complete && len(base.GenericParams) <= 64 && len(base.RegionParams) <= 8 {
			complete = true
			for _, region := range base.RegionParams {
				found := false
				for _, param := range base.GenericParams {
					if param.Kind == ast.GenericParamRegion && param.Name == region {
						found = true
						break
					}
				}
				if !found {
					complete = false
					break
				}
			}
		}
		if complete {
			return base.GenericParams
		}
		params := append([]ast.GenericParam(nil), base.GenericParams...)
		seenRegion := map[string]bool{}
		for _, param := range params {
			if param.Kind == ast.GenericParamRegion {
				seenRegion[param.Name] = true
			}
		}
		for _, name := range base.RegionParams {
			if !seenRegion[name] {
				params = append(params, ast.GenericParam{Kind: ast.GenericParamRegion, Name: name})
			}
		}
		return params
	}
	if len(base.TypeParams) == 0 && len(base.RegionParams) == 0 && len(base.NamedStateCases) == 0 {
		return nil
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams)+len(base.RegionParams)+1)
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	for _, name := range base.RegionParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamRegion, Name: name})
	}
	if len(base.NamedStateCases) != 0 {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), base.NamedStateCases...), StateOwner: base.Name})
	}
	return params
}

func genericBindingsForArgs(params []ast.GenericParam, args []semantic.Type) map[string]semantic.Type {
	if len(params) == 0 || len(args) == 0 {
		return nil
	}
	bindings := make(map[string]semantic.Type, len(params))
	for i, param := range params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		bindings[param.Name] = args[i]
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}
