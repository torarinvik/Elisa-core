package semantic

func (t *ConstEnumType) Member(name string) (*ConstEnumMember, bool) {
	if t == nil || t.MemberMap == nil {
		return nil, false
	}
	member, ok := t.MemberMap[name]
	return member, ok
}
func (t *EnumType) String() string   { return t.Name }
func (t *StructType) String() string { return t.Name }
func (t *OpaqueType) String() string { return t.Name }
