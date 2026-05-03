package ast

func (n *CallExpr) LoweredArgs() []Expr {
	if n == nil {
		return nil
	}
	explicitArgs := n.Args
	if n.ResolvedArgsValid && n.ResolvedCommonArgs == nil {
		explicitArgs = n.ResolvedArgs
	}
	if !n.ResolvedImplicitArgsValid || len(n.ResolvedImplicitArgs) == 0 {
		return explicitArgs
	}
	out := make([]Expr, 0, len(explicitArgs)+len(n.ResolvedImplicitArgs))
	out = append(out, explicitArgs...)
	out = append(out, n.ResolvedImplicitArgs...)
	return out
}
func (n *StructLitExpr) ArgName(index int) string {
	if n == nil || index < 0 || index >= len(n.ArgNames) {
		return ""
	}
	return n.ArgNames[index]
}
func (n *StructLitExpr) NamedArgCount() int {
	if n == nil {
		return 0
	}
	count := 0
	for _, name := range n.ArgNames {
		if name != "" {
			count++
		}
	}
	return count
}
func (n *StructLitExpr) LoweredArgs() []Expr {
	if n == nil {
		return nil
	}
	if n.ResolvedArgsValid {
		return n.ResolvedArgs
	}
	return n.Args
}
func (n *RecordUpdateExpr) ArgName(index int) string {
	if n == nil || index < 0 || index >= len(n.ArgNames) {
		return ""
	}
	return n.ArgNames[index]
}
func (n *RecordUpdateExpr) NamedArgCount() int {
	if n == nil {
		return 0
	}
	count := 0
	for _, name := range n.ArgNames {
		if name != "" {
			count++
		}
	}
	return count
}
func (n *RecordUpdateExpr) LoweredArgs() []Expr {
	if n == nil {
		return nil
	}
	if n.ResolvedArgsValid {
		return n.ResolvedArgs
	}
	return n.Args
}
