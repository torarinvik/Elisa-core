package semantic

func FlagsInstanceType(t Type) (*ConstEnumType, bool) {
	t = StripAggregateStateType(t)
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = StripAggregateStateType(ref.Elem)
	}
	instance, ok := t.(*GenericInstanceType)
	if !ok || instance == nil || instance.Name != "Flags" || len(instance.Args) != 1 {
		return nil, false
	}
	flagType, ok := StripAggregateStateType(instance.Args[0]).(*ConstEnumType)
	return flagType, ok && flagType != nil
}
