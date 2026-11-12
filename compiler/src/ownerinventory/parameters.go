package ownerinventory

import "elisacore/src/ast"

const parameterLimit = 256
const parameterMapEntryWork = 64
const parameterStepWork = 32
const parameterTypeNameMaxBytes = 8

func parameterTypeTag(typeName string) (byte, bool) {
	switch typeName {
	case "bool":
		return 1, true
	case "char":
		return 2, true
	case "int":
		return 3, true
	case "i64":
		return 4, true
	case "i8":
		return 5, true
	case "i16":
		return 6, true
	case "i32":
		return 7, true
	case "u8":
		return 8, true
	case "u16":
		return 9, true
	case "u32":
		return 10, true
	case "u64":
		return 11, true
	case "isize":
		return 12, true
	case "usize":
		return 13, true
	case "uintptr":
		return 14, true
	default:
		return 0, false
	}
}

// ParameterTags validates one supported signature, constructs
// its compact tags, and charges the shared work budget before each bounded scan
// and name-map operation.
func ParameterTags(fn *ast.FuncDecl, schemaValid bool, sourceBudget int, typeNameOf func(ast.TypeExpr) string, reserveWork func(int) bool) (tags [parameterLimit]byte, supported, ready bool) {
	if fn == nil || !schemaValid || len(fn.Params) > parameterLimit || len(fn.Name) > sourceBudget {
		return tags, false, true
	}
	mapBytes := len(fn.Params) * parameterMapEntryWork
	if reserveWork != nil && !reserveWork(mapBytes) {
		return tags, false, false
	}
	parameterNames := make(map[string]struct{}, len(fn.Params))
	sourceBytes := len(fn.Name)
	maxInt := int(^uint(0) >> 1)
	for i, param := range fn.Params {
		nameBytes := len(param.Name)
		if nameBytes > (maxInt-parameterStepWork-parameterTypeNameMaxBytes-1)/2 {
			return tags, false, false
		}
		workBytes := nameBytes*2 + parameterStepWork + parameterTypeNameMaxBytes + 1
		if reserveWork != nil && !reserveWork(workBytes) {
			return tags, false, false
		}
		if param.Name == "" || param.Mutable || param.DefaultValue != nil {
			return tags, false, true
		}
		typeName := typeNameOf(param.Type)
		if typeName == "" {
			return tags, false, true
		}
		if len(param.Name) > sourceBudget-sourceBytes {
			return tags, false, true
		}
		sourceBytes += len(param.Name)
		if len(typeName) > sourceBudget-sourceBytes {
			return tags, false, true
		}
		sourceBytes += len(typeName)
		if _, exists := parameterNames[param.Name]; exists {
			return tags, false, true
		}
		parameterNames[param.Name] = struct{}{}
		tag, ok := parameterTypeTag(typeName)
		if !ok {
			return tags, false, true
		}
		tags[i] = tag
	}
	return tags, true, true
}
