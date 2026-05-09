package semantic

func IsInvalidType(t Type) bool {
	_, ok := t.(*InvalidType)
	return ok
}

func IsNeverType(t Type) bool {
	_, ok := t.(*NeverType)
	return ok
}

func IsNullType(t Type) bool {
	_, ok := t.(*NullType)
	return ok
}

func IsOptionalType(t Type) (*OptionalType, bool) {
	opt, ok := t.(*OptionalType)
	return opt, ok
}

func UnwrapOptionalType(t Type) (Type, bool) {
	opt, ok := t.(*OptionalType)
	if !ok || opt == nil || opt.Value == nil {
		return t, false
	}
	return opt.Value, true
}

func (t *ErrorSetType) HasTag(name string) bool {
	if t == nil {
		return false
	}
	for _, tag := range t.Tags {
		if tag == name {
			return true
		}
	}
	return false
}

func (t *ErrorSetType) HasQualifiedTag(setName string, tagName string) bool {
	return t.HasTag(QualifyErrorTag(setName, tagName))
}

func (t *ErrorSetType) TagCode(name string) (uint32, bool) {
	if t == nil {
		return 0, false
	}
	for i, tag := range t.Tags {
		if tag == name {
			return uint32(i + 1), true
		}
	}
	return 0, false
}

func (t *ErrorSetType) TagCodeFor(setName string, tagName string) (uint32, bool) {
	return t.TagCode(QualifyErrorTag(setName, tagName))
}

func (t *ErrorSetType) PayloadForTag(name string) []Type {
	if t == nil || t.Payloads == nil {
		return nil
	}
	return t.Payloads[name]
}

func (t *ErrorSetType) HasPayloads() bool {
	if t == nil {
		return false
	}
	for _, tag := range t.Tags {
		if len(t.PayloadForTag(tag)) != 0 {
			return true
		}
	}
	return false
}

func MatchErrorTag(dst *ErrorSetType, srcTag string) (string, bool) {
	if dst == nil {
		return "", false
	}
	if dst.HasTag(srcTag) {
		return srcTag, true
	}
	shortName := ErrorTagShortName(srcTag)
	match := ""
	for _, candidate := range dst.Tags {
		if ErrorTagShortName(candidate) != shortName {
			continue
		}
		if match != "" {
			return "", false
		}
		match = candidate
	}
	if match == "" {
		return "", false
	}
	return match, true
}

func ErrorSetAssignable(dst, src *ErrorSetType) bool {
	if dst == nil || src == nil {
		return dst == src
	}
	for _, tag := range src.Tags {
		if _, ok := MatchErrorTag(dst, tag); !ok {
			return false
		}
	}
	return true
}

func IsBoolType(t Type) bool {
	b, ok := t.(*BuiltinType)
	return ok && b.Name == "bool"
}

func IsConstEnumType(t Type) (*ConstEnumType, bool) {
	ce, ok := t.(*ConstEnumType)
	return ce, ok
}

func ConstEnumStorageType(t Type) (Type, bool) {
	ce, ok := t.(*ConstEnumType)
	if !ok || ce == nil || ce.Storage == nil {
		return nil, false
	}
	return ce.Storage, true
}

func IsNumericType(t Type) bool {
	if _, _, ok := BitIntInfo(t); ok {
		return true
	}
	b, ok := t.(*BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		return true
	default:
		return false
	}
}

func IsFloatType(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "f32", "f64":
		return true
	default:
		return false
	}
}

func IsIntegralType(t Type) bool {
	if _, _, ok := BitIntInfo(t); ok {
		return true
	}
	switch b := t.(type) {
	case *BuiltinType:
		switch b.Name {
		case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func IsIntegralStorageType(t Type) bool {
	return IsIntegralType(t)
}

func IsTypeParamType(t Type) (*TypeParamType, bool) {
	tp, ok := t.(*TypeParamType)
	return tp, ok
}

func IsRefType(t Type) (*RefType, bool) {
	r, ok := t.(*RefType)
	return r, ok
}
