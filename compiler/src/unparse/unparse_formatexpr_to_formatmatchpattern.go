package unparse

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

func formatExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
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
					if item.ArgIndex < len(n.ArgShorthand) && n.ArgShorthand[item.ArgIndex] {
						parts = append(parts, name+":")
					} else {
						parts = append(parts, name+": "+argText)
					}
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
					if i < len(n.ArgShorthand) && n.ArgShorthand[i] {
						parts = append(parts, name+":")
					} else {
						parts = append(parts, name+": "+argText)
					}
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
		return formatExpr(n.Start) + " " + lexer.TokenName(n.Op) + " " + formatExpr(n.End)
	case *ast.ListComprehensionExpr:
		line := "[" + formatExpr(n.Value) + " for " + n.Name + " in " + formatExpr(n.Source)
		if n.RangeEnd != nil {
			line += " " + lexer.TokenName(n.RangeOp) + " " + formatExpr(n.RangeEnd)
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
			parts := make([]string, 0, len(n.Args)+len(n.Spreads))
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
		return "(" + formatExpr(n.Inner) + ")"
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
		if n.NodeSugar {
			options := make([]string, 0, 2)
			if n.Owner != nil {
				if ident, ok := n.Owner.(*ast.Ident); !ok || ident.Name != "alloc" {
					options = append(options, "alloc = "+formatExpr(n.Owner))
				}
			}
			if n.NodeSpan != nil {
				options = append(options, "span = "+formatExpr(n.NodeSpan))
			}
			if len(options) != 0 {
				return "node[" + strings.Join(options, ", ") + "] " + formatNodeSugarValue(n)
			}
			return "node " + formatNodeSugarValue(n)
		}
		if n.Owner != nil {
			return "new[" + formatExpr(n.Owner) + "] " + formatExpr(n.Value)
		}
		return "new " + formatExpr(n.Value)
	case *ast.CanExpr:
		return formatExprWithSurfacePermissions(n.Expr, n.Permissions)
	case *ast.MatchExpr:
		return formatMatchExpr(n)
	case *ast.VisitExpr:
		return formatVisitExpr(n)
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

func formatWhereViewExpr(expr *ast.CallExpr) (string, bool) {
	if expr == nil || len(expr.Args) != 2 || len(expr.ArgNames) != 0 || expr.HasArgForward {
		return "", false
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident == nil || ident.Name != "where" {
		return "", false
	}
	lambda, ok := expr.Args[1].(*ast.LambdaExpr)
	if !ok || lambda == nil || !lambda.UsesShorthandParams || len(lambda.Params) != 1 || lambda.BodyExpr == nil || len(lambda.Body) != 0 {
		return "", false
	}
	paramName := lambda.Params[0].Name
	if pattern, filter, ok := wherePatternPredicate(lambda.BodyExpr, paramName); ok {
		text := formatExpr(expr.Args[0]) + " where " + formatMatchPattern(pattern)
		if filter != nil {
			text += ": " + formatExpr(filter)
		}
		return text, true
	}
	if pattern, filter, ok := whereTupleSubjectPatternPredicate(lambda.BodyExpr, paramName); ok {
		text := formatExpr(expr.Args[0]) + " where index, value is " + formatMatchPattern(pattern)
		if filter != nil {
			text += ": " + formatWhereTuplePredicateExpr(filter, paramName)
		}
		return text, true
	}
	if paramName == "__where_item" {
		return formatExpr(expr.Args[0]) + " where index, value: " + formatWhereTuplePredicateExpr(lambda.BodyExpr, paramName), true
	}
	return formatExpr(expr.Args[0]) + " where " + paramName + ": " + formatExpr(lambda.BodyExpr), true
}

func wherePatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	if pattern, ok := directWherePatternPredicate(expr, paramName); ok {
		return pattern, nil, true
	}
	if pattern, filter, ok := matchWherePatternPredicate(expr, paramName); ok {
		return pattern, filter, true
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil && binary.Op == lexer.TOKEN_AND {
		if pattern, ok := directWherePatternPredicate(binary.Left, paramName); ok {
			return pattern, binary.Right, true
		}
	}
	return nil, nil, false
}

func directWherePatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary == nil || binary.Op != lexer.TOKEN_IS {
		return nil, false
	}
	ident, ok := binary.Left.(*ast.Ident)
	if !ok || ident == nil || ident.Name != paramName {
		return nil, false
	}
	switch target := binary.Right.(type) {
	case *ast.VariantTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	case *ast.StructTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	}
	return nil, false
}

func matchWherePatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	matchExpr, ok := expr.(*ast.MatchExpr)
	if !ok || matchExpr == nil || len(matchExpr.Arms) != 2 || matchExpr.Store != nil {
		return nil, nil, false
	}
	ident, ok := matchExpr.Value.(*ast.Ident)
	if !ok || ident == nil || ident.Name != paramName {
		return nil, nil, false
	}
	if _, ok := matchExpr.Arms[1].Pattern.(*ast.MatchWildcardPattern); !ok {
		return nil, nil, false
	}
	if len(matchExpr.Arms[0].Body) != 1 || len(matchExpr.Arms[1].Body) != 1 {
		return nil, nil, false
	}
	filterStmt, ok := matchExpr.Arms[0].Body[0].(*ast.ExprStmt)
	if !ok || filterStmt == nil {
		return nil, nil, false
	}
	fallbackStmt, ok := matchExpr.Arms[1].Body[0].(*ast.ExprStmt)
	if !ok || fallbackStmt == nil {
		return nil, nil, false
	}
	fallback, ok := fallbackStmt.Expr.(*ast.BoolLit)
	if !ok || fallback == nil || fallback.Value {
		return nil, nil, false
	}
	switch matchExpr.Arms[0].Pattern.(type) {
	case *ast.MatchVariantPattern, *ast.MatchStructPattern:
		return matchExpr.Arms[0].Pattern, filterStmt.Expr, true
	default:
		return nil, nil, false
	}
}

func whereTupleSubjectPatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	if pattern, ok := directWhereTupleSubjectPatternPredicate(expr, paramName); ok {
		return pattern, nil, true
	}
	if pattern, filter, ok := matchWhereTupleSubjectPatternPredicate(expr, paramName); ok {
		return pattern, filter, true
	}
	return nil, nil, false
}

func directWhereTupleSubjectPatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary == nil || binary.Op != lexer.TOKEN_IS {
		return nil, false
	}
	if !isWhereTupleValueField(binary.Left, paramName) {
		return nil, false
	}
	switch target := binary.Right.(type) {
	case *ast.VariantTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	case *ast.StructTestExpr:
		if target.Pattern != nil {
			return target.Pattern, true
		}
	}
	return nil, false
}

func matchWhereTupleSubjectPatternPredicate(expr ast.Expr, paramName string) (ast.MatchPattern, ast.Expr, bool) {
	matchExpr, ok := expr.(*ast.MatchExpr)
	if !ok || matchExpr == nil || len(matchExpr.Arms) != 2 || matchExpr.Store != nil {
		return nil, nil, false
	}
	if !isWhereTupleValueField(matchExpr.Value, paramName) {
		return nil, nil, false
	}
	if _, ok := matchExpr.Arms[1].Pattern.(*ast.MatchWildcardPattern); !ok {
		return nil, nil, false
	}
	if len(matchExpr.Arms[0].Body) != 1 || len(matchExpr.Arms[1].Body) != 1 {
		return nil, nil, false
	}
	filterStmt, ok := matchExpr.Arms[0].Body[0].(*ast.ExprStmt)
	if !ok || filterStmt == nil {
		return nil, nil, false
	}
	fallbackStmt, ok := matchExpr.Arms[1].Body[0].(*ast.ExprStmt)
	if !ok || fallbackStmt == nil {
		return nil, nil, false
	}
	fallback, ok := fallbackStmt.Expr.(*ast.BoolLit)
	if !ok || fallback == nil || fallback.Value {
		return nil, nil, false
	}
	switch matchExpr.Arms[0].Pattern.(type) {
	case *ast.MatchVariantPattern, *ast.MatchStructPattern:
		return matchExpr.Arms[0].Pattern, filterStmt.Expr, true
	default:
		return nil, nil, false
	}
}

func isWhereTupleValueField(expr ast.Expr, paramName string) bool {
	field, ok := expr.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "value" {
		return false
	}
	ident, ok := field.Object.(*ast.Ident)
	return ok && ident != nil && ident.Name == paramName
}

func formatWhereTuplePredicateExpr(expr ast.Expr, tupleName string) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		if ident, ok := n.Object.(*ast.Ident); ok && ident != nil && ident.Name == tupleName {
			switch n.Field {
			case "index":
				return "index"
			case "value":
				return "value"
			}
		}
		return formatWhereTuplePredicateExpr(n.Object, tupleName) + "." + n.Field
	case *ast.BinaryExpr:
		return "(" + formatWhereTuplePredicateExpr(n.Left, tupleName) + " " + lexer.TokenName(n.Op) + " " + formatWhereTuplePredicateExpr(n.Right, tupleName) + ")"
	case *ast.UnaryExpr:
		op := lexer.TokenName(n.Op)
		if op == "not" {
			return "(not " + formatWhereTuplePredicateExpr(n.Operand, tupleName) + ")"
		}
		return "(" + op + formatWhereTuplePredicateExpr(n.Operand, tupleName) + ")"
	case *ast.CallExpr:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, formatWhereTuplePredicateExpr(arg, tupleName))
		}
		return formatWhereTuplePredicateExpr(n.Func, tupleName) + "(" + strings.Join(parts, ", ") + ")"
	case *ast.ParenExpr:
		return "(" + formatWhereTuplePredicateExpr(n.Inner, tupleName) + ")"
	case *ast.IndexExpr:
		text := formatWhereTuplePredicateExpr(n.Object, tupleName) + "[" + formatWhereTuplePredicateExpr(n.Index, tupleName) + "]"
		if n.Fallback != nil {
			text += " else " + formatWhereTuplePredicateExpr(n.Fallback, tupleName)
		}
		return text
	case *ast.TernaryExpr:
		return "(" + formatWhereTuplePredicateExpr(n.Value, tupleName) + " if " + formatWhereTuplePredicateExpr(n.Cond, tupleName) + " else " + formatWhereTuplePredicateExpr(n.Alt, tupleName) + ")"
	default:
		return formatExpr(expr)
	}
}

func isRefCastTarget(t ast.TypeExpr) bool {
	switch n := t.(type) {
	case *ast.RefType:
		return true
	case *ast.MutableType:
		return isRefCastTarget(n.Elem)
	default:
		return false
	}
}
func formatStructLiteralTypeName(name string, typeArgs []ast.TypeExpr) string {
	if len(typeArgs) == 0 {
		return name
	}
	parts := make([]string, 0, len(typeArgs))
	for _, arg := range typeArgs {
		parts = append(parts, formatTypeExpr(arg))
	}
	return name + "[" + strings.Join(parts, ", ") + "]"
}
func formatNamedExprField(name string, value ast.Expr, separator string) string {
	if name == "" {
		return formatExpr(value)
	}
	if ident, ok := value.(*ast.Ident); ok && ident != nil && ident.Name == name {
		return name
	}
	return name + separator + formatExpr(value)
}
func formatStructLiteralField(name string, value ast.Expr, brace bool) string {
	if name == "" {
		return formatExpr(value)
	}
	if ident, ok := value.(*ast.Ident); ok && ident != nil && ident.Name == name {
		if brace {
			return name
		}
		return name + ":"
	}
	return name + ": " + formatExpr(value)
}
func formatMatchPatternField(arg ast.MatchPatternArg, brace bool) string {
	if arg.Name == "" {
		return ""
	}
	if brace {
		if bind, ok := arg.Pattern.(*ast.MatchBindPattern); ok && bind != nil && bind.Name == arg.Name {
			return arg.Name
		}
	}
	return arg.Name + ": " + formatMatchPattern(arg.Pattern)
}
func formatMatchExpr(expr *ast.MatchExpr) string {
	if expr == nil {
		return "match <nil>:"
	}
	return formatMatchBody(expr.Value, expr.Store, expr.Arms)
}
func formatMatchBody(value ast.Expr, store ast.Expr, arms []ast.MatchArm) string {
	var builder strings.Builder
	builder.WriteString(formatMatchHeader("match", value, store))
	for _, arm := range arms {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		builder.WriteString(formatMatchPattern(arm.Pattern))
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
	return builder.String()
}
func formatCatchExpr(expr *ast.CatchExpr) string {
	if expr == nil {
		return "catch <nil>:"
	}
	var builder strings.Builder
	builder.WriteString("catch ")
	builder.WriteString(formatExpr(expr.Value))
	builder.WriteString(":")
	writeArm := func(arm ast.CatchArm) {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		if arm.ErrorBinding {
			builder.WriteString("error ")
		}
		builder.WriteString(arm.Name)
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
	writeArm(expr.Success)
	for _, arm := range expr.Arms {
		writeArm(arm)
	}
	return builder.String()
}
func formatVisitExpr(expr *ast.VisitExpr) string {
	if expr == nil {
		return "visit <nil>:"
	}
	var builder strings.Builder
	builder.WriteString("visit ")
	builder.WriteString(formatExpr(expr.Value))
	if expr.Root != nil {
		builder.WriteString(" as ")
		builder.WriteString(formatTypeExpr(expr.Root))
	}
	builder.WriteString(":")
	formatVisitArmsInto(&builder, expr.Arms)
	return builder.String()
}
func formatFoldExpr(expr *ast.FoldExpr) string {
	if expr == nil {
		return "fold <nil> as <type> into <type>:"
	}
	keyword := expr.Keyword
	if keyword == "" {
		keyword = "fold"
	}
	var builder strings.Builder
	builder.WriteString(keyword)
	builder.WriteByte(' ')
	builder.WriteString(formatExpr(expr.Value))
	builder.WriteString(" as ")
	builder.WriteString(formatTypeExpr(expr.Root))
	if keyword == "rewrite" {
		if expr.RewriteDefault {
			builder.WriteString(" default")
		}
	} else {
		builder.WriteString(" into ")
		builder.WriteString(formatTypeExpr(expr.ResultType))
	}
	builder.WriteString(":")
	formatVisitArmsInto(&builder, expr.Arms)
	return builder.String()
}
func formatVisitArmsInto(builder *strings.Builder, arms []ast.VisitArm) {
	for _, arm := range arms {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		builder.WriteString(formatVisitArm(arm))
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
}
func formatVisitArm(arm ast.VisitArm) string {
	if arm.Wildcard {
		line := "_"
		if arm.Guard != nil {
			line += " when " + formatExpr(arm.Guard)
		}
		return line
	}
	line := arm.TargetName
	if arm.BindName == "" && arm.ChildResultsName == "" && len(arm.ChildBindings) == 0 {
		if arm.Guard != nil {
			line += " when " + formatExpr(arm.Guard)
		}
		return line
	}
	line += "("
	if arm.BindName != "" {
		line += arm.BindName
	}
	if arm.ChildResultsName != "" {
		if arm.BindName != "" {
			line += ", "
		}
		line += arm.ChildResultsName
	} else if len(arm.ChildBindings) != 0 {
		for i, binding := range arm.ChildBindings {
			if arm.BindName != "" || i != 0 {
				line += ", "
			}
			line += binding.FieldName
			if binding.BindName != "" && binding.BindName != binding.FieldName {
				line += ": "
				line += binding.BindName
			}
		}
	}
	line += ")"
	if arm.Guard != nil {
		line += " when " + formatExpr(arm.Guard)
	}
	return line
}
func formatMatchPattern(pattern ast.MatchPattern) string {
	switch n := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return "_"
	case *ast.MatchBindPattern:
		return n.Name
	case *ast.MatchStringLiteralPattern:
		return strconv.Quote(n.Value)
	case *ast.MatchLiteralPattern:
		if n.Pinned {
			return "^" + formatExpr(n.Value)
		}
		return formatExpr(n.Value)
	case *ast.MatchTuplePattern:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, formatMatchPattern(elem))
		}
		return strings.Join(parts, ", ")
	case *ast.MatchListPattern:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, formatMatchPattern(elem))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.MatchOrPattern:
		parts := make([]string, 0, len(n.Options))
		for _, option := range n.Options {
			parts = append(parts, formatMatchPattern(option))
		}
		return strings.Join(parts, " | ")
	case *ast.MatchRestPattern:
		return "..."
	case *ast.MatchStructPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			part := formatMatchPatternField(arg, n.Brace)
			if part == "" {
				part = formatMatchPattern(arg.Pattern)
			}
			parts = append(parts, part)
		}
		if n.Brace {
			if n.TypeName == "" {
				return "{" + strings.Join(parts, ", ") + "}"
			}
			return n.TypeName + "{" + strings.Join(parts, ", ") + "}"
		}
		if len(parts) == 0 {
			return n.TypeName + "()"
		}
		return n.TypeName + "(" + strings.Join(parts, ", ") + ")"
	case *ast.MatchVariantPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			if arg.Name != "" {
				parts = append(parts, arg.Name+": "+formatMatchPattern(arg.Pattern))
			} else {
				parts = append(parts, formatMatchPattern(arg.Pattern))
			}
		}
		line := n.EnumName + "." + n.Variant
		if len(parts) != 0 {
			line += "(" + strings.Join(parts, ", ") + ")"
		}
		return line
	default:
		return "<pattern>"
	}
}
