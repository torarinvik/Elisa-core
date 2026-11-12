package parserdesugar

import "elisacore/src/ast"

type SlotClass int

const (
	SlotValue SlotClass = iota
	SlotThread
	SlotNeutral
)

// classifySlot: a mutating call rooted at the target THREADS; the bare target binding
// is NEUTRAL (thread-erase ≡ self-assign); anything else is a VALUE.
func ClassifySlot(e ast.Expr, name string) SlotClass {
	if id, ok := e.(*ast.Ident); ok && id.Name == name {
		return SlotNeutral
	}
	if call, ok := e.(*ast.CallExpr); ok {
		if root, ok := ExprRootIdentName(call); ok && root == name {
			return SlotThread
		}
	}
	return SlotValue
}

func ClassifySingleLeaves(value ast.Expr, name string) (threads, values int, enumerable bool) {
	switch n := value.(type) {
	case *ast.TernaryExpr:
		t1, v1, ok1 := ClassifySingleLeaves(n.Value, name)
		t2, v2, ok2 := ClassifySingleLeaves(n.Alt, name)
		return t1 + t2, v1 + v2, ok1 && ok2
	case *ast.ExprBlock:
		if n.Value == nil {
			return 0, 0, false
		}
		return ClassifySingleLeaves(n.Value, name)
	default:
		switch ClassifySlot(value, name) {
		case SlotThread:
			return 1, 0, true
		case SlotNeutral:
			return 0, 0, true
		default:
			return 0, 1, true
		}
	}
}
