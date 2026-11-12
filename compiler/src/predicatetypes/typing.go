package predicatetypes

func TypeTag(name string) byte {
	switch name {
	case "int":
		return 'n'
	case "i64":
		return 'i'
	case "bool":
		return 'b'
	case "char":
		return 'c'
	case "i8":
		return '0'
	case "i16":
		return '1'
	case "i32":
		return '2'
	case "u8":
		return '3'
	case "u16":
		return '4'
	case "u32":
		return '5'
	case "u64":
		return '6'
	case "isize":
		return '7'
	case "usize":
		return '8'
	case "uintptr":
		return '9'
	default:
		return 0
	}
}

func TypeName(tag byte) string {
	switch tag {
	case 'n':
		return "int"
	case 'i':
		return "i64"
	case 'b':
		return "bool"
	case 'c':
		return "char"
	case '0':
		return "i8"
	case '1':
		return "i16"
	case '2':
		return "i32"
	case '3':
		return "u8"
	case '4':
		return "u16"
	case '5':
		return "u32"
	case '6':
		return "u64"
	case '7':
		return "isize"
	case '8':
		return "usize"
	case '9':
		return "uintptr"
	default:
		return ""
	}
}

func ValidTyping(types string) bool {
	if len(types) == 0 || len(types) > 4096 || TypeName(types[0]) != "bool" {
		return false
	}
	for i := range types {
		if TypeName(types[i]) == "" {
			return false
		}
	}
	return true
}

// Range containers have no standalone semantic ExprType. Their children are
// typed scalars; the normalized candidate-list type is rebuilt on current ASTs.
const RangeTypeTag byte = 'R'

func ValidRangeTyping(types string) bool {
	if len(types) == 0 || len(types) > 4096 || types[0] != 'b' {
		return false
	}
	found := false
	for i := range types {
		if types[i] == RangeTypeTag {
			found = true
			continue
		}
		if TypeName(types[i]) == "" {
			return false
		}
	}
	return found
}
