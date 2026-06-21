package ast

// CloneExpr returns a deep copy of expr with fresh node identities, preserving source positions and
// all syntactic fields. It clones only the parser-produced (pre-analysis) shape: nodes whose meaning
// is fully captured by their syntactic fields. Nodes outside that set return nil, which callers treat
// as "not clonable" and handle by bailing out of whatever rewrite they were attempting.
//
// It exists so a desugaring can splice the same sub-expression into several positions without sharing
// a node pointer (which would let later annotation/affine passes see one node in multiple scopes). The
// fold-reduction lowering uses it to copy a per-element transform across the unrolled reduction lanes.
func CloneExpr(expr Expr) Expr {
	switch n := expr.(type) {
	case nil:
		return nil
	case *Ident:
		return &Ident{Position: n.Position, Name: n.Name}
	case *IntLit:
		return &IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	case *FloatLit:
		return &FloatLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix}
	case *StringLit:
		return &StringLit{Position: n.Position, Value: n.Value}
	case *CharLit:
		return &CharLit{Position: n.Position, Value: n.Value}
	case *BoolLit:
		return &BoolLit{Position: n.Position, Value: n.Value}
	case *NullLit:
		return &NullLit{Position: n.Position}
	case *ZeroedLit:
		return &ZeroedLit{Position: n.Position}
	case *ParenExpr:
		inner := CloneExpr(n.Inner)
		if n.Inner != nil && inner == nil {
			return nil
		}
		return &ParenExpr{Position: n.Position, Inner: inner}
	case *UnaryExpr:
		operand := CloneExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}
	case *BinaryExpr:
		left := CloneExpr(n.Left)
		right := CloneExpr(n.Right)
		if (n.Left != nil && left == nil) || (n.Right != nil && right == nil) {
			return nil
		}
		return &BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}
	case *MoveExpr:
		operand := CloneExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &MoveExpr{Position: n.Position, Operand: operand}
	case *FieldExpr:
		object := CloneExpr(n.Object)
		if n.Object != nil && object == nil {
			return nil
		}
		return &FieldExpr{Position: n.Position, Object: object, Field: n.Field, Safe: n.Safe}
	case *ShorthandMemberExpr:
		return &ShorthandMemberExpr{Position: n.Position, Parts: append([]string(nil), n.Parts...)}
	case *IndexExpr:
		object := CloneExpr(n.Object)
		index := CloneExpr(n.Index)
		fallback := CloneExpr(n.Fallback)
		if (n.Object != nil && object == nil) || (n.Index != nil && index == nil) || (n.Fallback != nil && fallback == nil) {
			return nil
		}
		return &IndexExpr{Position: n.Position, Object: object, Index: index, Fallback: fallback, LegacyElseFallback: n.LegacyElseFallback}
	case *SliceExpr:
		object := CloneExpr(n.Object)
		start := CloneExpr(n.Start)
		end := CloneExpr(n.End)
		if (n.Object != nil && object == nil) || (n.Start != nil && start == nil) || (n.End != nil && end == nil) {
			return nil
		}
		return &SliceExpr{Position: n.Position, Object: object, Start: start, End: end}
	case *ListLitExpr:
		elems := cloneExprs(n.Elems)
		owner := CloneExpr(n.Owner)
		if (len(n.Elems) != 0 && elems == nil) || (n.Owner != nil && owner == nil) {
			return nil
		}
		return &ListLitExpr{Position: n.Position, Elems: elems, Spreads: append([]bool(nil), n.Spreads...), Brace: n.Brace, Owner: owner}
	case *CallExpr:
		fn := CloneExpr(n.Func)
		safeReceiver := CloneExpr(n.SafeReceiver)
		args := cloneExprs(n.Args)
		if (n.Func != nil && fn == nil) || (n.SafeReceiver != nil && safeReceiver == nil) || (len(n.Args) != 0 && args == nil) {
			return nil
		}
		var argItems []CallArgItem
		if len(n.ArgItemOrder) != 0 {
			argItems = make([]CallArgItem, 0, len(n.ArgItemOrder))
			for _, item := range n.ArgItemOrder {
				argItems = append(argItems, CallArgItem{Position: item.Position, ArgIndex: item.ArgIndex})
			}
		}
		return &CallExpr{
			Position:      n.Position,
			Func:          fn,
			SafeReceiver:  safeReceiver,
			HasArgForward: n.HasArgForward,
			ArgForwardPos: n.ArgForwardPos,
			Args:          args,
			ArgNames:      append([]string(nil), n.ArgNames...),
			ArgShorthand:  append([]bool(nil), n.ArgShorthand...),
			ArgItemOrder:  argItems,
			Safe:          n.Safe,
		}
	case *CastExpr:
		operand := CloneExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &CastExpr{Position: n.Position, Operand: operand, Target: n.Target, Origin: n.Origin}
	case *SizeofExpr:
		return &SizeofExpr{Position: n.Position, Type: n.Type}
	case *AlignofExpr:
		return &AlignofExpr{Position: n.Position, Type: n.Type}
	case *OffsetofExpr:
		return &OffsetofExpr{Position: n.Position, Type: n.Type, Field: n.Field}
	case *TernaryExpr:
		value := CloneExpr(n.Value)
		cond := CloneExpr(n.Cond)
		alt := CloneExpr(n.Alt)
		if (n.Value != nil && value == nil) || (n.Cond != nil && cond == nil) || (n.Alt != nil && alt == nil) {
			return nil
		}
		return &TernaryExpr{Position: n.Position, Value: value, Cond: cond, Alt: alt}
	case *AddrOfExpr:
		operand := CloneExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &AddrOfExpr{Position: n.Position, Operand: operand}
	case *SpecializeExpr:
		operand := CloneExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &SpecializeExpr{Position: n.Position, Operand: operand, TypeArgs: append([]TypeExpr(nil), n.TypeArgs...)}
	case *TupleExpr:
		elems := cloneExprs(n.Elems)
		if len(n.Elems) != 0 && elems == nil {
			return nil
		}
		return &TupleExpr{Position: n.Position, Elems: elems}
	case *TypeExprExpr:
		return &TypeExprExpr{Position: n.Position, Type: n.Type}
	case *QuantifierExpr:
		in := CloneExpr(n.In)
		if n.In != nil && in == nil {
			return nil
		}
		body := CloneExpr(n.Body)
		if n.Body != nil && body == nil {
			return nil
		}
		return &QuantifierExpr{Position: n.Position, Exists: n.Exists, Vars: append([]string(nil), n.Vars...), In: in, Body: body}
	default:
		return nil
	}
}

// CloneExprSubst is CloneExpr with simultaneous capture-free substitution: any *Ident whose name is a
// key in `subst` is replaced by a fresh deep clone of the bound replacement expression. It is used to
// instantiate a law body for a concrete subject (`self` -> EXPR, params -> bracket args) so the body
// can be recorded as an SMT flow fact. Returns nil under the same "not clonable" contract as CloneExpr;
// substitution replacements that are themselves not clonable also yield nil (the caller bails out).
func CloneExprSubst(expr Expr, subst map[string]Expr) Expr {
	if len(subst) == 0 {
		return CloneExpr(expr)
	}
	if id, ok := expr.(*Ident); ok && id != nil {
		if repl, ok := subst[id.Name]; ok {
			return CloneExpr(repl)
		}
		return &Ident{Position: id.Position, Name: id.Name}
	}
	switch n := expr.(type) {
	case nil:
		return nil
	case *ParenExpr:
		inner := CloneExprSubst(n.Inner, subst)
		if n.Inner != nil && inner == nil {
			return nil
		}
		return &ParenExpr{Position: n.Position, Inner: inner}
	case *UnaryExpr:
		operand := CloneExprSubst(n.Operand, subst)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}
	case *BinaryExpr:
		left := CloneExprSubst(n.Left, subst)
		right := CloneExprSubst(n.Right, subst)
		if (n.Left != nil && left == nil) || (n.Right != nil && right == nil) {
			return nil
		}
		return &BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}
	case *FieldExpr:
		object := CloneExprSubst(n.Object, subst)
		if n.Object != nil && object == nil {
			return nil
		}
		return &FieldExpr{Position: n.Position, Object: object, Field: n.Field, Safe: n.Safe}
	case *IndexExpr:
		object := CloneExprSubst(n.Object, subst)
		index := CloneExprSubst(n.Index, subst)
		fallback := CloneExprSubst(n.Fallback, subst)
		if (n.Object != nil && object == nil) || (n.Index != nil && index == nil) || (n.Fallback != nil && fallback == nil) {
			return nil
		}
		return &IndexExpr{Position: n.Position, Object: object, Index: index, Fallback: fallback, LegacyElseFallback: n.LegacyElseFallback}
	case *TernaryExpr:
		value := CloneExprSubst(n.Value, subst)
		cond := CloneExprSubst(n.Cond, subst)
		alt := CloneExprSubst(n.Alt, subst)
		if (n.Value != nil && value == nil) || (n.Cond != nil && cond == nil) || (n.Alt != nil && alt == nil) {
			return nil
		}
		return &TernaryExpr{Position: n.Position, Value: value, Cond: cond, Alt: alt}
	case *CallExpr:
		fn := CloneExprSubst(n.Func, subst)
		if n.Func != nil && fn == nil {
			return nil
		}
		args := make([]Expr, 0, len(n.Args))
		for _, arg := range n.Args {
			next := CloneExprSubst(arg, subst)
			if arg != nil && next == nil {
				return nil
			}
			args = append(args, next)
		}
		return &CallExpr{Position: n.Position, Func: fn, Args: args, ArgNames: append([]string(nil), n.ArgNames...)}
	default:
		// Non-substituting fallback for the remaining literal/leaf shapes (no idents to replace).
		return CloneExpr(expr)
	}
}

// ExprContainsCall reports whether an expression contains a function/method call. It gates the
// AutovecExpected marker (and thus the -Wperf un-vectorized warning): a call in a comprehension/fold
// body legitimately blocks auto-vectorization (the callee may not inline), so flagging it would be
// noise. Unknown node types are treated conservatively as "contains a call" so the marker is only set
// for clearly-simple, call-free bodies — false negatives, never false positives.
func ExprContainsCall(e Expr) bool {
	switch n := e.(type) {
	case nil:
		return false
	case *IntLit, *FloatLit, *BoolLit, *StringLit, *CharLit, *Ident:
		return false
	case *ParenExpr:
		return ExprContainsCall(n.Inner)
	case *BinaryExpr:
		return ExprContainsCall(n.Left) || ExprContainsCall(n.Right)
	case *UnaryExpr:
		return ExprContainsCall(n.Operand)
	case *TernaryExpr:
		return ExprContainsCall(n.Value) || ExprContainsCall(n.Cond) || ExprContainsCall(n.Alt)
	case *IndexExpr:
		return ExprContainsCall(n.Object) || ExprContainsCall(n.Index)
	case *FieldExpr:
		return ExprContainsCall(n.Object)
	case *CastExpr:
		return ExprContainsCall(n.Operand)
	case *CallExpr:
		return true
	default:
		return true
	}
}

func cloneExprs(exprs []Expr) []Expr {
	if len(exprs) == 0 {
		return nil
	}
	cloned := make([]Expr, 0, len(exprs))
	for _, expr := range exprs {
		next := CloneExpr(expr)
		if expr != nil && next == nil {
			return nil
		}
		cloned = append(cloned, next)
	}
	return cloned
}
