package predicatetypes

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

func TypingMatchesNodes(nodes []ast.Expr, types string) bool {
	if len(nodes) != len(types) {
		return false
	}
	ranges := false
	for i, node := range nodes {
		_, rangeNode := node.(*ast.MembershipRangeExpr)
		if rangeNode != (types[i] == RangeTypeTag) {
			return false
		}
		ranges = ranges || rangeNode
	}
	if ranges {
		return ValidRangeTyping(types)
	}
	return ValidTyping(types)
}

// Cold analysis wraps direct ranges in a one-element candidate list. Both
// spellings have the same source protocol; retain the current wrapper, never a
// prior invocation's AST or ArrayType. Other list shapes remain unsupported.
func MembershipRange(expr ast.Expr) (*ast.MembershipRangeExpr, *ast.ListLitExpr, bool) {
	switch node := expr.(type) {
	case *ast.MembershipRangeExpr:
		return node, nil, node != nil
	case *ast.ListLitExpr:
		if node == nil || len(node.Elems) != 1 {
			return nil, nil, false
		}
		copy := *node
		copy.Position = lexer.Pos{}
		copy.Elems = nil
		if !reflect.ValueOf(copy).IsZero() {
			return nil, nil, false
		}
		rangeNode, ok := node.Elems[0].(*ast.MembershipRangeExpr)
		return rangeNode, node, ok && rangeNode != nil
	}
	return nil, nil, false
}

// Predicate admission is deliberately separate from integer-local planning.
// Only canonical scalar syntax is accepted; current semantic bindings and
// operator candidates must still be validated at the replay boundary.
func BuiltinSyntaxName(input ast.TypeExpr) string {
	var name string
	switch typ := input.(type) {
	case *ast.NamedType:
		if typ == nil {
			return ""
		}
		copy := *typ
		name = copy.Name
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if !reflect.ValueOf(copy).IsZero() {
			return ""
		}
	case *ast.BuiltinTypeExpr:
		if typ == nil {
			return ""
		}
		copy := *typ
		name = copy.Name
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if len(copy.TypeArgs) != 0 || len(copy.ValueArgs) != 0 {
			return ""
		}
		copy.TypeArgs = nil
		copy.ValueArgs = nil
		if !reflect.ValueOf(copy).IsZero() {
			return ""
		}
	default:
		return ""
	}
	switch name {
	case "bool", "char", "int", "i64", "i8", "i16", "i32", "u8", "u16", "u32", "u64", "isize", "usize", "uintptr":
		return name
	}
	return ""
}
