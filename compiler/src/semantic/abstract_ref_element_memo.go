package semantic

// A root reference may reuse its element's complete summary only when the
// reference's cycle marker cannot appear in that element's walk. Reject nested
// references and generic substitution rather than guessing about that boundary.
type abstractRefRestartKey struct {
	element *StructType
	blocked string
}

func (a *Analyzer) abstractRefElementCanRestart(element *StructType, blocked string) bool {
	if a == nil || !a.typeShapesFrozen {
		return false
	}
	key := abstractRefRestartKey{element, blocked}
	if result, known := a.abstractRefRestartMemo[key]; known {
		return result
	}
	if a.parallel != nil {
		if result, known := a.parallel.abstractRefRestartMemoBase[key]; known {
			return result
		}
	}
	visited := map[Type]bool{}
	var walk func(Type) bool
	walk = func(typ Type) bool {
		if typ == nil || visited[typ] {
			return true
		}
		if len(visited) >= 4096 {
			return false
		}
		visited[typ] = true
		switch typ.(type) {
		case *RefType, *GenericInstanceType, *TypeParamType:
			return false
		}
		// The existing cycle walk uses names, so also reject a nominal type whose
		// spelling collides with the reference marker even if it has another identity.
		if typ.String() == blocked {
			return false
		}
		switch t := typ.(type) {
		case *OptionalType:
			return walk(t.Value)
		case *StructType:
			for _, field := range t.Fields {
				if !walk(field.Type) {
					return false
				}
			}
		case *EnumType:
			if !t.Packed {
				for _, variant := range t.Variants {
					for _, payload := range variant.Payload {
						if !walk(payload) {
							return false
						}
					}
				}
			}
		case *ArrayType:
			return walk(t.Elem)
		case *DArrayType:
			return walk(t.Elem)
		case *ViewType:
			return walk(t.Elem)
		case *DictType:
			return walk(t.Key) && walk(t.Value)
		case *SetType:
			return walk(t.Elem)
		}
		return true
	}
	result := walk(element)
	if a.abstractRefRestartMemo == nil {
		a.abstractRefRestartMemo = map[abstractRefRestartKey]bool{}
	}
	a.abstractRefRestartMemo[key] = result
	return result
}
