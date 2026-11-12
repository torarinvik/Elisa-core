package semantic

import "elisacore/src/ast"

// scanPackedStoreBody collects construction roots and direct callees in one
// traversal. If autoRegionRoots is supplied, it also records the packed
// allocations that use the implicit region. The walker visits shared argument
// nodes only once.
func (a *Analyzer) scanPackedStoreBody(fn *ast.FuncDecl, callees map[*FuncType]bool, bareVariantNames, autoRegionRoots map[string]bool) (constructors map[string]*EnumType, bareVariants map[string]bool) {
	walkStoreScanNodes(fn.Body, func(node any) {
		var constructed *EnumType
		switch n := node.(type) {
		case *ast.AllocExpr:
			if n != nil {
				if enumType, _, ok := a.packedAllocConstructorInfo(n.Value); ok && enumType != nil && enumType.Packed {
					constructed = enumType.Root()
					if autoRegionRoots != nil && n.AutoRegion {
						autoRegionRoots[constructed.Name] = true
					}
				}
			}
		case *ast.CallExpr:
			if n != nil {
				if enumType, ok := a.regionBackedEnumConstructor(n); ok && enumType != nil {
					constructed = enumType.Root()
				}
				// Generic method dispatch uses the caller's protocol bounds.
				// Keep that context local to resolving call-graph edges.
				previous := a.regionPolyFn
				a.regionPolyFn = fn
				a.recordPackedStoreCallees(n, callees)
				a.regionPolyFn = previous
			}
		case *ast.FieldExpr:
			if n != nil && (bareVariantNames == nil || bareVariantNames[n.Field]) {
				if root := a.barePackedVariantRoot(n); root != nil {
					if bareVariants == nil {
						bareVariants = make(map[string]bool)
					}
					bareVariants[root.Name] = true
				}
			}
		}
		if constructed != nil {
			if constructors == nil {
				constructors = make(map[string]*EnumType)
			}
			constructors[constructed.Name] = constructed
		}
	})
	return constructors, bareVariants
}

// Only named packed enums can resolve a bare constructor. Their variant
// spellings are fixed for the duration of the pre-body store analysis.
func (a *Analyzer) packedStoreBareVariantNames() map[string]bool {
	if a.lookupTrace != nil {
		return nil // preserve recorded type lookups, including misses
	}
	names := map[string]bool{}
	for _, typ := range a.namedTypes {
		enum, ok := typ.(*EnumType)
		if !ok || enum == nil || !enum.Packed {
			continue
		}
		for name, variant := range enum.VariantMap {
			if variant != nil && len(variant.Payload) == 0 {
				names[name] = true
			}
		}
	}
	return names
}

// A payload-less packed variant used as a value allocates a fresh node.
func (a *Analyzer) barePackedVariantRoot(field *ast.FieldExpr) *EnumType {
	enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(field)
	if ok && enumType != nil && enumType.Packed && variant != nil && len(variant.Payload) == 0 {
		return enumType.Root()
	}
	return nil
}

func (a *Analyzer) recordPackedStoreCallees(call *ast.CallExpr, out map[*FuncType]bool) {
	if ft := a.regionPolyCalleeFuncType(call); ft != nil {
		out[ft] = true
	}
	field, ok := unwrapParenForRegionPoly(call.Func).(*ast.FieldExpr)
	if !ok {
		return
	}
	for _, ft := range a.regionPolyProtocolMethodImplFuncTypes(field) {
		out[ft] = true
	}
	// Receiver types are unavailable in this pre-pass. Conservatively include
	// each visible extension method with the same name, just as for protocols.
	for _, candidate := range a.visibleNameCandidates(field.Field) {
		for _, method := range a.extensionMethodsByName[candidate] {
			if method != nil && method.Symbol != nil {
				if ft, ok := method.Symbol.Type.(*FuncType); ok && ft != nil {
					out[ft] = true
				}
			}
		}
	}
}
