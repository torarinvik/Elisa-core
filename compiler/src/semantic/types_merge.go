package semantic

func MergeTypes(a, b Type) Type {
	if IsNeverType(a) {
		return b
	}
	if IsNeverType(b) {
		return a
	}
	if SameType(a, b) {
		return a
	}
	if au, ok := a.(*ErrorUnionType); ok {
		if bu, ok := b.(*ErrorUnionType); ok {
			merged := MergeTypes(au.Value, bu.Value)
			if IsInvalidType(merged) {
				return invalidType
			}
			switch {
			case ErrorSetAssignable(au.Errors, bu.Errors):
				return &ErrorUnionType{Value: merged, Errors: au.Errors}
			case ErrorSetAssignable(bu.Errors, au.Errors):
				return &ErrorUnionType{Value: merged, Errors: bu.Errors}
			}
		}
	}
	if ao, ok := a.(*OptionalType); ok {
		if bo, ok := b.(*OptionalType); ok {
			merged := MergeTypes(ao.Value, bo.Value)
			if IsInvalidType(merged) {
				return invalidType
			}
			return &OptionalType{Value: merged}
		}
		merged := MergeTypes(ao.Value, b)
		if IsInvalidType(merged) {
			return invalidType
		}
		return &OptionalType{Value: merged}
	}
	if bo, ok := b.(*OptionalType); ok {
		merged := MergeTypes(a, bo.Value)
		if IsInvalidType(merged) {
			return invalidType
		}
		return &OptionalType{Value: merged}
	}
	if at, ok := a.(*TupleType); ok {
		if bt, ok := b.(*TupleType); ok && len(at.Fields) == len(bt.Fields) {
			fields := make([]TupleField, len(at.Fields))
			for i := range at.Fields {
				merged := MergeTypes(at.Fields[i].Type, bt.Fields[i].Type)
				if IsInvalidType(merged) {
					return invalidType
				}
				name := at.Fields[i].Name
				if name == "" {
					name = bt.Fields[i].Name
				}
				fields[i] = TupleField{Name: name, Type: merged}
			}
			return &TupleType{Fields: fields}
		}
	}
	if _, ok := a.(*StructStateCaseType); ok {
		return mergeNamedStateTypes(a, b, nil)
	}
	if aset, ok := a.(*StructStateSetType); ok {
		return mergeNamedStateTypes(a, b, aset.Cases)
	}
	if _, ok := b.(*StructStateCaseType); ok {
		return mergeNamedStateTypes(a, b, nil)
	}
	if bset, ok := b.(*StructStateSetType); ok {
		return mergeNamedStateTypes(a, b, bset.Cases)
	}
	if aa, ok := a.(*AggregateStateType); ok {
		if ba, ok := b.(*AggregateStateType); ok && SameType(aa.Base, ba.Base) {
			if states, ok := mergeAggregateStateLists(aggregateStateStates(aa), aggregateStateStates(ba)); ok {
				return cloneAggregateStateWithBase(aa.Base, states)
			}
		}
	}
	if agi, ok := a.(*GenericInstanceType); ok {
		if bgi, ok := b.(*GenericInstanceType); ok && agi.Name == bgi.Name && SameType(agi.Base, bgi.Base) && len(agi.Args) == len(bgi.Args) {
			base, ok := agi.Base.(*StructType)
			if ok && base != nil {
				stateIndex := namedStateArgIndex(base)
				if stateIndex >= 0 {
					mergedArgs := make([]Type, len(agi.Args))
					for i := range agi.Args {
						if i == stateIndex {
							merged := mergeNamedStateTypes(agi.Args[i], bgi.Args[i], base.NamedStateCases)
							if IsInvalidType(merged) {
								return invalidType
							}
							mergedArgs[i] = merged
							continue
						}
						if !SameType(agi.Args[i], bgi.Args[i]) {
							return invalidType
						}
						mergedArgs[i] = agi.Args[i]
					}
					return &GenericInstanceType{Name: agi.Name, Base: agi.Base, Args: mergedArgs}
				}
			}
		}
	}
	if IsNumericType(a) && IsNumericType(b) {
		return CommonNumericType(a, b)
	}
	if ar, ok := a.(*RefType); ok {
		if br, ok := b.(*RefType); ok && SameType(ar.Elem, br.Elem) {
			mutable := ar.Mutable && br.Mutable
			if ar.StateParam != br.StateParam || ar.StorageParam != br.StorageParam {
				return invalidType
			}
			storage, explicit, okStorage := mergeRefStorages(ar.Storage, br.Storage, ar.ExplicitStorage, br.ExplicitStorage)
			if !okStorage {
				return invalidType
			}
			region, okRegion := mergeRefRegions(ar.Region, br.Region)
			if !okRegion {
				return invalidType
			}
			if state, ok := mergeRefStates(ar.State, br.State); ok {
				return &RefType{Elem: ar.Elem, Mutable: mutable, State: state, StateParam: ar.StateParam, Storage: storage, StorageParam: ar.StorageParam, Region: region, ExplicitStorage: explicit}
			}
		}
	}
	if av, ok := a.(*ViewType); ok {
		if bv, ok := b.(*ViewType); ok && SameType(av.Elem, bv.Elem) {
			if viewBoundsEqual(av, bv) {
				return av
			}
			return &ViewType{Elem: av.Elem}
		}
	}
	if IsNullType(a) {
		if opt, ok := b.(*OptionalType); ok {
			return opt
		}
		if r, ok := b.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return b
			case RefStateNonNull:
				return cloneRefTypeWithState(r, RefStateNullable)
			}
		}
	}
	if IsNullType(b) {
		if r, ok := a.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return a
			case RefStateNonNull:
				return cloneRefTypeWithState(r, RefStateNullable)
			}
		}
	}
	return invalidType
}
