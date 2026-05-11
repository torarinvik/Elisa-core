package unparse

import (
	"elisacore/src/ast"
	"strconv"
	"strings"
)

func (f *formatter) writePrecedenceTerm(level int, precedence *ast.GrammarPrecedenceTerm) {
	if precedence == nil {
		f.writeLine(level, "<invalid_grammar_term>")
		return
	}
	if len(precedence.Levels) != 0 {
		f.writeLine(level, "precedence("+precedence.Result+"):")
		for _, levelDecl := range precedence.Levels {
			f.writeNamedPrecedenceLevel(level+1, levelDecl)
		}
		return
	}
	header := "precedence"
	if precedence.Assoc != "" {
		header += " " + precedence.Assoc
	}
	f.writeLine(level, header+"("+precedence.LeftName+" = "+formatGrammarTerm(precedence.Seed)+"):")
	for _, arm := range precedence.Arms {
		f.writePrecedenceArm(level+1, arm)
	}
}
func (f *formatter) writeNamedPrecedenceLevel(level int, levelDecl ast.GrammarPrecedenceLevel) {
	prefix := ""
	if levelDecl.Assoc != "" {
		prefix = levelDecl.Assoc + " "
	}
	if levelDecl.LeftName == "" {
		if nested, ok := levelDecl.Seed.(*ast.GrammarPrecedenceTerm); ok {
			f.writeBoundPrecedenceTerm(level, levelDecl.Name, nested)
			return
		}
		f.writeLine(level, prefix+levelDecl.Name+" = "+formatGrammarTerm(levelDecl.Seed))
		return
	}
	f.writeLine(level, prefix+levelDecl.Name+"("+levelDecl.LeftName+" = "+formatGrammarTerm(levelDecl.Seed)+"):")
	for _, arm := range levelDecl.Arms {
		f.writePrecedenceArm(level+1, arm)
	}
}
func (f *formatter) writePrecedenceArm(level int, arm ast.GrammarPrecedenceArm) {
	opText := formatGrammarTerm(arm.Op)
	if arm.OpName != "" {
		opText = arm.OpName + " = " + opText
	}
	if arm.Block {
		f.writeLine(level, opText+":")
		for _, binding := range arm.Bindings {
			f.writeLine(level+1, formatGrammarBinding(binding))
		}
		f.writeLine(level+1, "-> "+formatExpr(arm.Value))
		return
	}
	parts := []string{opText}
	for _, binding := range arm.Bindings {
		parts = append(parts, formatGrammarBinding(binding))
	}
	parts = append(parts, "-> "+formatExpr(arm.Value))
	f.writeLine(level, strings.Join(parts, " "))
}
func (f *formatter) writePostfixArm(level int, arm ast.GrammarPostfixArm) {
	opText := formatGrammarTerm(arm.Op)
	if arm.OpName != "" {
		opText = arm.OpName + " = " + opText
	}
	if arm.Block {
		f.writeLine(level, opText+":")
		for _, binding := range arm.Bindings {
			f.writeLine(level+1, formatGrammarBinding(binding))
		}
		f.writeLine(level+1, "-> "+formatExpr(arm.Value))
		return
	}
	parts := []string{opText}
	for _, binding := range arm.Bindings {
		parts = append(parts, formatGrammarBinding(binding))
	}
	parts = append(parts, "-> "+formatExpr(arm.Value))
	f.writeLine(level, strings.Join(parts, " "))
}
func formatGrammarBinding(binding *ast.GrammarBindTerm) string {
	if binding == nil {
		return "<invalid_grammar_binding>"
	}
	if tokenKind, ok := binding.Term.(*ast.GrammarTokenKindTerm); ok {
		return "." + tokenKind.Kind + "(" + binding.Name + ")"
	}
	return binding.Name + " = " + formatGrammarTerm(binding.Term)
}
func formatGrammarReturnPayload(term ast.GrammarTerm) string {
	if term == nil {
		return "<invalid_grammar_term>"
	}
	if expr, ok := term.(*ast.GrammarExprTerm); ok && expr != nil && expr.Type == nil {
		return formatExpr(expr.Expr)
	}
	return formatGrammarTerm(term)
}
func formatGrammarPrefixSugar(seq *ast.GrammarSeqTerm) (string, bool) {
	if seq == nil || len(seq.Terms) != 3 {
		return "", false
	}
	opBind, ok := seq.Terms[0].(*ast.GrammarBindTerm)
	if !ok || opBind.Name != "op" || opBind.Term == nil {
		return "", false
	}
	operandBind, ok := seq.Terms[1].(*ast.GrammarBindTerm)
	if !ok || operandBind.Name != "operand" || operandBind.Term == nil {
		return "", false
	}
	value, ok := seq.Terms[2].(*ast.GrammarExprTerm)
	if !ok || value == nil || value.Type != nil {
		return "", false
	}
	return "prefix(" + formatGrammarPrefixOps(opBind.Term) + ") " + formatGrammarTerm(operandBind.Term) + " -> " + formatExpr(value.Expr), true
}
func formatGrammarPrefixOps(term ast.GrammarTerm) string {
	if choice, ok := term.(*ast.GrammarChoiceTerm); ok {
		parts := make([]string, 0, len(choice.Options))
		for _, option := range choice.Options {
			parts = append(parts, formatGrammarTerm(option))
		}
		return strings.Join(parts, ", ")
	}
	return formatGrammarTerm(term)
}
func formatGrammarTerm(term ast.GrammarTerm) string {
	switch n := term.(type) {
	case *ast.GrammarPassTerm:
		return "pass"
	case *ast.GrammarTokenTerm:
		return strconv.Quote(n.Value)
	case *ast.GrammarTokenKindTerm:
		return "." + n.Kind
	case *ast.GrammarCallTerm:
		if !n.Explicit && len(n.Args) == 0 {
			return n.Name
		}
		args := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			args = append(args, formatExpr(arg))
		}
		return n.Name + "(" + strings.Join(args, ", ") + ")"
	case *ast.GrammarChoiceTerm:
		options := make([]string, 0, len(n.Options))
		for _, option := range n.Options {
			options = append(options, formatGrammarTerm(option))
		}
		return "choice(" + strings.Join(options, ", ") + ")"
	case *ast.GrammarWhenTerm:
		if n.TokenKindGate != "" {
			text := "." + n.TokenKindGate + "? then " + formatGrammarTerm(n.Then)
			if !grammarWhenElseIsImplicitNull(n.Else) {
				text += " else " + formatGrammarTokenGateElse(n.Else)
			}
			return text
		}
		return "when(" + formatExpr(n.Cond) + ", " + formatGrammarTerm(n.Then) + ", " + formatGrammarTerm(n.Else) + ")"
	case *ast.GrammarMatchTerm:
		return "match " + formatExpr(n.Value) + ": ..."
	case *ast.GrammarRequiredTerm:
		return "required(" + formatGrammarTerm(n.Term) + ", " + formatExpr(n.Message) + ")"
	case *ast.GrammarDelimitedTerm:
		return "delimited(" + formatGrammarTerm(n.Open) + ", " + formatGrammarTerm(n.Body) + ", " + formatGrammarTerm(n.Close) + ", " + formatExpr(n.Message) + ")"
	case *ast.GrammarSeqTerm:
		if prefix, ok := formatGrammarPrefixSugar(n); ok {
			return prefix
		}
		parts := make([]string, 0, len(n.Terms))
		for _, term := range n.Terms {
			parts = append(parts, formatGrammarTerm(term))
		}
		return "seq(" + strings.Join(parts, ", ") + ")"
	case *ast.GrammarLookaheadTerm:
		return "lookahead(" + formatGrammarTerm(n.Term) + ")"
	case *ast.GrammarExprTerm:
		switch n.Expr.(type) {
		case *ast.ListLitExpr, *ast.ListComprehensionExpr:
			return formatExpr(n.Expr)
		}
		if n.Type != nil {
			return "expr[" + formatTypeExpr(n.Type) + "](" + formatExpr(n.Expr) + ")"
		}
		return "expr(" + formatExpr(n.Expr) + ")"
	case *ast.GrammarSingletonTerm:
		if n.Type != nil {
			return "singleton[" + formatTypeExpr(n.Type) + "](" + formatExpr(n.Value) + ")"
		}
		return "singleton(" + formatExpr(n.Value) + ")"
	case *ast.GrammarEmptyTerm:
		if n.Type != nil {
			return "empty[" + formatTypeExpr(n.Type) + "]"
		}
		return "empty"
	case *ast.GrammarConcatTerm:
		parts := make([]string, 0, len(n.Terms))
		for _, term := range n.Terms {
			parts = append(parts, formatGrammarTerm(term))
		}
		return strings.Join(parts, " + ")
	case *ast.GrammarRecoverTerm:
		if n.RecoverPolicy != "" {
			return formatGrammarTerm(n.Term) + formatGrammarRecoverPolicyUse(n.RecoverPolicy)
		}
		return formatGrammarTerm(n.Term) + formatGrammarRecoverClause(n.RecoverMsg, n.RecoverUntil, n.RecoverValue)
	case *ast.GrammarGuardTerm:
		return "guard(" + formatExpr(n.Cond) + ")"
	case *ast.GrammarAttemptTerm:
		return "attempt(" + formatExpr(n.Expr) + ")"
	case *ast.GrammarCutTerm:
		return "cut"
	case *ast.GrammarOptionalTerm:
		return formatGrammarTerm(n.Term) + "?"
	case *ast.GrammarListTerm:
		if n.Separator != nil {
			return formatReadableSeparatedTerm(n.Elem, n.Separator, n.Until)
		}
		parts := []string{formatGrammarTerm(n.Elem)}
		if len(n.Until) != 0 {
			parts = append(parts, formatGrammarUntil(n.Until))
		}
		return "list(" + strings.Join(parts, ", ") + ")"
	case *ast.GrammarRepeatTerm:
		parts := []string{formatGrammarTerm(n.Elem)}
		if len(n.Until) != 0 {
			parts = append(parts, formatGrammarUntil(n.Until))
		}
		return "repeat(" + strings.Join(parts, ", ") + ")"
	case *ast.GrammarFlatRepeatTerm:
		parts := []string{formatGrammarTerm(n.Elem)}
		if len(n.Until) != 0 {
			parts = append(parts, formatGrammarUntil(n.Until))
		}
		return "flatrepeat(" + strings.Join(parts, ", ") + ")"
	case *ast.GrammarWhileTerm:
		parts := make([]string, 0, len(n.Until))
		for _, until := range n.Until {
			parts = append(parts, formatGrammarTerm(until))
		}
		return "[" + formatGrammarTerm(n.Elem) + "] while token in tokens != [" + strings.Join(parts, ", ") + "]"
	case *ast.GrammarSeparatedTerm:
		return formatReadableSeparatedTerm(n.Elem, n.Separator, n.Until)
	case *ast.GrammarSuffixTerm:
		return "suffix(" + n.LeftName + " = " + formatGrammarTerm(n.Seed) + "):"
	case *ast.GrammarPostfixTerm:
		return "postfix(" + n.LeftName + " = " + formatGrammarTerm(n.Seed) + "):"
	case *ast.GrammarPrecedenceTerm:
		if len(n.Levels) != 0 {
			return "precedence(" + n.Result + "):"
		}
		header := "precedence"
		if n.Assoc != "" {
			header += " " + n.Assoc
		}
		return header + "(" + n.LeftName + " = " + formatGrammarTerm(n.Seed) + "):"
	case *ast.GrammarInfixTableTerm:
		return "infix(" + n.TableName + ")"
	case *ast.GrammarTokenSetRefTerm:
		return n.Name
	case *ast.GrammarFirstTerm:
		return "first(" + n.Name + ")"
	case *ast.GrammarApplyTerm:
		if n.Piped && len(n.Args) != 0 {
			args := make([]string, 0, len(n.Args)-1)
			for _, arg := range n.Args[1:] {
				text := formatGrammarTerm(arg.Term)
				if arg.Name != "" {
					text = arg.Name + ": " + text
				}
				args = append(args, text)
			}
			return formatGrammarTerm(n.Args[0].Term) + " |> " + n.Name + "(" + strings.Join(args, ", ") + ")"
		}
		args := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			text := formatGrammarTerm(arg.Term)
			if arg.Name != "" {
				text = arg.Name + ": " + text
			}
			args = append(args, text)
		}
		if n.Direct {
			return n.Name + "(" + strings.Join(args, ", ") + ")"
		}
		return "apply " + n.Name + "(" + strings.Join(args, ", ") + ")"
	case *ast.GrammarBindTerm:
		return formatGrammarBinding(n)
	case *ast.GrammarAssignTerm:
		return n.Name + " <- " + formatGrammarTerm(n.Term)
	case *ast.GrammarReturnTerm:
		return "return " + formatGrammarReturnPayload(n.Term)
	default:
		return "<grammar-term>"
	}
}
func grammarWhenElseIsImplicitNull(term ast.GrammarTerm) bool {
	exprTerm, ok := term.(*ast.GrammarExprTerm)
	if !ok || exprTerm == nil {
		return false
	}
	_, ok = exprTerm.Expr.(*ast.NullLit)
	return ok
}
func formatGrammarTokenGateElse(term ast.GrammarTerm) string {
	if empty, ok := term.(*ast.GrammarEmptyTerm); ok && empty != nil && empty.Type == nil {
		return "[]"
	}
	return formatGrammarTerm(term)
}
func formatGrammarUntil(until []ast.GrammarTerm) string {
	untilParts := make([]string, 0, len(until))
	for _, stop := range until {
		untilParts = append(untilParts, formatGrammarTerm(stop))
	}
	return "until(" + strings.Join(untilParts, ", ") + ")"
}
func formatReadableSeparatedTerm(elem ast.GrammarTerm, separator ast.GrammarTerm, until []ast.GrammarTerm) string {
	text := "separated " + formatGrammarTerm(elem) + " by " + formatGrammarTerm(separator)
	if len(until) != 0 {
		text += " " + formatGrammarUntil(until)
	}
	return text
}
func (f *formatter) writeField(level int, field ast.FieldDecl) {
	f.writeAnnotations(level, field.Annotations)
	line := field.Name + ": "
	if field.Mutable {
		line += "mutable "
	}
	if field.IsTail {
		line += "tail "
	}
	line += formatTypeExpr(field.Type)
	f.writeLine(level, line)
}
func formatExprWithSurfacePermissions(expr ast.Expr, permissions []ast.PermissionRef) string {
	permissionText := formatPermissionRefSurfaceList(permissions)
	if permissionText == "" {
		return formatExpr(expr)
	}
	exprText := formatExpr(expr)
	switch expr.(type) {
	case *ast.TryExpr, *ast.UnwrapElseExpr:
		exprText = "(" + exprText + ")"
	}
	return exprText + " can " + permissionText
}
func formatInlineCanStmt(stmt ast.Stmt, permissions []ast.PermissionRef) (string, bool) {
	if stmt == nil || len(permissions) == 0 {
		return "", false
	}
	if stmtContainsNodeSugar(stmt) {
		return "", false
	}
	if stmtContainsCanExpr(stmt) {
		return "", false
	}
	wrapExpr := func(expr ast.Expr) string {
		return formatExprWithSurfacePermissions(expr, permissions)
	}
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		op := " <- "
		if n.Optional {
			op = " ?= "
		}
		return formatExpr(n.Target) + op + wrapExpr(n.Value), true
	case *ast.AsRefAssignStmt:
		line := formatExpr(n.Target) + " as"
		if n.AsKind != "" {
			line += " " + n.AsKind
		}
		line += " <- " + wrapExpr(n.Value)
		return line, true
	case *ast.VarDeclStmt:
		if n.Value == nil {
			return "", false
		}
		if n.Type == nil {
			return n.Name + " = " + wrapExpr(n.Value), true
		}
		line := n.Name + ": "
		if n.Mutable {
			line += "mutable "
		}
		line += formatTypeExpr(n.Type)
		line += " = " + wrapExpr(n.Value)
		return line, true
	case *ast.TupleBindStmt:
		names := make([]string, 0, len(n.Names))
		for _, name := range n.Names {
			names = append(names, name.Name)
		}
		op := " <- "
		if n.Declare {
			op = " = "
		}
		return strings.Join(names, ", ") + op + wrapExpr(n.Value), true
	case *ast.ReturnStmt:
		if n.Value == nil {
			return "", false
		}
		return "return " + wrapExpr(n.Value), true
	case *ast.ExprStmt:
		return wrapExpr(n.Expr), true
	case *ast.DiscardStmt:
		return "_ = " + wrapExpr(n.Value), true
	default:
		return "", false
	}
}
func stmtContainsCanExpr(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		return exprContainsCanExpr(n.Value)
	case *ast.AsRefAssignStmt:
		return exprContainsCanExpr(n.Value)
	case *ast.VarDeclStmt:
		return exprContainsCanExpr(n.Value)
	case *ast.TupleBindStmt:
		return exprContainsCanExpr(n.Value)
	case *ast.ReturnStmt:
		return exprContainsCanExpr(n.Value)
	case *ast.ExprStmt:
		return exprContainsCanExpr(n.Expr)
	case *ast.DiscardStmt:
		return exprContainsCanExpr(n.Value)
	default:
		return false
	}
}
func exprContainsCanExpr(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return false
	case *ast.CanExpr:
		return true
	case *ast.AllocExpr:
		return exprContainsCanExpr(n.Owner) || exprContainsCanExpr(n.Value) || exprContainsCanExpr(n.NodeSpan)
	case *ast.BinaryExpr:
		return exprContainsCanExpr(n.Left) || exprContainsCanExpr(n.Right)
	case *ast.UnaryExpr:
		return exprContainsCanExpr(n.Operand)
	case *ast.CallExpr:
		if exprContainsCanExpr(n.Func) {
			return true
		}
		if exprContainsCanExpr(n.SafeReceiver) {
			return true
		}
		for _, arg := range n.Args {
			if exprContainsCanExpr(arg) {
				return true
			}
		}
		return false
	case *ast.FieldExpr:
		return exprContainsCanExpr(n.Object)
	case *ast.IndexExpr:
		return exprContainsCanExpr(n.Object) || exprContainsCanExpr(n.Index) || exprContainsCanExpr(n.Fallback)
	case *ast.SliceExpr:
		return exprContainsCanExpr(n.Object) || exprContainsCanExpr(n.Start) || exprContainsCanExpr(n.End)
	case *ast.CastExpr:
		return exprContainsCanExpr(n.Operand)
	case *ast.ListLitExpr:
		for _, item := range n.Elems {
			if exprContainsCanExpr(item) {
				return true
			}
		}
		return exprContainsCanExpr(n.Owner)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			if exprContainsCanExpr(arg) {
				return true
			}
		}
		return false
	case *ast.RecordUpdateExpr:
		if exprContainsCanExpr(n.Base) {
			return true
		}
		for _, arg := range n.Args {
			if exprContainsCanExpr(arg) {
				return true
			}
		}
		return false
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if exprContainsCanExpr(elem) {
				return true
			}
		}
		return false
	case *ast.ParenExpr:
		return exprContainsCanExpr(n.Inner)
	case *ast.TernaryExpr:
		return exprContainsCanExpr(n.Value) || exprContainsCanExpr(n.Cond) || exprContainsCanExpr(n.Alt)
	case *ast.AddrOfExpr:
		return exprContainsCanExpr(n.Operand)
	case *ast.SpecializeExpr:
		return exprContainsCanExpr(n.Operand)
	case *ast.TryExpr:
		return exprContainsCanExpr(n.Value) || exprContainsCanExpr(n.Fallback)
	case *ast.CatchExpr:
		return exprContainsCanExpr(n.Value)
	case *ast.UnwrapElseExpr:
		return exprContainsCanExpr(n.Value) || exprContainsCanExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		return exprContainsCanExpr(n.Value)
	default:
		return false
	}
}
func stmtContainsNodeSugar(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		return exprContainsNodeSugar(n.Value)
	case *ast.AsRefAssignStmt:
		return exprContainsNodeSugar(n.Value)
	case *ast.VarDeclStmt:
		return exprContainsNodeSugar(n.Value)
	case *ast.TupleBindStmt:
		return exprContainsNodeSugar(n.Value)
	case *ast.ReturnStmt:
		return exprContainsNodeSugar(n.Value)
	case *ast.ExprStmt:
		return exprContainsNodeSugar(n.Expr)
	case *ast.DiscardStmt:
		return exprContainsNodeSugar(n.Value)
	default:
		return false
	}
}
