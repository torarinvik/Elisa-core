package unparse

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

// rangeOpName renders a range operator for output, emitting `..=` for the inclusive kind (stored as
// TOKEN_RANGE) instead of the now-deprecated bare `..`, so unparsed ranges re-parse cleanly.
func rangeOpName(op lexer.TokenKind) string {
	if op == lexer.TOKEN_RANGE {
		return "..="
	}
	return lexer.TokenName(op)
}

func formatExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch n := expr.(type) {
	case *ast.Ident:
		// A dot inside an Ident NAME can only have come from a `::` chain: in expression
		// position `.` is member access and parses to a FieldExpr, never into the name.
		// So `Outer::Inner::helper` arrives here as "Outer.Inner.helper" and has to be
		// spelled back out, or the formatter emits a call to an undefined identifier.
		return ast.ModulePathSpelling(n.Name)
	case *ast.IntLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.FloatLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.StringLit:
		return strconv.Quote(n.Value)
	case *ast.CharLit:
		return formatCharLiteral(n.Value)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.ZeroedLit:
		return "zeroed"
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_IS {
			right := formatExpr(n.Right)
			if strings.Contains(right, "\n") {
				return formatExpr(n.Left) + " is " + right
			}
		}
		return "(" + formatExpr(n.Left) + " " + lexer.TokenName(n.Op) + " " + formatExpr(n.Right) + ")"
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			if isExpr, ok := n.Operand.(*ast.BinaryExpr); ok && isExpr != nil && isExpr.Op == lexer.TOKEN_IS {
				return formatExpr(isExpr.Left) + " is not " + formatExpr(isExpr.Right)
			}
			if membership, ok := n.Operand.(*ast.BinaryExpr); ok && membership != nil && membership.Op == lexer.TOKEN_IN {
				return formatExpr(membership.Left) + " not in " + formatExpr(membership.Right)
			}
		}
		op := lexer.TokenName(n.Op)
		if op == "not" {
			return "(not " + formatExpr(n.Operand) + ")"
		}
		return "(" + op + formatExpr(n.Operand) + ")"
	case *ast.MoveExpr:
		return "move " + formatExpr(n.Operand)
	case *ast.CallExpr:
		if text, ok := formatWhereViewExpr(n); ok {
			return text
		}
		funcText := formatExpr(n.Func)
		if n.Safe {
			if n.SafeReceiver != nil {
				funcText = formatExpr(n.SafeReceiver) + "?.(" + formatExpr(n.Func) + ")"
				if len(n.Args) == 0 && !n.HasArgForward {
					return funcText
				}
			} else if fieldExpr, ok := n.Func.(*ast.FieldExpr); ok && fieldExpr != nil {
				funcText = formatExpr(fieldExpr.Object) + "?." + fieldExpr.Field
			}
		}
		partCapacity := len(n.Args)
		if n.HasArgForward {
			partCapacity++
		}
		parts := make([]string, 0, partCapacity)
		multiline := strings.Contains(funcText, "\n")
		if n.HasArgForward {
			parts = append(parts, "..")
		}
		if len(n.ArgItemOrder) != 0 {
			for _, item := range n.ArgItemOrder {
				argText := formatExpr(n.Args[item.ArgIndex])
				if strings.Contains(argText, "\n") {
					multiline = true
					argText = indentMultilineText(argText, indentUnit)
				}
				if name := n.ArgName(item.ArgIndex); name != "" {
					parts = append(parts, name+": "+argText)
				} else {
					parts = append(parts, argText)
				}
			}
		} else {
			for i, arg := range n.Args {
				argText := formatExpr(arg)
				if strings.Contains(argText, "\n") {
					multiline = true
					argText = indentMultilineText(argText, indentUnit)
				}
				if name := n.ArgName(i); name != "" {
					parts = append(parts, name+": "+argText)
				} else {
					parts = append(parts, argText)
				}
			}
		}
		line := ""
		if multiline {
			line = funcText + "(\n" + strings.Join(parts, ",\n") + "\n)"
		} else {
			line = funcText + "(" + strings.Join(parts, ", ") + ")"
		}
		return line
	case *ast.FieldExpr:
		if n.Safe {
			return formatExpr(n.Object) + "?." + n.Field
		}
		return formatExpr(n.Object) + "." + n.Field
	case *ast.EnumColumnExpr:
		return n.Enum + " of ." + n.Field
	case *ast.ShorthandMemberExpr:
		return "." + strings.Join(n.Parts, ".")
	case *ast.IndexExpr:
		line := formatExpr(n.Object) + "[" + formatExpr(n.Index) + "]"
		if n.Fallback != nil {
			line += " else " + formatExpr(n.Fallback)
		}
		return line
	case *ast.SliceExpr:
		return formatExpr(n.Object) + "[" + formatExpr(n.Start) + ":" + formatExpr(n.End) + "]"
	case *ast.ListLitExpr:
		parts := make([]string, 0, len(n.Elems))
		for i, elem := range n.Elems {
			prefix := ""
			if i < len(n.Spreads) && n.Spreads[i] {
				prefix = "..."
			}
			parts = append(parts, prefix+formatExpr(elem))
		}
		if n.Brace {
			return "{" + strings.Join(parts, ", ") + "}"
		}
		line := "[" + strings.Join(parts, ", ") + "]"
		if n.Owner != nil {
			line += " in " + formatExpr(n.Owner)
		}
		return line
	case *ast.MembershipRangeExpr:
		return formatExpr(n.Start) + " " + rangeOpName(n.Op) + " " + formatExpr(n.End)
	case *ast.ListComprehensionExpr:
		line := "[" + formatExpr(n.Value) + " for " + n.Name + " in " + formatExpr(n.Source)
		if n.RangeEnd != nil {
			line += " " + rangeOpName(n.RangeOp) + " " + formatExpr(n.RangeEnd)
			if n.RangeStep != nil {
				line += " .. " + formatExpr(n.RangeStep)
			}
		}
		if n.Filter != nil {
			line += " if " + formatExpr(n.Filter)
		}
		line += "]"
		if n.Owner != nil {
			line += " in " + formatExpr(n.Owner)
		}
		return line
	case *ast.QueryExpr:
		binder := formatQueryBindPattern(n.Name, n.Pattern)
		if n.Kind == ast.QueryExprFirst && n.Projection != nil {
			return formatExpr(n.Projection) + " for first " + binder + " in " + formatExpr(n.Source) + formatQueryFilter(n.Filter, n.PatternFilter, n.PatternFilterSubject) + formatQueryOwner(n.Owner)
		}
		if n.Kind == ast.QueryExprEach && n.Projection != nil {
			return formatExpr(n.Projection) + " for each " + binder + " in " + formatExpr(n.Source) + formatQueryFilter(n.Filter, n.PatternFilter, n.PatternFilterSubject) + formatQueryOwner(n.Owner)
		}
		keyword := "any"
		switch n.Kind {
		case ast.QueryExprAny:
			keyword = "any"
		case ast.QueryExprAll:
			keyword = "all"
		case ast.QueryExprFirst:
			keyword = "first"
		case ast.QueryExprCount:
			keyword = "count"
		case ast.QueryExprEach:
			keyword = "each"
		}
		return keyword + " " + binder + " in " + formatExpr(n.Source) + formatQueryFilter(n.Filter, n.PatternFilter, n.PatternFilterSubject) + formatQueryOwner(n.Owner)
	case *ast.CastExpr:
		if n.Origin == ast.CastExprOriginIndirectCall {
			// The synthetic cast produced by `value.call_as[func(...)->T](args)`.
			// Render it back as `.call_as[...]` (the enclosing CallExpr appends
			// the `(args)`); `.cast[...]` would not re-parse with a trailing call.
			return formatExpr(n.Operand) + ".call_as[" + formatTypeExpr(n.Target) + "]"
		}
		if n.Origin == ast.CastExprOriginPostfixShorthand {
			if target, ok := formatPostfixShorthandCastTarget(n.Target); ok {
				return formatExpr(n.Operand) + "." + target + "()"
			}
		}
		// `as`/`->` cast spellings are removed; render all value casts as
		// `.cast[T]` (the canonical form) regardless of their parsed origin.
		if addr, ok := n.Operand.(*ast.AddrOfExpr); ok && addr != nil {
			if isRefCastTarget(n.Target) {
				return formatExpr(addr.Operand) + ".ref[" + formatTypeExpr(n.Target) + "]"
			}
		}
		return formatExpr(n.Operand) + ".cast[" + formatTypeExpr(n.Target) + "]"
	case *ast.LambdaExpr:
		return formatLambdaExpr(n)
	case *ast.SizeofExpr:
		return "size_of(" + formatTypeExpr(n.Type) + ")"
	case *ast.AlignofExpr:
		return "align_of(" + formatTypeExpr(n.Type) + ")"
	case *ast.OffsetofExpr:
		return "offset_of(" + formatTypeExpr(n.Type) + ", " + n.Field + ")"
	case *ast.TernaryExpr:
		return "(" + formatExpr(n.Value) + " if " + formatExpr(n.Cond) + " else " + formatExpr(n.Alt) + ")"
	case *ast.AddrOfExpr:
		return "&" + formatExpr(n.Operand)
	case *ast.SpecializeExpr:
		parts := make([]string, 0, len(n.TypeArgs))
		for _, arg := range n.TypeArgs {
			parts = append(parts, formatTypeExpr(arg))
		}
		return formatExpr(n.Operand) + "[" + strings.Join(parts, ", ") + "]"
	case *ast.StructLitExpr:
		typeName := formatStructLiteralTypeName(n.Name, n.TypeArgs)
		if n.Brace {
			parts := make([]string, 0, len(n.Args)+len(n.Spreads)+1)
			if n.CopyBase != nil {
				parts = append(parts, ".."+formatExpr(n.CopyBase))
			}
			for _, spread := range n.Spreads {
				parts = append(parts, "..."+formatExpr(spread))
			}
			for i, arg := range n.Args {
				parts = append(parts, formatStructLiteralField(n.ArgName(i), arg, true))
			}
			return typeName + "{" + strings.Join(parts, ", ") + "}"
		}
		parts := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			parts = append(parts, formatStructLiteralField(n.ArgName(i), arg, false))
		}
		return typeName + "(" + strings.Join(parts, ", ") + ")"
	case *ast.RecordUpdateExpr:
		parts := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			parts = append(parts, formatNamedExprField(n.ArgName(i), arg, " = "))
		}
		return formatExpr(n.Base) + "{" + strings.Join(parts, ", ") + "}"
	case *ast.TupleExpr:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, formatExpr(elem))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *ast.ExprBlock:
		lines := []string{"do:"}
		for _, stmt := range n.Stmts {
			formatted := strings.TrimRight(FormatStmt(stmt), "\n")
			for _, line := range strings.Split(formatted, "\n") {
				lines = append(lines, indentUnit+line)
			}
		}
		valueText := strings.TrimRight(formatExpr(n.Value), "\n")
		if matchExpr, ok := n.Value.(*ast.MatchExpr); ok {
			valueText = strings.TrimRight(formatMatchBody(matchExpr.Value, matchExpr.Store, matchExpr.Arms), "\n")
		}
		for _, line := range strings.Split(valueText, "\n") {
			lines = append(lines, indentUnit+line)
		}
		return strings.Join(lines, "\n")
	case *ast.VariantTestExpr:
		if n.Pattern == nil {
			return "<variant-test>"
		}
		return formatMatchPattern(n.Pattern)
	case *ast.StructTestExpr:
		if n.Pattern == nil {
			return "<struct-test>"
		}
		return formatMatchPattern(n.Pattern)
	case *ast.IsPatternExpr:
		parts := make([]string, 0, len(n.Targets))
		for _, target := range n.Targets {
			parts = append(parts, formatExpr(target))
		}
		inline := strings.Join(parts, " | ")
		if len(inline) <= 96 && len(parts) <= 3 {
			if n.Brackets {
				return "[" + inline + "]"
			}
			return inline
		}
		open, close := "(", ")"
		if n.Brackets {
			open, close = "[", "]"
		}
		lines := []string{open}
		for index, part := range parts {
			prefix := "    "
			if index > 0 {
				prefix = "    | "
			}
			lines = append(lines, prefix+part)
		}
		lines = append(lines, close)
		return strings.Join(lines, "\n")
	case *ast.IsAliasExpr:
		if n.Target == nil {
			return "as " + n.Alias
		}
		return formatExpr(n.Target) + " as " + n.Alias
	case *ast.TypeExprExpr:
		return formatTypeExpr(n.Type)
	case *ast.ParenExpr:
		// Do not double a group the inner expression already prints for itself. A
		// BinaryExpr always emits its own surrounding parens (see the case above), so a
		// source-written `(a + b)` parses to ParenExpr{BinaryExpr} and used to format as
		// `((a + b))`. That made -emit fmt NON-IDEMPOTENT: re-formatting its own output
		// added another pair every pass, `((total * 10) + i)` -> `((((total * 10)) + i))`
		// and so on without bound.
		inner := formatExpr(n.Inner)
		if selfParenthesizingExpr(n.Inner) &&
			strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") {
			return inner
		}
		return "(" + inner + ")"
	case *ast.RaiseExpr:
		return "raise " + formatExpr(n.Error)
	case *ast.TryExpr:
		line := "try " + formatExpr(n.Value)
		if n.Recovery != nil || n.Fallback != nil {
			line += " else " + formatRecoveryClause(n.Recovery, n.Fallback)
		}
		return line
	case *ast.CatchExpr:
		return formatCatchExpr(n)
	case *ast.UnwrapElseExpr:
		return formatExpr(n.Value) + " else " + formatRecoveryClause(n.Recovery, n.Fallback)
	case *ast.OptionalBindExpr:
		return "let " + n.Name + " = " + formatExpr(n.Value)
	case *ast.AllocExpr:
		if n.Owner != nil {
			return "new[" + formatExpr(n.Owner) + "] " + formatExpr(n.Value)
		}
		return "new " + formatExpr(n.Value)
	case *ast.CanExpr:
		return formatExprWithSurfacePermissions(n.Expr, n.Permissions)
	case *ast.MatchExpr:
		return formatMatchExpr(n)
	case *ast.FoldExpr:
		return formatFoldExpr(n)
	case *ast.EmitExpr:
		if n.Nothing || n.Value == nil {
			return "emit nothing"
		}
		if n.All {
			return "emit all " + formatExpr(n.Value)
		}
		return "emit " + formatExpr(n.Value)
	default:
		return "<expr>"
	}
}

func formatQueryBindPattern(name string, pattern ast.MoveBindPattern) string {
	if pattern == nil {
		return name
	}
	return formatMoveBindPattern(pattern)
}

func formatQueryFilter(filter ast.Expr, pattern ast.MatchPattern, subject string) string {
	if pattern != nil {
		prefix := ""
		if subject != "" {
			prefix = subject + " is "
		}
		if filter != nil {
			return " where " + prefix + formatMatchPattern(pattern) + ": " + formatExpr(filter)
		}
		return " where " + prefix + formatMatchPattern(pattern)
	}
	if filter == nil {
		return ""
	}
	return " where " + formatExpr(filter)
}

func formatQueryOwner(owner ast.Expr) string {
	if owner == nil {
		return ""
	}
	return " with " + formatExpr(owner)
}

func formatRecoveryClause(recovery *ast.RecoveryClause, fallback ast.Expr) string {
	if recovery == nil {
		return formatExpr(fallback)
	}
	switch recovery.Kind {
	case ast.RecoveryValue:
		return formatExpr(recovery.Value)
	case ast.RecoveryReturn:
		if recovery.Value == nil {
			return "return"
		}
		return "return " + formatExpr(recovery.Value)
	case ast.RecoveryRaise:
		return "raise " + formatExpr(recovery.Value)
	case ast.RecoveryVoid:
		return "void"
	case ast.RecoveryBlock:
		if recovery.Binding != "" {
			return recovery.Binding + ": ..."
		}
		return ": ..."
	default:
		return formatExpr(fallback)
	}
}
